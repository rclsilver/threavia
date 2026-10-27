// Package worker runs one existing provider integration inside an execution pod.
// Workers have no backend credential or Kubernetes management credential.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	claude "github.com/rclsilver/threavia/internal/backends/claude/runner"
	codex "github.com/rclsilver/threavia/internal/backends/codex/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

type Options struct {
	Provider, Binary, Scratch, Version, APIKey, RunID, JobID string
	Readable                                                 []string
	Probe                                                    bool
}
type native interface {
	runner.Runner
	Models(context.Context) (*backendv1.ModelCatalog, error)
	Authenticated(context.Context) (bool, bool)
	Quotas(context.Context) quotas.Status
}
type Factory func(*mcp.Server, mcp.Asker) (native, error)

type Worker struct {
	options  Options
	provider native
	tools    *mcp.Server
	peer     *bridge.Peer
	mu       sync.Mutex
	started  bool
	ended    bool
}

func Serve(ctx context.Context, conn io.ReadWriteCloser, options Options, logger *slog.Logger) error {
	return serve(ctx, conn, options, logger, nil)
}
func serve(ctx context.Context, conn io.ReadWriteCloser, o Options, logger *slog.Logger, factory Factory) error {
	w := &Worker{options: o, tools: mcp.New(logger)}
	lease := newLease()
	ready := make(chan struct{})
	w.peer = bridge.New(ctx, conn, func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		if method == "lease" {
			return nil, lease.renew(raw)
		}
		select {
		case <-ready:
			return w.handle(ctx, method, raw)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer w.peer.Close()
	go lease.watch(ctx, w.peer)
	asker := &remoteAsker{peer: w.peer, jobID: o.JobID}
	if factory != nil {
		var err error
		w.provider, err = factory(w.tools, asker)
		if err != nil {
			return err
		}
	} else {
		switch o.Provider {
		case "claude":
			w.provider = claude.NewClaude(o.Binary, w.tools, claude.Options{Readable: o.Readable, Scratch: o.Scratch}, logger)
		case "codex":
			w.provider = codex.New(o.Binary, w.tools, codex.Options{APIKey: o.APIKey, Version: o.Version, Scratch: o.Scratch, ExternalSandbox: true}, logger)
		default:
			return errors.New("unsupported execution provider")
		}
	}
	if w.provider == nil {
		return errors.New("execution provider was not initialized")
	}
	if c, ok := w.provider.(*codex.Codex); ok {
		c.SetAsker(asker)
	}
	w.tools.SetAsker(asker)
	if err := w.tools.Start(); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if closer, ok := w.provider.(interface{ Close(context.Context) error }); ok {
			_ = closer.Close(cleanup)
		}
		_ = w.tools.Close(cleanup)
	}()
	close(ready)
	<-w.peer.Done()
	w.mu.Lock()
	started, ended := w.started, w.ended
	w.mu.Unlock()
	if started && !ended {
		_ = w.provider.Cancel(o.JobID)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return w.peer.Err()
}
func (w *Worker) handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if method == "status" {
		if !w.options.Probe {
			return nil, errors.New("status is only available on a probe worker")
		}
		if err := w.provider.Available(); err != nil {
			return protocol.Status{Error: err.Error()}, nil
		}
		result := protocol.Status{Available: true}
		result.Authenticated, result.AuthKnown = w.provider.Authenticated(ctx)
		var err error
		result.Models, err = w.provider.Models(ctx)
		if err != nil {
			result.ModelError = err.Error()
		}
		result.Quotas = w.provider.Quotas(ctx)
		return result, nil
	}
	if w.options.Probe {
		return nil, errors.New("probe worker cannot run jobs")
	}
	switch method {
	case "run":
		var start protocol.Start
		if err := json.Unmarshal(raw, &start); err != nil {
			return nil, err
		}
		if start.Version != protocol.Version || start.Params.RunID != w.options.RunID || start.Params.JobID != w.options.JobID {
			return nil, errors.New("execution identity or protocol does not match")
		}
		w.mu.Lock()
		if w.started {
			w.mu.Unlock()
			return nil, errors.New("execution already started")
		}
		w.started = true
		w.mu.Unlock()
		for _, tool := range start.Tools {
			t := tool
			start.Params.ExternalTools = append(start.Params.ExternalTools, mcp.LocalTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, RawResult: t.RawResult, Call: func(ctx context.Context, input map[string]any) (any, error) {
				var result any
				err := w.peer.Call(ctx, "local", protocol.Ask{Name: t.Name, Input: input}, &result)
				return result, err
			}})
		}
		err := w.provider.Run(ctx, start.Params, &remoteSink{peer: w.peer, runID: w.options.RunID, jobID: w.options.JobID})
		w.mu.Lock()
		w.ended = true
		w.mu.Unlock()
		return nil, err
	case "inject":
		var input protocol.Input
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return nil, w.provider.Inject(w.options.JobID, input.Text, input.Interrupt)
	case "cancel":
		return nil, w.provider.Cancel(w.options.JobID)
	case "policy":
		var p policy.Policy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if updater, ok := w.provider.(interface{ UpdatePolicy(string, policy.Policy) }); ok {
			updater.UpdatePolicy(w.options.JobID, p)
			return nil, nil
		}
		return nil, fmt.Errorf("provider %s cannot update its policy", w.options.Provider)
	default:
		return nil, errors.New("unknown worker operation")
	}
}

type remoteAsker struct {
	peer  *bridge.Peer
	jobID string
}

func (a *remoteAsker) check(job string) error {
	if job != a.jobID || job == "" {
		return errors.New("cross-job request refused")
	}
	return nil
}
func (a *remoteAsker) AskPermission(ctx context.Context, job, name string, input map[string]any) (mcp.Decision, error) {
	if err := a.check(job); err != nil {
		return mcp.Decision{}, err
	}
	var result mcp.Decision
	err := a.peer.Call(ctx, "permission", protocol.Ask{Name: name, Input: input}, &result)
	return result, err
}
func (a *remoteAsker) AskValidation(ctx context.Context, job, name string, input map[string]any) (mcp.Decision, error) {
	if err := a.check(job); err != nil {
		return mcp.Decision{}, err
	}
	var result mcp.Decision
	err := a.peer.Call(ctx, "validation", protocol.Ask{Name: name, Input: input}, &result)
	return result, err
}
func (a *remoteAsker) AskUser(ctx context.Context, job, prompt string, choices []string, free bool) (string, error) {
	if err := a.check(job); err != nil {
		return "", err
	}
	var result string
	err := a.peer.Call(ctx, "question", protocol.Ask{Prompt: prompt, Choices: choices, FreeText: free}, &result)
	return result, err
}
func (a *remoteAsker) CallCoreTool(ctx context.Context, job, name string, input map[string]any, file string) (map[string]any, error) {
	if err := a.check(job); err != nil {
		return nil, err
	}
	var result map[string]any
	err := a.peer.Call(ctx, "core", protocol.Ask{Name: name, Input: input, FileInput: file}, &result)
	return result, err
}
func (a *remoteAsker) ReviewCheckpoint(ctx context.Context, job string) error {
	if err := a.check(job); err != nil {
		return err
	}
	return a.peer.Call(ctx, "review", nil, nil)
}
