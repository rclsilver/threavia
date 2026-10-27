package api_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestResultFollowUpRefusesUnfinishedStaleAndForeignSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newCore(t)
	project := c.createProject("learnings")
	backendID, credential := c.registerBackend("learnings")
	backend := c.connectBackend(credential)
	session := c.startSession(project, backendID, "", "verify the resolution")
	cmd := receive(t, "source job", backend.starts)
	path := "/api/v1/sessions/" + session + "/messages"
	body := map[string]any{"message": "Propose learnings; do not save anything yet.", "afterJobId": cmd.GetJobId(), "delivery": "QUEUE"}
	// A visible response is not sufficient while the source Job is unfinished.
	c.mustDo(http.MethodPost, path, body, nil, http.StatusConflict)
	if got := len(c.snapshot(session).Jobs); got != 1 {
		t.Fatalf("rejected capture queued a job: %d", got)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobStarted(ctx, cmd.GetRunId(), cmd.GetJobId())
	}))
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, cmd.GetRunId(), cmd.GetJobId(), "verified", nil)
	}))
	waitUntil(t, "source completed", func() bool {
		job, err := c.store.JobByID(ctx, domain.JobID(cmd.GetJobId()))
		return err == nil && job.Status == domain.JobCompleted
	})
	c.asUser("alice").mustDo(http.MethodPost, path, body, nil, http.StatusNotFound)
	c.mustDo(http.MethodPost, path, map[string]any{"message": "capture", "afterJobId": cmd.GetJobId(), "delivery": "NOW"}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPost, path, map[string]any{"message": "capture", "afterJobId": ""}, nil, http.StatusBadRequest)
	// Another client starts work between the first client's review and send.
	c.mustDo(http.MethodPost, path, map[string]any{"message": "new work"}, nil, http.StatusCreated)
	receive(t, "new work", backend.starts)
	c.mustDo(http.MethodPost, path, body, nil, http.StatusConflict)
	if got := len(c.snapshot(session).Jobs); got != 2 {
		t.Fatalf("stale capture queued a job: %d", got)
	}
}

func TestResultFollowUpIsIdempotentAcrossConcurrentClients(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newCore(t)
	project := c.createProject("concurrent learnings")
	backendID, credential := c.registerBackend("learnings")
	backend := c.connectBackend(credential)
	session := c.startSession(project, backendID, "", "verify")
	cmd := receive(t, "source job", backend.starts)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobStarted(ctx, cmd.GetRunId(), cmd.GetJobId())
	}))
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, cmd.GetRunId(), cmd.GetJobId(), "verified", nil)
	}))
	waitUntil(t, "completed source", func() bool {
		job, err := c.store.JobByID(ctx, domain.JobID(cmd.GetJobId()))
		return err == nil && job.Status == domain.JobCompleted
	})
	var wg sync.WaitGroup
	type outcome struct {
		job domain.Job
		err error
	}
	results := make(chan outcome, 8)
	for range 8 {
		wg.Go(func() {
			job, err := c.svc.PostMessageFromResult(ctx, auth.Identity{UserID: "thomas"}, domain.SessionID(session), domain.JobID(cmd.GetJobId()), "Propose learnings only", "capture-learnings:"+cmd.GetJobId(), "QUEUE")
			results <- outcome{job, err}
		})
	}
	wg.Wait()
	close(results)
	var id domain.JobID
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent capture: %v", result.err)
		}
		if id != "" && id != result.job.ID {
			t.Fatalf("two jobs for one result: %s and %s", id, result.job.ID)
		}
		id = result.job.ID
	}
	if got := len(c.snapshot(session).Jobs); got != 2 {
		t.Fatalf("jobs=%d, want source+one capture", got)
	}
	next := receive(t, "capture launch", backend.starts)
	if next.GetJobId() != string(id) {
		t.Fatal("wrong job dispatched")
	}
	expectNothing(t, "duplicate capture launch", backend.starts)
	// Retrying the accepted HTTP command replays it even though it is running.
	var replay domain.Job
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Propose learnings only", "afterJobId": cmd.GetJobId()}, &replay, http.StatusCreated, [2]string{"Idempotency-Key", "capture-learnings:" + cmd.GetJobId()})
	if replay.ID != id {
		t.Fatal("retry created another turn")
	}
}
