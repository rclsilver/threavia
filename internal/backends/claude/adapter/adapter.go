package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
)

// Adapter implements the backend SDK Handler for Claude Code.
//
// This is the translation skeleton: the control plane side is complete
// (handshake, status reporting, command routing, durable event buffering), while
// actually driving Claude Code is delegated to the runner, which is the next
// implementation step. Unimplemented commands are explicitly rejected rather
// than silently accepted, so Core never believes work started when it did not.
type Adapter struct {
	client.BaseHandler

	cfg    Config
	runner runner.Runner
	logger *slog.Logger

	mu     sync.RWMutex
	client *client.Client
}

// New builds the Claude adapter.
func New(cfg Config, claudeRunner runner.Runner, logger *slog.Logger) *Adapter {
	return &Adapter{cfg: cfg, runner: claudeRunner, logger: logger}
}

// Bind gives the adapter the SDK client it reports through. It is called once,
// after the client is built, because the client needs the handler at
// construction time.
func (a *Adapter) Bind(c *client.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = c
}

func (a *Adapter) sdk() *client.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client
}

// OnConnected reports the initial operational status to Core.
//
// A missing Claude Code CLI is reported as DEGRADED with an explanatory
// condition rather than as a startup failure: the backend stays reachable and
// the user can see why it cannot work.
func (a *Adapter) OnConnected(ctx context.Context, welcome *backendv1.Welcome) error {
	sdk := a.sdk()
	if sdk == nil {
		return nil
	}

	status := backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY
	var conditions []*backendv1.Condition

	if available, ok := a.runner.(interface{ Available() error }); ok {
		if err := available.Available(); err != nil {
			status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
			conditions = append(conditions, &backendv1.Condition{
				Type:    "ProviderAvailable",
				Status:  "False",
				Reason:  "ExecutableNotFound",
				Message: err.Error(),
			})
		}
	}

	// Provider authentication is entirely backend-owned; Core only ever sees
	// this coarse state. Reporting it accurately is part of the runner work.
	providerAuth := backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_UNSPECIFIED

	a.logger.Info("reporting backend status",
		slog.String("connectionId", welcome.GetConnectionId()),
		slog.String("status", status.String()),
	)
	return sdk.SendStatus(ctx, status, providerAuth, conditions...)
}

// OnDisconnected logs the loss of the control stream. Local execution keeps
// running: a Core outage must never stop agent work.
func (a *Adapter) OnDisconnected(_ context.Context, cause error) {
	if cause != nil {
		a.logger.Warn("disconnected from core", slog.String("cause", cause.Error()))
		return
	}
	a.logger.Info("disconnected from core")
}

// OnStartJob starts or resumes the provider native session for a Run.
func (a *Adapter) OnStartJob(ctx context.Context, cmd *backendv1.StartJob) error {
	params := runner.StartParams{
		RunID:            cmd.GetRunId(),
		JobID:            cmd.GetJobId(),
		NativeSessionID:  cmd.GetNativeSessionId(),
		WorkingDirectory: cmd.GetProjectContext().GetWorkingDirectoryPath(),
		Prompt:           cmd.GetPrompt(),
	}

	a.logger.Info("start job requested",
		slog.String("runId", params.RunID),
		slog.String("jobId", params.JobID),
		slog.Bool("resume", params.NativeSessionID != ""),
		slog.String("workingDirectory", params.WorkingDirectory),
	)

	var err error
	if params.NativeSessionID == "" {
		_, err = a.runner.Start(ctx, params)
	} else {
		_, err = a.runner.Resume(ctx, params)
	}
	if err != nil {
		return fmt.Errorf("start job %s: %w", params.JobID, err)
	}
	return nil
}

// OnCancelJob asks the runner to stop a Job. Core only moves the Job to
// CANCELLED once the backend confirms the stop.
func (a *Adapter) OnCancelJob(ctx context.Context, cmd *backendv1.CancelJob) error {
	a.logger.Info("cancel job requested",
		slog.String("runId", cmd.GetRunId()),
		slog.String("jobId", cmd.GetJobId()),
		slog.String("reason", cmd.GetReason()),
	)
	if err := a.runner.Cancel(ctx, cmd.GetJobId()); err != nil {
		return fmt.Errorf("cancel job %s: %w", cmd.GetJobId(), err)
	}
	return nil
}
