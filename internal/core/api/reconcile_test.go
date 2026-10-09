package api_test

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

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

// TestAJobTheBackendAlreadyEndedIsSettled is a regression test.
//
// Core held every event the backend had produced and still believed the Job
// live, because the ending arrived in a state the Job state machine refused.
// Reconnecting then found the two sides disagreeing with nothing left to
// replay, and left the Job parked: the Session stayed silent for good. The
// backend owns what actually happened (spec section 9), so Core takes its word
// and lets the queue move.
func TestAJobTheBackendAlreadyEndedIsSettled(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "premier")
	first := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	message := backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.AgentMessage(ctx, first.GetRunId(), first.GetJobId(), "voila")
	})
	backend.emit(t, ctx, message)
	waitUntil(t, "core to persist what the backend sent", func() bool {
		return c.countEvents(session, "agent.message") == 1
	})

	var second idOnly
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages",
		map[string]any{"message": "deuxieme"}, &second, http.StatusCreated)

	c.svc.Disconnected(ctx, backendInstance(t, c), "stale-connection")
	waitUntil(t, "the job to be parked", func() bool {
		return c.jobStatus(session, first.GetJobId()) == "WAITING_BACKEND"
	})

	// The backend reports the Job finished, and Core is not missing a single
	// event: there is nothing left to converge through.
	c.svc.ReconcileState(ctx, backendInstance(t, c), &backendv1.ReconcileState{
		Runs: []*backendv1.RunState{{
			RunId: first.GetRunId(),
			Jobs: []*backendv1.JobState{{
				JobId:               first.GetJobId(),
				Status:              backendv1.JobStatus_JOB_STATUS_COMPLETED,
				LastBackendSequence: message.GetBackendSequence(),
			}},
		}},
	})

	waitUntil(t, "the job to settle on what the backend reported", func() bool {
		return c.jobStatus(session, first.GetJobId()) == "COMPLETED"
	})

	next := receive(t, "the queued message to be dispatched", backend.starts)
	if next.GetJobId() != second.ID {
		t.Fatalf("next job = %s, want the queued one %s", next.GetJobId(), second.ID)
	}
}

// TestDeletingASessionIsRefusedWhileItRuns covers the one thing deleting a
// Session must not do: leave a Job going on a machine with nowhere to report.
func TestDeletingASessionIsRefusedWhileItRuns(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)

	c.mustDo(http.MethodDelete, "/api/v1/sessions/"+session, nil, nil, http.StatusConflict)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "fait", nil)
	}))
	waitUntil(t, "the job to end", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "COMPLETED"
	})

	// Nothing is running now, so it goes, and with it the timeline that only
	// existed inside it.
	c.mustDo(http.MethodDelete, "/api/v1/sessions/"+session, nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, nil, http.StatusNotFound)

	var listed struct {
		Items []idOnly `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/sessions?includeArchived=true",
		nil, &listed, http.StatusOK)
	if len(listed.Items) != 0 {
		t.Fatalf("%d sessions left, want none", len(listed.Items))
	}
}

// TestADecisionLostWithTheStreamIsSentAgain pins the one failure a client
// cannot recover from on its own.
//
// A decision travels Core to backend once, over the control stream, without an
// acknowledgement. If the stream dies between the answer and its delivery, the
// answer is gone: the request is already RESOLVED, which is exactly what stops
// anyone from answering it a second time, so the tool call it was meant to
// unblock waits for ever and every later message queues behind a Session that
// is wedged by a question that was, in fact, answered.
//
// Reconnecting has to put it back.
func TestADecisionLostWithTheStreamIsSentAgain(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Modifie la configuration")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	payload, err := structpb.NewStruct(map[string]any{"tool": "Bash", "command": "git push"})
	if err != nil {
		t.Fatalf("building the payload: %v", err)
	}
	requested := backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.ValidationRequested{RequestId: "req-1", Title: "git push", RequestPayload: payload})
	})
	backend.emit(t, ctx, requested)
	waitUntil(t, "the job to wait for validation", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_VALIDATION"
	})

	var pending snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &pending, http.StatusOK)
	if len(pending.Attention.Validations) != 1 {
		t.Fatalf("%d pending validations, want 1", len(pending.Attention.Validations))
	}

	// The answer is given at the moment the stream is dying. Core sends it
	// once; draining it here is that loss, the frame that left Core and
	// reached nobody.
	c.mustDo(http.MethodPost, "/api/v1/validations/"+pending.Attention.Validations[0].ID+"/resolve",
		map[string]any{"approved": true, "channel": "web"}, nil, http.StatusOK)
	receive(t, "the decision Core sends once", backend.validations)

	c.svc.Disconnected(ctx, backendInstance(t, c), "stale-connection")
	waitUntil(t, "the job to be parked", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_BACKEND"
	})

	// The backend never restarted: it still runs the Job, and is still blocked
	// on the question nobody can answer twice.
	c.svc.ReconcileState(ctx, backendInstance(t, c), &backendv1.ReconcileState{
		Runs: []*backendv1.RunState{{
			RunId: start.GetRunId(),
			Jobs: []*backendv1.JobState{{
				JobId:               start.GetJobId(),
				Status:              backendv1.JobStatus_JOB_STATUS_RUNNING,
				LastBackendSequence: requested.GetBackendSequence(),
			}},
		}},
	})

	resolution := receive(t, "the decision to be sent again", backend.validations)
	if resolution.GetRequestId() != "req-1" {
		t.Fatalf("replayed request %q, want req-1", resolution.GetRequestId())
	}
	if !resolution.GetApproved() {
		t.Fatalf("the replayed decision is a refusal, want the approval that was given")
	}
}

// TestADispatchOutlivesTheRequestThatAskedForIt pins a failure that showed up
// as a flaky test and was neither flaky nor a test problem.
//
// A dispatch is called from an HTTP handler, from a gRPC stream and from
// reconciliation, and every one of those contexts can end while Core is still
// reading what the Job needs. The reads then failed with "context canceled",
// each one was logged and skipped, and the Job went out anyway — with no
// decisions, no tasks and the default policy. An agent cannot tell that apart
// from a project that has never decided anything: it simply works as though
// nothing had been, and the ruling someone recorded so every later session
// would know goes unread.
func TestADispatchOutlivesTheRequestThatAskedForIt(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"decision_create", map[string]any{
			"title":      "Deploy with Puppet",
			"content":    "Ansible was tried and rejected.",
			"importance": "IMPORTANT",
		})

	// Park it, so dispatching it again is a thing Core will do.
	c.svc.Disconnected(ctx, backendInstance(t, c), "stale-connection")
	waitUntil(t, "the job to be parked", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_BACKEND"
	})

	// The caller is already gone by the time the dispatch runs.
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	c.svc.DispatchJob(dead, domain.JobID(start.GetJobId()))

	again := receive(t, "the job to be dispatched again", backend.starts)
	if decisions := again.GetProjectContext().GetDecisions(); len(decisions) != 1 {
		t.Fatalf("the re-dispatched job carries %d decisions, want the one that was recorded", len(decisions))
	}
	if again.GetExecutionPolicy() == nil {
		t.Error("the re-dispatched job carries no execution policy")
	}
}

// TestABackendOnlyBindsItsOwnRuns is a regression test.
//
// A reconnecting backend reports the native session of each Run it knows, and
// Core recorded every one of them. A backend could name a Run of another
// backend, another person's included, and choose the session its next Job
// would resume.
func TestABackendOnlyBindsItsOwnRuns(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	laptop := backendInstance(t, c)
	c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)
	alices, _ := c.asUser("alice").registerBackend("alice-laptop")

	ctx := context.Background()
	report := func(native string) *backendv1.ReconcileState {
		return &backendv1.ReconcileState{Runs: []*backendv1.RunState{{
			RunId:           start.GetRunId(),
			NativeSessionId: native,
			Jobs: []*backendv1.JobState{{
				JobId:  start.GetJobId(),
				Status: backendv1.JobStatus_JOB_STATUS_RUNNING,
			}},
		}}}
	}
	nativeSession := func() string {
		run, err := c.store.RunByID(ctx, domain.RunID(start.GetRunId()))
		if err != nil {
			t.Fatalf("reading the run: %v", err)
		}
		if run.NativeSessionID == nil {
			return ""
		}
		return *run.NativeSessionID
	}

	c.svc.ReconcileState(ctx, domain.BackendInstanceID(alices), report("chosen-by-alice"))
	if got := nativeSession(); got != "" {
		t.Fatalf("another user's backend bound the run to %q", got)
	}

	// An id that would read as an option on the provider's command line is
	// refused even from the Run's own backend.
	c.svc.ReconcileState(ctx, laptop, report("--dangerously-skip-permissions"))
	if got := nativeSession(); got != "" {
		t.Fatalf("a malformed native session was bound: %q", got)
	}

	c.svc.ReconcileState(ctx, laptop, report("native-1"))
	if got := nativeSession(); got != "native-1" {
		t.Fatalf("native session = %q, want the one its own backend reported", got)
	}
}
