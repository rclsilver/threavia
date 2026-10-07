package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// project, directory and session shapes the tests decode into.
type (
	idOnly struct {
		ID string `json:"id"`
	}
	startSessionResponse struct {
		Session struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"session"`
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	snapshotResponse struct {
		Session struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"session"`
		Runs []struct {
			ID                string `json:"id"`
			BackendInstanceID string `json:"backendInstanceId"`
		} `json:"runs"`
		Jobs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"jobs"`
		Events []struct {
			Sequence int64           `json:"sequence"`
			Type     string          `json:"type"`
			Payload  json.RawMessage `json:"payload"`
		} `json:"events"`
		Attention struct {
			Validations []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"validations"`
			UserInputs []struct {
				ID     string `json:"id"`
				Prompt string `json:"prompt"`
			} `json:"userInputs"`
		} `json:"attention"`
		Cursor int64 `json:"cursor"`
	}
)

// setup builds a Project with a bound KnownDirectory and a connected backend,
// which is the starting point of the target user story.
func setup(t *testing.T) (*core, *fakeBackend, string, string) {
	t.Helper()

	c := newCore(t)
	backendID, credential := c.registerBackend("laptop")

	var project idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects",
		map[string]any{"name": "homelab", "description": "Homelab"}, &project, http.StatusCreated)

	var dir idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects/"+project.ID+"/directories",
		map[string]any{"name": "puppet", "description": "Configuration Puppet"}, &dir, http.StatusCreated)

	c.mustDo(http.MethodPost, "/api/v1/directories/"+dir.ID+"/bindings",
		map[string]any{"backendInstanceId": backendID, "path": "/home/thomas/git/puppet"}, nil, http.StatusOK)

	backend := c.connectBackend(credential)
	return c, backend, project.ID, dir.ID
}

// TestFirstSendCreatesEverythingAtomically covers acceptance criteria 4 and 5:
// the first message creates Session, Run and Job in one operation, and the
// backend is told to work in the resolved physical directory.
func TestFirstSendCreatesEverythingAtomically(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{
		"projectId":          projectID,
		"backendInstanceId":  c.backendID,
		"workingDirectoryId": dirID,
		"message":            "Analyse ce projet",
	}, &started, http.StatusCreated)

	if started.Session.ID == "" || started.Run.ID == "" || started.Job.ID == "" {
		t.Fatalf("the first send must create a session, a run and a job: %+v", started)
	}
	if started.Session.Title != "Analyse ce projet" {
		t.Errorf("generated title = %q, want the first message", started.Session.Title)
	}

	start := receive(t, "the dispatched job", backend.starts)
	if start.GetJobId() != started.Job.ID {
		t.Errorf("dispatched job = %s, want %s", start.GetJobId(), started.Job.ID)
	}
	if start.GetPrompt() != "Analyse ce projet" {
		t.Errorf("prompt = %q, want the first message", start.GetPrompt())
	}
	if got := start.GetProjectContext().GetWorkingDirectoryPath(); got != "/home/thomas/git/puppet" {
		t.Errorf("working directory = %q, want the path bound on this backend", got)
	}
	// A fresh Run has no provider native session yet.
	if start.GetNativeSessionId() != "" {
		t.Errorf("native session = %q, want empty on a first run", start.GetNativeSessionId())
	}
}

// TestAgentOutputReachesTheTimeline covers acceptance criteria 6 and 7: backend
// output becomes history, and a replayed event does not duplicate it.
func TestAgentOutputReachesTheTimeline(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	native := domain.NewUUID()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.NativeSessionBound(ctx, start.GetRunId(), start.GetJobId(), native)
	}))
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), "Je regarde le projet.")
	}))

	waitUntil(t, "the agent message to reach the timeline", func() bool {
		return c.countEvents(session, "agent.message") == 1
	})

	// Replaying the same event is deduplicated on the backend event id.
	message, err := backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), "duplicated")
	if err != nil {
		t.Fatalf("building an event: %v", err)
	}
	backend.emit(t, ctx, message)
	backend.emit(t, ctx, message)

	waitUntil(t, "the duplicated event to be recorded once", func() bool {
		return c.countEvents(session, "agent.message") == 2
	})
	time.Sleep(200 * time.Millisecond)
	if got := c.countEvents(session, "agent.message"); got != 2 {
		t.Fatalf("%d agent messages, want 2: a replay must not duplicate history", got)
	}

	// The native session is recorded on the Run, not shown as an event.
	run, err := c.store.RunByID(ctx, domain.RunID(start.GetRunId()))
	if err != nil {
		t.Fatalf("reading the run: %v", err)
	}
	if run.NativeSessionID == nil || *run.NativeSessionID != native {
		t.Fatalf("native session = %v, want %s", run.NativeSessionID, native)
	}
}

// TestValidationWaitsAndResolves covers acceptance criterion 8: a validation
// waits indefinitely, is visible as pending attention, and resolving it once
// clears it everywhere and releases the agent.
func TestValidationWaitsAndResolves(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Modifie la configuration")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	payload, err := structpb.NewStruct(map[string]any{"tool": "Write", "path": "roles/foo/tasks/main.yml"})
	if err != nil {
		t.Fatalf("building the payload: %v", err)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.ValidationRequested{
				RequestId:      "req-1",
				Title:          "Write roles/foo/tasks/main.yml",
				RequestPayload: payload,
			})
	}))

	// The Job waits, with no timeout.
	waitUntil(t, "the job to wait for validation", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_VALIDATION"
	})

	var pending snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &pending, http.StatusOK)
	if len(pending.Attention.Validations) != 1 {
		t.Fatalf("%d pending validations, want 1", len(pending.Attention.Validations))
	}
	validationID := pending.Attention.Validations[0].ID

	c.mustDo(http.MethodPost, "/api/v1/validations/"+validationID+"/resolve",
		map[string]any{"approved": true, "channel": "web"}, nil, http.StatusOK)

	resolution := receive(t, "the validation resolution", backend.validations)
	if !resolution.GetApproved() || resolution.GetRequestId() != "req-1" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}

	// Resolved means gone from pending attention, for every client.
	var after snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &after, http.StatusOK)
	if len(after.Attention.Validations) != 0 {
		t.Fatalf("%d validations still pending after resolution, want 0", len(after.Attention.Validations))
	}
	waitUntil(t, "the job to resume", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "RUNNING"
	})

	// A second client answering the same request is told it is already resolved.
	c.mustDo(http.MethodPost, "/api/v1/validations/"+validationID+"/resolve",
		map[string]any{"approved": false, "channel": "android"}, nil, http.StatusConflict)
}

// TestUserInputWaitsAndResolves pins the second persistent attention object,
// distinct from a validation: it asks for information, not permission.
func TestUserInputWaitsAndResolves(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Deploie")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.UserInputRequested{
				RequestId: "ask-1",
				Prompt:    "Quel environnement ?",
				Choices:   []string{"staging", "production"},
			})
	}))

	waitUntil(t, "the job to wait for input", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_INPUT"
	})

	var pending snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &pending, http.StatusOK)
	if len(pending.Attention.UserInputs) != 1 {
		t.Fatalf("%d pending input requests, want 1", len(pending.Attention.UserInputs))
	}

	c.mustDo(http.MethodPost, "/api/v1/user-input/"+pending.Attention.UserInputs[0].ID+"/resolve",
		map[string]any{"value": "staging", "channel": "web"}, nil, http.StatusOK)

	answer := receive(t, "the input resolution", backend.inputs)
	if answer.GetValue() != "staging" {
		t.Fatalf("answer = %q, want staging", answer.GetValue())
	}
	waitUntil(t, "the job to resume", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "RUNNING"
	})
}

// TestSecondMessageResumesTheNativeSession covers acceptance criterion 12, the
// one the whole architecture exists for: a second message is a second Job on the
// same Run, and the backend is told to resume the same provider session.
func TestSecondMessageResumesTheNativeSession(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	first := receive(t, "the first job", backend.starts)

	ctx := context.Background()
	native := domain.NewUUID()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.NativeSessionBound(ctx, first.GetRunId(), first.GetJobId(), native)
	}))
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, first.GetRunId(), first.GetJobId(), "fait")
	}))

	waitUntil(t, "the first job to complete", func() bool {
		return c.jobStatus(session, first.GetJobId()) == "COMPLETED"
	})

	var second struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages",
		map[string]any{"message": "Et maintenant corrige le role foo"}, &second, http.StatusCreated)

	secondStart := receive(t, "the second job", backend.starts)
	if secondStart.GetRunId() != first.GetRunId() {
		t.Errorf("second job runs on %s, want the same run %s", secondStart.GetRunId(), first.GetRunId())
	}
	if secondStart.GetNativeSessionId() != native {
		t.Errorf("native session = %q, want the one bound by the first job %q", secondStart.GetNativeSessionId(), native)
	}
	if secondStart.GetPrompt() != "Et maintenant corrige le role foo" {
		t.Errorf("prompt = %q, want the second message", secondStart.GetPrompt())
	}
}

// TestQueuedJobsAreFIFOAndSerialised pins that a Run runs one Job at a time and
// starts the next one only when the slot frees up.
func TestQueuedJobsAreFIFOAndSerialised(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "premier")
	first := receive(t, "the first job", backend.starts)

	var second, third struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "deuxieme"}, &second, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "troisieme"}, &third, http.StatusCreated)

	// Nothing else starts while the first Job holds the active slot.
	expectNothing(t, "a second dispatch", backend.starts)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, first.GetRunId(), first.GetJobId(), "fait")
	}))

	next := receive(t, "the next queued job", backend.starts)
	if next.GetJobId() != second.ID {
		t.Fatalf("next job = %s, want the oldest queued one %s", next.GetJobId(), second.ID)
	}
	if next.GetPrompt() != "deuxieme" {
		t.Fatalf("prompt = %q, want the second message", next.GetPrompt())
	}
}

// TestCancellationNeedsBackendConfirmation covers acceptance criterion 11: a
// live Job goes to CANCELLING and only reaches CANCELLED once the backend says
// it actually stopped.
func TestCancellationNeedsBackendConfirmation(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Une longue tache")
	start := receive(t, "the dispatched job", backend.starts)

	c.mustDo(http.MethodPost, "/api/v1/jobs/"+start.GetJobId()+"/cancel",
		map[string]any{"reason": "changement d'avis"}, nil, http.StatusOK)

	if got := c.jobStatus(session, start.GetJobId()); got != "CANCELLING" {
		t.Fatalf("job status = %s, want CANCELLING before the backend confirms", got)
	}
	cancel := receive(t, "the cancel command", backend.cancels)
	if cancel.GetJobId() != start.GetJobId() {
		t.Fatalf("cancelled job = %s, want %s", cancel.GetJobId(), start.GetJobId())
	}

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCancelled(ctx, start.GetRunId(), start.GetJobId())
	}))
	waitUntil(t, "the job to reach CANCELLED", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "CANCELLED"
	})
}

// TestCancellingAQueuedJobIsImmediate pins the other half of the rule: a queued
// Job has nothing running on a backend to confirm a stop.
func TestCancellingAQueuedJobIsImmediate(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "premier")
	receive(t, "the first job", backend.starts)

	var queued struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "deuxieme"}, &queued, http.StatusCreated)

	c.mustDo(http.MethodPost, "/api/v1/jobs/"+queued.ID+"/cancel", map[string]any{}, nil, http.StatusOK)
	if got := c.jobStatus(session, queued.ID); got != "CANCELLED" {
		t.Fatalf("queued job status = %s, want CANCELLED", got)
	}
	expectNothing(t, "a cancel command for a job that never started", backend.cancels)
}

// TestWorkWaitsForAnOfflineBackend pins that enqueueing never depends on backend
// availability: the Job waits and is dispatched when the backend comes back.
func TestWorkWaitsForAnOfflineBackend(t *testing.T) {
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

	if started.Job.Status != "QUEUED" {
		t.Fatalf("job status = %s, want QUEUED while the backend is offline", started.Job.Status)
	}

	backend := c.connectBackend(credential)
	start := receive(t, "the job dispatched on reconnection", backend.starts)
	if start.GetJobId() != started.Job.ID {
		t.Fatalf("dispatched job = %s, want %s", start.GetJobId(), started.Job.ID)
	}
}

// TestAnUnboundDirectoryStillStarts pins that a missing binding is the
// backend's problem to solve, not the user's to pre-empt: Core accepts the
// Session, and the backend resolves the directory when the Job starts, asking
// the user only if it has to (spec section 11).
func TestAnUnboundDirectoryStillStarts(t *testing.T) {
	c := newCore(t)
	backendID, _ := c.registerBackend("laptop")

	var project idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects", map[string]any{"name": "homelab"}, &project, http.StatusCreated)

	var dir idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects/"+project.ID+"/directories",
		map[string]any{"name": "puppet"}, &dir, http.StatusCreated)

	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{
		"projectId":          project.ID,
		"backendInstanceId":  backendID,
		"workingDirectoryId": dir.ID,
		"message":            "Analyse",
	}, nil, http.StatusCreated)
}

// TestStartSessionIsIdempotent pins specification section 27: a retried first
// send returns the same Session rather than creating a second one.
func TestStartSessionIsIdempotent(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	key := domain.NewUUID()

	body := map[string]any{
		"projectId":          projectID,
		"backendInstanceId":  c.backendID,
		"workingDirectoryId": dirID,
		"message":            "Analyse ce projet",
	}

	var first, second startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", body, &first, http.StatusCreated, [2]string{"Idempotency-Key", key})
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", body, &second, http.StatusCreated, [2]string{"Idempotency-Key", key})

	if first.Session.ID != second.Session.ID || first.Job.ID != second.Job.ID {
		t.Fatalf("a retried first send created a second session: %s then %s", first.Session.ID, second.Session.ID)
	}
	receive(t, "the single dispatch", backend.starts)
	expectNothing(t, "a second dispatch for a retried request", backend.starts)
}
