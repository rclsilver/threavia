package adapter

import (
	"context"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
	"io"
	"log/slog"
	"testing"
	"time"
)

type authProbeRunner struct{ authenticated, known bool }

func (r *authProbeRunner) Authenticated(context.Context) (bool, bool) {
	return r.authenticated, r.known
}
func (*authProbeRunner) Run(context.Context, runner.StartParams, runner.Sink) error { return nil }
func (*authProbeRunner) Cancel(string) error                                        { return runner.ErrUnknownJob }
func (*authProbeRunner) Inject(string, string, bool) error                          { return runner.ErrUnknownJob }
func (*authProbeRunner) Available() error                                           { return nil }

func TestProviderReadinessKeepsConfirmedFailuresUntilRecovery(t *testing.T) {
	r := &authProbeRunner{known: true}
	a := New(Default(), r, state.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	if err := a.refreshProviderStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if a.providerBlock == nil || a.providerBlock.Reason != "AUTHENTICATION_REQUIRED" {
		t.Fatal(a.providerBlock)
	}
	r.known = false
	_ = a.refreshProviderStatus(ctx)
	if a.providerBlock == nil {
		t.Fatal("failed probe erased confirmed authentication failure")
	}
	r.authenticated = true
	r.known = true
	_ = a.refreshProviderStatus(ctx)
	if a.providerBlock != nil {
		t.Fatal("reauthentication did not restore readiness")
	}
	future := time.Now().Add(time.Hour)
	a.providerBlock = &runner.ProviderBlock{Reason: "QUOTA_EXCEEDED", RetryAt: &future}
	_ = a.refreshProviderStatus(ctx)
	if a.providerBlock == nil {
		t.Fatal("a valid login cleared an exhausted quota")
	}
	past := time.Now().Add(-time.Second)
	a.providerBlock.RetryAt = &past
	_ = a.refreshProviderStatus(ctx)
	if a.providerBlock != nil {
		t.Fatal("provider retry window stayed closed")
	}
}
