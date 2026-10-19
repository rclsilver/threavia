// This deterministic backend is only for the local human-review validation
// stack. It runs no model, shell command supplied by a user, or real project.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

type fixture struct {
	root   string
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}
type caller interface {
	CallCoreTool(context.Context, string, string, map[string]any, string) (map[string]any, error)
}

func (f *fixture) Available() error { return nil }
func (f *fixture) Inject(string, string, bool) error {
	return fmt.Errorf("this deterministic fixture only accepts queued correction turns")
}
func (f *fixture) Cancel(job string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cancel := f.cancel[job]
	if cancel == nil {
		return runner.ErrUnknownJob
	}
	cancel()
	return nil
}
func (f *fixture) Run(ctx context.Context, p runner.StartParams, s runner.Sink) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	f.mu.Lock()
	f.cancel[p.JobID] = cancel
	f.mu.Unlock()
	defer func() { f.mu.Lock(); delete(f.cancel, p.JobID); f.mu.Unlock() }()
	_ = s.JobStarted(ctx, p.RunID, p.JobID)
	call := s.(caller)
	fail := func(err error) error {
		if ctx.Err() != nil {
			return s.JobCancelled(context.Background(), p.RunID, p.JobID)
		}
		return s.JobFailed(ctx, p.RunID, p.JobID, "FIXTURE", err.Error(), nil)
	}
	extra := filepath.Join(f.root, "status-page-review")
	if _, err := call.CallCoreTool(ctx, p.JobID, "review_repository_register", map[string]any{"directory": extra, "name": "status-page"}, ""); err != nil {
		return fail(err)
	}
	correction := strings.Contains(p.Prompt, "Address this human code review")
	value := "replicaCount: 2\nservice:\n  port: 8080\n"
	if correction {
		value = "replicaCount: 3\nservice:\n  port: 8080\n"
	}
	if err := os.WriteFile(filepath.Join(p.WorkingDirectory, "apps", "ingress", "values.yaml"), []byte(value), 0600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(extra, "src", "health.ts"), []byte("export async function healthy(url: string): Promise<boolean> {\n  const response = await fetch(url);\n  return response.ok;\n}\n"), 0600); err != nil {
		return fail(err)
	}
	if correction {
		review, err := call.CallCoreTool(ctx, p.JobID, "review_read", map[string]any{}, "")
		if err != nil {
			return fail(err)
		}
		if threads, ok := review["threads"].([]any); ok {
			for _, raw := range threads {
				thread, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				_, err = call.CallCoreTool(ctx, p.JobID, "review_reply", map[string]any{"threadId": thread["id"], "body": "Local fixture: simulated correction applied (replicaCount: 3). No model was run. Resolve or reopen this discussion after inspecting the new revision."}, "")
				if err != nil {
					return fail(err)
				}
			}
		}
		_ = s.AgentMessage(ctx, p.RunID, p.JobID, "The local fixture applied its deterministic correction. Inspect the new revision and approve the review when ready. No model or production repository was used.")
		return s.JobCompleted(ctx, p.RunID, p.JobID, "Simulated review correction", nil)
	}
	_ = s.AgentMessage(ctx, p.RunID, p.JobID, "Synthetic changes are ready across two repositories. Open Code Review, add draft comments, then send them together. This fixture waits for human validation at its next action.")
	if _, err := call.CallCoreTool(ctx, p.JobID, "review_request", map[string]any{"required": true}, ""); err != nil {
		return fail(err)
	}
	for {
		err := s.(interface {
			ReviewCheckpoint(context.Context, string) error
		}).ReviewCheckpoint(ctx, p.JobID)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return fail(err)
		}
		if !strings.Contains(err.Error(), "human review is still pending") {
			return fail(err)
		}
	}
	return s.JobCompleted(ctx, p.RunID, p.JobID, "Human validation received; fixture continued", nil)
}

func prepare(root string) error {
	for _, name := range []string{"homelab-review", "status-page-review"} {
		dir := filepath.Join(root, name)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, "apps", "ingress"), 0700); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0700); err != nil {
			return err
		}
		files := map[string]string{"README.md": "# Synthetic review repository\n\nOnly used for local validation.\n", "apps/ingress/values.yaml": "replicaCount: 1\nservice:\n  port: 80\n", "src/health.ts": "export const healthy = true;\n"}
		for path, value := range files {
			if err := os.WriteFile(filepath.Join(dir, path), []byte(value), 0600); err != nil {
				return err
			}
		}
		for _, args := range [][]string{{"init", "--initial-branch", "main"}, {"add", "."}, {"-c", "user.name=Review fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "Synthetic baseline"}} {
			command := exec.Command("git", args...)
			command.Dir = dir
			if output, err := command.CombinedOutput(); err != nil {
				return fmt.Errorf("prepare fixture: %w: %s", err, output)
			}
		}
	}
	return nil
}

func request(ctx context.Context, api, method, path string, body, out any) error {
	var data bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&data).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, &data)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("fixture setup %s: HTTP %d", path, response.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(response.Body).Decode(out)
	}
	return nil
}

func run() error {
	api := flag.String("core-api", "http://127.0.0.1:18081", "isolated Core HTTP URL")
	address := flag.String("core-address", "127.0.0.1:19091", "isolated Core gRPC address")
	root := flag.String("workspace", "", "fixture-only data directory")
	flag.Parse()
	if *root == "" {
		return fmt.Errorf("--workspace must identify an isolated fixture directory")
	}
	if err := prepare(*root); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := adapter.Default()
	cfg.Registration.CoreAPI = *api
	cfg.Provider.DefaultWorkingDirectory = filepath.Join(*root, "homelab-review")
	cfg.Provider.ScratchPath = filepath.Join(*root, "scratch")
	cfg.Provider.SkillCachePath = filepath.Join(*root, "skills")
	cfg.Client.CoreAddress = *address
	cfg.Client.InstanceName = "Local review fixture (no model)"
	cfg.Client.BackendName = "review-fixture"
	cfg.Client.TLS.Enabled = false
	store, err := state.OpenSQLite(filepath.Join(*root, "backend.sqlite"), logger)
	if err != nil {
		return err
	}
	defer store.Close()
	var token struct{ Token string }
	if err = request(ctx, *api, "POST", "/api/v1/backend-tokens", map[string]any{"label": "Local review fixture"}, &token); err != nil {
		return err
	}
	cfg.Registration.Token = token.Token
	credential, err := adapter.Credential(ctx, cfg, store, logger)
	if err != nil {
		return err
	}
	cfg.Client.Token = credential
	provider := &fixture{root: *root, cancel: map[string]context.CancelFunc{}}
	handler := adapter.New(cfg, provider, store, logger)
	sdk, err := client.New(cfg.Client, handler, store, logger)
	if err != nil {
		return err
	}
	handler.Bind(sdk)
	go func() {
		if err := sdk.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("fixture connection stopped", "error", err)
		}
	}()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !sdk.Connected() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("fixture did not connect")
		case <-tick.C:
		}
	}
	var backends struct{ Items []struct{ ID, Name string } }
	if err = request(ctx, *api, "GET", "/api/v1/backends", nil, &backends); err != nil {
		return err
	}
	backendID := ""
	for _, backend := range backends.Items {
		if backend.Name == cfg.Client.InstanceName {
			backendID = backend.ID
		}
	}
	var projects struct{ Items []struct{ ID, Name string } }
	if err = request(ctx, *api, "GET", "/api/v1/projects", nil, &projects); err != nil {
		return err
	}
	// Seed once; restarts preserve the review and its drafts.
	projectID := ""
	for _, project := range projects.Items {
		if project.Name == "Local code review validation" {
			projectID = project.ID
		}
	}
	if projectID == "" {
		var project struct{ ID string }
		if err = request(ctx, *api, "POST", "/api/v1/projects", map[string]any{"name": "Local code review validation", "description": "Synthetic data; local fixture backend with no model."}, &project); err != nil {
			return err
		}
		projectID = project.ID
		var session struct{ Session struct{ ID string } }
		if err = request(ctx, *api, "POST", "/api/v1/sessions/start", map[string]any{"projectId": projectID, "backendInstanceId": backendID, "message": "Demonstrate mandatory human review across two synthetic repositories."}, &session); err != nil {
			return err
		}
		logger.Info("local review fixture seeded", "sessionId", session.Session.ID)
	}
	<-ctx.Done()
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
