package api_test

import (
	"context"
	"net/http"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// backendInstance returns the registered BackendInstance id as the domain type.
func backendInstance(t *testing.T, c *core) domain.BackendInstanceID {
	t.Helper()
	return domain.BackendInstanceID(c.backendID)
}

// TestJobFinishedDuringAnOutageIsNotRerun pins the rule of specification
// section 9 at its sharpest: Core owns desired state, but the backend owns what
// actually happened.
//
// Core parks a live Job when the control stream drops. If the backend finished
// it meanwhile, reconnecting must converge through the replayed events, never by
// running the work a second time.
func TestJobFinishedDuringAnOutageIsNotRerun(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Compte jusqu'a cinq")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()

	// The backend finishes the work while Core cannot hear it: the event is
	// buffered locally rather than lost.
	completed, err := backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "1 2 3 4 5", nil)
	if err != nil {
		t.Fatalf("building the completion event: %v", err)
	}
	// Core loses the connection and parks the Job.
	c.svc.Disconnected(ctx, backendInstance(t, c), "stale-connection")
	waitUntil(t, "the job to be parked", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_BACKEND"
	})

	// The backend comes back and reports the truth, then replays what Core
	// missed.
	c.svc.ReconcileState(ctx, backendInstance(t, c), &backendv1.ReconcileState{
		Runs: []*backendv1.RunState{{
			RunId: start.GetRunId(),
			Jobs: []*backendv1.JobState{{
				JobId:               start.GetJobId(),
				Status:              backendv1.JobStatus_JOB_STATUS_COMPLETED,
				LastBackendSequence: completed.GetBackendSequence(),
			}},
		}},
	})

	// The decisive assertion: the finished Job is not sent out again.
	expectNothing(t, "a second dispatch of a job the backend already finished", backend.starts)

	backend.emit(t, ctx, completed)
	waitUntil(t, "the job to converge to COMPLETED", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "COMPLETED"
	})

	if got := c.countEvents(session, "job.completed"); got != 1 {
		t.Fatalf("%d completions in the timeline, want 1", got)
	}
}

// TestJobTheBackendLostIsRequeued pins the other half: when the backend has no
// memory of a Job, nothing is running anywhere, so Core must send it again.
func TestJobTheBackendLostIsRequeued(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	c.svc.Disconnected(ctx, backendInstance(t, c), "stale-connection")
	waitUntil(t, "the job to be parked", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_BACKEND"
	})

	// The backend restarted and knows nothing about the Job.
	c.svc.ReconcileState(ctx, backendInstance(t, c), &backendv1.ReconcileState{})

	again := receive(t, "the job to be dispatched again", backend.starts)
	if again.GetJobId() != start.GetJobId() {
		t.Fatalf("dispatched %s, want the lost job %s", again.GetJobId(), start.GetJobId())
	}
	if again.GetPrompt() != start.GetPrompt() {
		t.Fatalf("prompt = %q, want the original one", again.GetPrompt())
	}
}

// TestParkedJobIsNotReleasedOnConnectAlone pins that reconnecting is not by
// itself a reason to run work again: only reconciliation can decide.
func TestParkedJobIsNotReleasedOnConnectAlone(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	c.svc.Disconnected(ctx, backendInstance(t, c), "stale-connection")
	waitUntil(t, "the job to be parked", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_BACKEND"
	})

	// A fresh connection, with no reconciliation report yet.
	if err := c.svc.Connected(ctx, backendInstance(t, c), "new-connection", &backendv1.Hello{
		Protocol:     &backendv1.ProtocolInfo{Version: 1},
		Capabilities: []backendv1.Capability{backendv1.Capability_CAPABILITY_CODE},
		Capacity:     &backendv1.Capacity{MaxConcurrentRuns: 1},
	}); err != nil {
		t.Fatalf("reconnecting: %v", err)
	}

	expectNothing(t, "a dispatch before the backend said what it had", backend.starts)
	_ = session
}

// TestQueuedWorkIsStillReleasedOnConnect keeps the fix from stalling work that
// was never dispatched anywhere.
func TestQueuedWorkIsStillReleasedOnConnect(t *testing.T) {
	c := newCore(t)
	backendID, credential := c.registerBackend("laptop")

	var project idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects", map[string]any{"name": "homelab"}, &project, http.StatusCreated)

	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{
		"projectId":         project.ID,
		"backendInstanceId": backendID,
		"message":           "Analyse ce projet",
	}, &started, http.StatusCreated)

	backend := c.connectBackend(credential)
	start := receive(t, "the queued job on connection", backend.starts)
	if start.GetJobId() != started.Job.ID {
		t.Fatalf("dispatched %s, want %s", start.GetJobId(), started.Job.ID)
	}
}
