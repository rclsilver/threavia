package api_test

import (
	"context"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/service"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

func resumeRun(t *testing.T, c *core, session string) domain.Run {
	t.Helper()
	var snapshot struct {
		Runs []domain.Run `json:"runs"`
	}
	c.mustDo("GET", "/api/v1/sessions/"+session, nil, &snapshot, 200)
	return snapshot.Runs[0]
}

func boundResumeSession(t *testing.T) (*core, *fakeBackend, string, *backendv1.StartJob) {
	t.Helper()
	c, backend, project, directory := setup(t)
	session := c.startSession(project, c.backendID, directory, "Remember the deployment plan")
	start := receive(t, "first job", backend.starts)
	ctx := context.Background()
	event, err := backend.events.NativeSessionBound(ctx, start.RunId, start.JobId, "old-native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), event); err != nil {
		t.Fatal(err)
	}
	return c, backend, session, start
}

func TestLostResumePreservesHistoryAndNextMessageStartsFresh(t *testing.T) {
	c, backend, session, first := boundResumeSession(t)
	ctx := context.Background()
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	failure, err := backend.events.JobFailed(ctx, first.RunId, first.JobId, resume.FailureCode("NOT_FOUND"), "Native session unavailable", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); err != nil {
		t.Fatal(err)
	}
	webLost := awaitFrame(t, web, "run.resume_unavailable")
	if other := awaitFrame(t, editor, "run.resume_unavailable"); other.event.Sequence != webLost.event.Sequence {
		t.Fatal("clients disagree about lost context")
	}
	before := c.snapshot(session)
	run := resumeRun(t, c, session)
	if run.ResumeStatus != domain.ResumeUnavailable || run.ResumeReason == nil || *run.ResumeReason != "NOT_FOUND" || run.NativeSessionID == nil {
		t.Fatal("lost historical binding", before.Runs)
	}
	count := len(lifecycleEvents(t, c, "run.resume_"))
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); err != nil {
		t.Fatal(err)
	}
	// A reconnecting old backend must not turn its remembered id into proof
	// that resuming that id actually worked.
	c.svc.ReconcileState(ctx, backendInstance(t, c), &backendv1.ReconcileState{Runs: []*backendv1.RunState{{RunId: first.RunId, NativeSessionId: "old-native"}}})
	if len(lifecycleEvents(t, c, "run.resume_")) != count || resumeRun(t, c, session).ResumeStatus != domain.ResumeUnavailable {
		t.Fatal("replay or reconcile resurrected the old session")
	}
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Continue with a fresh conversation"}, nil, 201)
	next := receive(t, "fresh job", backend.starts)
	if next.NativeSessionId != "" || next.RunId != first.RunId || next.Prompt != "Continue with a fresh conversation" {
		t.Fatal("resumed the lost id, changed run or rewrote prompt", next)
	}
	context := next.ProjectContext.GetProjectInstructions()
	if !strings.Contains(context, "Native provider conversation recovery reference") || !strings.Contains(context, "Remember the deployment plan") || strings.Contains(context, "Continue with a fresh conversation") || strings.Contains(context, "old-native") {
		t.Fatal("unsafe or missing reconstruction", context)
	}
	bound, err := backend.events.NativeSessionBound(ctx, next.RunId, next.JobId, "fresh-native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), bound); err != nil {
		t.Fatal(err)
	}
	awaitFrame(t, web, "run.resume_available")
	awaitFrame(t, editor, "run.resume_available")
	current := resumeRun(t, c, session)
	if current.ResumeStatus != domain.ResumeAvailable || current.ResumeReason != nil || current.NativeSessionID == nil || *current.NativeSessionID != "fresh-native" {
		t.Fatal(current)
	}
	if len(c.snapshot(session).Events) <= len(before.Events) {
		t.Fatal("history was replaced")
	}
	// Replaying a binding from an already failed Job cannot replace the new id.
	late, _ := backend.events.NativeSessionBound(ctx, first.RunId, first.JobId, "old-native")
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), late); err != nil {
		t.Fatal(err)
	}
	if got := resumeRun(t, c, session); *got.NativeSessionID != "fresh-native" {
		t.Fatal("late binding revived old conversation", got)
	}
}

func TestUnknownResumeFailureRetainsTheNativeBinding(t *testing.T) {
	c, backend, session, first := boundResumeSession(t)
	ctx := context.Background()
	failure, _ := backend.events.JobFailed(ctx, first.RunId, first.JobId, "PROVIDER_ERROR", "connection reset", nil)
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); err != nil {
		t.Fatal(err)
	}
	if run := resumeRun(t, c, session); run.ResumeStatus != domain.ResumeAvailable || len(lifecycleEvents(t, c, "run.resume_")) != 0 {
		t.Fatal("temporary error discarded binding", run)
	}
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Try again"}, nil, 201)
	next := receive(t, "retry on old native session", backend.starts)
	if next.NativeSessionId != "old-native" || strings.Contains(next.ProjectContext.GetProjectInstructions(), "recovery reference") {
		t.Fatal(next)
	}
}

func TestFailedResumeHandshakeIsUnknownUntilAnActualRetrySucceeds(t *testing.T) {
	c, backend, session, first := boundResumeSession(t)
	ctx := context.Background()
	failure, _ := backend.events.JobFailed(ctx, first.RunId, first.JobId, resume.AttemptFailureCode("UNKNOWN"), "Resume failed temporarily", nil)
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); err != nil {
		t.Fatal(err)
	}
	if run := resumeRun(t, c, session); run.ResumeStatus != domain.ResumeUnknown || run.ResumeReason == nil || *run.ResumeReason != "UNKNOWN" {
		t.Fatal(run)
	}
	c.svc.ReconcileState(ctx, backendInstance(t, c), &backendv1.ReconcileState{Runs: []*backendv1.RunState{{RunId: first.RunId, NativeSessionId: "old-native"}}})
	if run := resumeRun(t, c, session); run.ResumeStatus != domain.ResumeUnknown {
		t.Fatal("reconciliation claimed a successful retry", run)
	}
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Retry the same conversation"}, nil, 201)
	next := receive(t, "retry", backend.starts)
	if next.NativeSessionId != "old-native" {
		t.Fatal(next)
	}
	bound, _ := backend.events.NativeSessionBound(ctx, next.RunId, next.JobId, "old-native")
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), bound); err != nil {
		t.Fatal(err)
	}
	if run := resumeRun(t, c, session); run.ResumeStatus != domain.ResumeAvailable || run.ResumeReason != nil {
		t.Fatal(run)
	}
	events := lifecycleEvents(t, c, "run.resume_")
	if len(events) != 2 || events[0].Type != "run.resume_failed" || events[1].Type != "run.resume_available" {
		t.Fatal(events)
	}
}

func TestResumeLossAndTerminalEventRollBackTogether(t *testing.T) {
	c, backend, session, first := boundResumeSession(t)
	ctx := context.Background()
	lifecycleSQL(t, c, "ALTER TABLE events ADD CONSTRAINT reject_resume_loss CHECK (type <> 'run.resume_unavailable') NOT VALID")
	before := c.snapshot(session)
	failure, _ := backend.events.JobFailed(ctx, first.RunId, first.JobId, resume.FailureCode("PURGED"), "Native conversation purged", nil)
	subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
	defer unsubscribe()
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); err == nil {
		t.Fatal("accepted missing resume event")
	}
	after := c.snapshot(session)
	if resumeRun(t, c, session).ResumeStatus != domain.ResumeAvailable || len(after.Events) != len(before.Events) || c.jobStatus(session, first.JobId) == "FAILED" {
		t.Fatal("partial resume loss committed")
	}
	noLifecyclePublication(t, subscription, "run.")
	noLifecyclePublication(t, subscription, "job.")
	lifecycleSQL(t, c, "ALTER TABLE events DROP CONSTRAINT reject_resume_loss")
	if _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); err != nil {
		t.Fatal(err)
	}
	if len(lifecycleEvents(t, c, string(events.TypeRunResumeUnavailable))) != 1 {
		t.Fatal("retry did not persist one resume event")
	}
}

func TestDispatchAlreadyWaitingDuringResumeLossUsesFreshBinding(t *testing.T) {
	c, backend, session, first := boundResumeSession(t)
	ctx := context.Background()
	queued, err := c.svc.PostMessage(ctx, auth.Identity{UserID: "thomas"}, domain.SessionID(session), "Queued before the loss", "", service.DeliveryQueue)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := c.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	// Keep dispatch waiting without blocking the event's foreign-key check.
	if _, err := lock.Exec(ctx, "SELECT id FROM sessions WHERE id=$1 FOR NO KEY UPDATE", session); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := lock.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.svc.DispatchJob(ctx, queued.ID) }()
	waitUntil(t, "dispatch to hold its old context while waiting", func() bool {
		var blocked bool
		if err := c.db.Pool().QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		return blocked
	})
	failure, _ := backend.events.JobFailed(ctx, first.RunId, first.JobId, resume.FailureCode("NOT_FOUND"), "Native session missing", nil)
	failed := make(chan error, 1)
	go func() { _, err := c.svc.JobEvent(ctx, backendInstance(t, c), failure); failed <- err }()
	waitUntil(t, "loss to commit before waiting dispatch resumes", func() bool {
		run, err := c.store.RunByID(ctx, domain.RunID(first.RunId))
		if err != nil {
			t.Fatal(err)
		}
		return run.ResumeStatus == domain.ResumeUnavailable
	})
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	next := receive(t, "fresh queued job", backend.starts)
	if next.JobId != string(queued.ID) || next.NativeSessionId != "" || strings.Contains(next.ProjectContext.GetProjectInstructions(), "Queued before the loss") {
		t.Fatal("stale dispatch resumed old conversation or repeated prompt", next)
	}
	<-done
	if err := <-failed; err != nil {
		t.Fatal(err)
	}
}
