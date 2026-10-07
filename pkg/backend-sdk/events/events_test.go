package events

import (
	"context"
	"strconv"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

func newTestFactory() *Factory {
	var n int
	return NewFactory(state.NewMemoryStore(),
		WithIDFunc(func() string { n++; return "evt-" + strconv.Itoa(n) }),
		WithClock(func() time.Time { return time.Date(2026, 10, 6, 20, 7, 0, 0, time.UTC) }),
	)
}

// TestEventsCarryTheirBackendIdentity pins the two backend-side identifiers of
// specification section 4: a unique id for deduplication and a per-Job monotonic
// sequence for ordering and replay.
func TestEventsCarryTheirBackendIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	factory := newTestFactory()

	first, err := factory.JobStarted(ctx, "run-1", "job-1")
	if err != nil {
		t.Fatalf("building an event: %v", err)
	}
	second, err := factory.AgentMessage(ctx, "run-1", "job-1", "hello")
	if err != nil {
		t.Fatalf("building an event: %v", err)
	}

	if first.GetBackendEventId() == second.GetBackendEventId() {
		t.Error("each event must carry its own backend event id")
	}
	if first.GetBackendSequence() != 1 || second.GetBackendSequence() != 2 {
		t.Errorf("sequences = %d, %d, want 1, 2", first.GetBackendSequence(), second.GetBackendSequence())
	}
	if first.GetOccurredAt().AsTime().IsZero() {
		t.Error("an event must be timestamped")
	}

	// Sequences are per Job: unrelated concurrent Jobs are not serialised.
	other, err := factory.JobStarted(ctx, "run-1", "job-2")
	if err != nil {
		t.Fatalf("building an event: %v", err)
	}
	if other.GetBackendSequence() != 1 {
		t.Errorf("the first event of another job has sequence %d, want 1", other.GetBackendSequence())
	}
}

func TestEventBodies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	factory := newTestFactory()

	message, _ := factory.AgentMessage(ctx, "run-1", "job-1", "bonjour", "artifact-1")
	if got := message.GetAgentMessage().GetText(); got != "bonjour" {
		t.Errorf("agent message text = %q, want %q", got, "bonjour")
	}
	if got := message.GetAgentMessage().GetArtifactIds(); len(got) != 1 || got[0] != "artifact-1" {
		t.Errorf("artifact ids = %v, want [artifact-1]", got)
	}

	bound, _ := factory.NativeSessionBound(ctx, "run-1", "job-1", "native-42")
	if got := bound.GetNativeSessionBound().GetNativeSessionId(); got != "native-42" {
		t.Errorf("native session id = %q, want %q", got, "native-42")
	}

	failed, _ := factory.JobFailed(ctx, "run-1", "job-1", "PROVIDER_ERROR", "boom", nil)
	if got := failed.GetJobFailed().GetError().GetCode(); got != "PROVIDER_ERROR" {
		t.Errorf("error code = %q, want %q", got, "PROVIDER_ERROR")
	}

	completed, _ := factory.JobCompleted(ctx, "run-1", "job-1", "done", nil)
	if got := completed.GetJobCompleted().GetSummary(); got != "done" {
		t.Errorf("summary = %q, want %q", got, "done")
	}

	cancelled, _ := factory.JobCancelled(ctx, "run-1", "job-1")
	if cancelled.GetJobCancelled() == nil {
		t.Error("the cancellation body must be set")
	}

	validation, _ := factory.ValidationRequested(ctx, "run-1", "job-1", &backendv1.ValidationRequested{
		RequestId: "req-1",
		Title:     "Write to roles/foo/tasks/main.yml",
	})
	if got := validation.GetValidationRequested().GetRequestId(); got != "req-1" {
		t.Errorf("validation request id = %q, want %q", got, "req-1")
	}
}

// TestEventsRequireTheirScope keeps an unattributable event from being built.
func TestEventsRequireTheirScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	factory := newTestFactory()

	if _, err := factory.JobStarted(ctx, "", "job-1"); err == nil {
		t.Error("an event without a run id must be rejected")
	}
	if _, err := factory.JobStarted(ctx, "run-1", ""); err == nil {
		t.Error("an event without a job id must be rejected")
	}
}

// TestEphemeralEventsAreNotSequenced pins that liveness-only signals carry no
// durable identity: they are never persisted.
func TestEphemeralEventsAreNotSequenced(t *testing.T) {
	t.Parallel()

	factory := newTestFactory()
	event := factory.Ephemeral("run-1", "job-1", "agent.working", "reading files")

	if event.GetKind() != "agent.working" {
		t.Errorf("kind = %q, want %q", event.GetKind(), "agent.working")
	}
	if event.GetOccurredAt().AsTime().IsZero() {
		t.Error("an ephemeral event must still be timestamped")
	}
}
