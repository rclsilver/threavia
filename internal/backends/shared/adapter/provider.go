package adapter

import (
	"context"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ProviderBlocked is called only after the provider process and its tools have
// stopped. Core may redispatch the same Job later, never a second live attempt.
func (a *Adapter) ProviderBlocked(ctx context.Context, runID, jobID string, block *runner.ProviderBlock, usage *backendv1.Usage) error {
	copy := *block
	next := time.Now().UTC().Add(time.Minute)
	if copy.RetryAt == nil || !copy.RetryAt.After(time.Now()) {
		copy.RetryAt = &next
	}
	a.providerMu.Lock()
	a.providerBlock = &copy
	_ = a.reportProviderStatusLocked(ctx)
	supported := a.providerRecovery
	a.providerMu.Unlock()
	if !supported {
		return a.JobFailed(ctx, runID, jobID, copy.Reason, copy.Message, usage)
	}
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_WAITING_BACKEND)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobWaitingBackend(ctx, runID, jobID, &backendv1.JobWaitingBackend{
			Reason: copy.Reason, Message: copy.Message, RetryAt: timestamppb.New(*copy.RetryAt), Usage: usage, ActiveSeconds: copy.ActiveSeconds,
		})
	})
}

func (a *Adapter) refreshProviderStatus(ctx context.Context) error {
	// Serialize probes with observed refusals: a probe started before a failed
	// request must not overwrite that newer evidence with READY.
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	if reader, ok := a.runner.(runner.AuthReader); ok {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		authenticated, known := reader.Authenticated(probeCtx)
		cancel()
		if known && !authenticated {
			provider := a.cfg.Client.BackendName
			a.providerBlock = runner.ProviderBlockFor(provider, "AUTHENTICATION_REQUIRED", "")
		} else if known && authenticated && a.providerBlock != nil && a.providerBlock.Reason == "AUTHENTICATION_REQUIRED" {
			a.providerBlock = nil
		}
	}
	if block := a.providerBlock; block != nil && block.Reason != "AUTHENTICATION_REQUIRED" && block.RetryAt != nil && !block.RetryAt.After(time.Now()) {
		// This only opens a retry window. A real request, not a timer, proves
		// that the provider can work again.
		a.providerBlock = nil
	}
	return a.reportProviderStatusLocked(ctx)
}

func (a *Adapter) reportProviderStatusLocked(ctx context.Context) error {
	status := backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY
	auth := backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED
	var conditions []*backendv1.Condition
	if err := a.runner.Available(); err != nil {
		status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
		auth = backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED
		conditions = append(conditions, &backendv1.Condition{Type: "ProviderAvailable", Status: "False", Reason: "ExecutableNotFound", Message: err.Error()})
	} else if block := a.providerBlock; block != nil {
		status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
		kind := "ProviderQuotaAvailable"
		if block.Reason == "AUTHENTICATION_REQUIRED" {
			auth = backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED
			kind = "ProviderAuthenticated"
		}
		conditions = append(conditions, &backendv1.Condition{Type: kind, Status: "False", Reason: block.Reason, Message: block.Message})
	}
	if sdk := a.sdk(); sdk != nil {
		return sdk.SendStatus(ctx, status, auth, conditions...)
	}
	return nil
}
