package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func idleSessionForHandoff(t *testing.T) (*core, *fakeBackend, string, string, *backendv1.StartJob) {
	t.Helper()
	c, backend, project, directory := setup(t)
	session := c.startSession(project, c.backendID, directory, "Continue the deployment work")
	start := receive(t, "the source job", backend.starts)
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "Reviewed the manifests; deployment is still pending", nil)
	}))
	waitUntil(t, "source completion", func() bool { return c.jobStatus(session, start.GetJobId()) == "COMPLETED" })
	return c, backend, session, directory, start
}

func TestHandoffPreviewIsPrivateReadOnlyAndExplainsDirectory(t *testing.T) {
	c, _, session, dir, start := idleSessionForHandoff(t)
	target, _ := c.registerBackend("Destination")
	path := "/api/v1/sessions/" + session + "/handoff?backendInstanceId=" + target
	var preview service.HandoffPreview
	before := len(c.snapshot(session).Events)
	c.mustDo("GET", path, nil, &preview, 200)
	if !preview.CanMove || preview.FromRunID != domain.RunID(start.GetRunId()) || preview.NativeSessionTransferred || preview.WorkingDirectory.Resolution != "resolve" || preview.WorkingDirectory.KnownDirectoryID != domain.KnownDirectoryID(dir) || len(preview.Warnings) == 0 {
		t.Fatalf("wrong preview: %+v", preview)
	}
	if len(c.snapshot(session).Events) != before || len(c.snapshot(session).Runs) != 1 {
		t.Fatal("preview mutated the session")
	}
	c.asUser("other").mustDo("GET", path, nil, nil, 404)
	otherTarget, _ := c.asUser("other").registerBackend("Other destination")
	c.mustDo("GET", "/api/v1/sessions/"+session+"/handoff?backendInstanceId="+otherTarget, nil, nil, 404)
	c.mustDo("POST", "/api/v1/directories/"+dir+"/bindings", map[string]any{"backendInstanceId": target, "path": "/target/project"}, nil, 200)
	c.mustDo("GET", path, nil, &preview, 200)
	if preview.WorkingDirectory.Path != "/target/project" || preview.WorkingDirectory.Resolution != "bound" {
		t.Fatal(preview.WorkingDirectory)
	}
	c.mustDo("GET", "/api/v1/sessions/"+session+"/handoff?backendInstanceId=bad", nil, nil, 400)
}

func TestHandoffRefusesFullDrainingAndQueuedWork(t *testing.T) {
	c, _, session, _, _ := idleSessionForHandoff(t)
	target, _ := c.registerBackend("Capacity")
	ctx := context.Background()
	for _, capacity := range []domain.Capacity{{MaxConcurrentRuns: 0}, {MaxConcurrentRuns: 1, ActiveRuns: 1}} {
		lifecycleSQL(t, c, fmt.Sprintf("UPDATE backend_instances SET max_concurrent_runs=%d,active_runs=%d WHERE id='%s'", capacity.MaxConcurrentRuns, capacity.ActiveRuns, target))
		var preview service.HandoffPreview
		c.mustDo("GET", "/api/v1/sessions/"+session+"/handoff?backendInstanceId="+target, nil, &preview, 200)
		if preview.CanMove || len(preview.Reasons) == 0 {
			t.Fatal("capacity was ignored")
		}
		c.mustDo("PATCH", "/api/v1/sessions/"+session, map[string]any{"backendInstanceId": target}, nil, 409)
	}
	lifecycleSQL(t, c, fmt.Sprintf("UPDATE backend_instances SET max_concurrent_runs=1,active_runs=0 WHERE id='%s'", target))
	// Persist a queued Job before dispatch: a move must not abandon it on
	// its old Run, even though no Job holds the active slot yet.
	run, err := c.store.LatestRun(ctx, domain.SessionID(session))
	if err != nil {
		t.Fatal(err)
	}
	job := domain.Job{ID: domain.NewJobID(), RunID: run.ID, Status: domain.JobQueued, OriginChannel: domain.ChannelWeb}
	if err := c.store.CreateJob(ctx, &job); err != nil {
		t.Fatal(err)
	}
	c.mustDo("PATCH", "/api/v1/sessions/"+session, map[string]any{"backendInstanceId": target}, nil, 409)
	if len(c.snapshot(session).Runs) != 1 {
		t.Fatal("queued work was stranded")
	}
}

func TestHandoffHistoryAndAuditCommitTogether(t *testing.T) {
	for _, table := range []string{"audit_entries", "events"} {
		t.Run(table, func(t *testing.T) {
			c, _, session, _, _ := idleSessionForHandoff(t)
			target, _ := c.registerBackend("Rollback")
			column, action := "action", "session.backend_changed"
			if table == "events" {
				column, action = "type", "run.created"
			}
			lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT reject_handoff CHECK (%s <> '%s') NOT VALID", table, column, action))
			before := len(c.snapshot(session).Events)
			sub, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
			defer unsubscribe()
			if _, err := c.svc.MoveSessionToBackend(context.Background(), auth.Identity{UserID: "thomas"}, domain.SessionID(session), domain.BackendInstanceID(target), ""); err == nil {
				t.Fatal("failed history was accepted")
			}
			if len(c.snapshot(session).Runs) != 1 || len(c.snapshot(session).Events) != before || len(lifecycleAudit(t, c, "session.backend_changed")) != 0 {
				t.Fatal("failed history escaped its transaction")
			}
			noLifecyclePublication(t, sub, "run.")
		})
	}
}

func TestHandoffPreviewsCannotRaceAnotherMove(t *testing.T) {
	c, _, session, _, start := idleSessionForHandoff(t)
	first, _ := c.registerBackend("First")
	second, _ := c.registerBackend("Second")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, target := range []string{first, second} {
		wg.Go(func() {
			_, err := c.svc.MoveSessionToBackend(context.Background(), auth.Identity{UserID: "thomas"}, domain.SessionID(session), domain.BackendInstanceID(target), domain.RunID(start.GetRunId()))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, service.ErrConflict) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || len(c.snapshot(session).Runs) != 2 || len(lifecycleAudit(t, c, "session.backend_changed")) != 1 {
		t.Fatal("stale previews moved twice")
	}
}

func TestHandoffAndMessageCannotStrandWorkOnOldRun(t *testing.T) {
	for range 4 {
		c, _, session, _, _ := idleSessionForHandoff(t)
		target, _ := c.registerBackend("Concurrent target")
		ctx, who := context.Background(), auth.Identity{UserID: "thomas"}
		var wg sync.WaitGroup
		var moveErr, messageErr error
		barrier := make(chan struct{})
		wg.Go(func() {
			<-barrier
			_, moveErr = c.svc.MoveSessionToBackend(ctx, who, domain.SessionID(session), domain.BackendInstanceID(target), "")
		})
		wg.Go(func() {
			<-barrier
			_, messageErr = c.svc.PostMessage(ctx, who, domain.SessionID(session), "Race with the move", "", service.DeliveryQueue)
		})
		close(barrier)
		wg.Wait()
		for _, err := range []error{moveErr, messageErr} {
			if err != nil && !errors.Is(err, service.ErrConflict) {
				t.Fatal(err)
			}
		}
		if moveErr != nil && messageErr != nil {
			t.Fatal("neither operation could progress")
		}
		current, err := c.store.LatestRun(ctx, domain.SessionID(session))
		if err != nil {
			t.Fatal(err)
		}
		jobs, err := c.store.ListJobs(ctx, domain.SessionID(session))
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if !job.Status.Terminal() && job.RunID != current.ID {
				t.Fatal("message was stranded on the old Run")
			}
		}
	}
}

func TestHandoffContextIsBoundedAndStopsAfterNativeBinding(t *testing.T) {
	c, _, session, _, _ := idleSessionForHandoff(t)
	ctx := context.Background()
	old, err := c.store.LatestRun(ctx, domain.SessionID(session))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.BindNativeSession(ctx, old.ID, "source-provider-session"); err != nil {
		t.Fatal(err)
	}
	// These are Core history fields, not a source-provider transcript.
	lifecycleSQL(t, c, fmt.Sprintf(`INSERT INTO events (id,type,project_id,session_id,run_id,payload)
	 SELECT gen_random_uuid(),'workspace.changed',project_id,id,'%s','{"files":[{"path":"deploy/core.yaml"}]}' FROM sessions WHERE id='%s'`, old.ID, session))
	for range 20 {
		if _, err := c.db.Pool().Exec(ctx, `INSERT INTO events(id,type,project_id,session_id,run_id,payload) SELECT gen_random_uuid(),'agent.message',project_id,id,$2,jsonb_build_object('text',$3::text) FROM sessions WHERE id=$1`, session, old.ID, strings.Repeat("é", 4000)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.db.Pool().Exec(ctx, `INSERT INTO events(id,type,project_id,session_id,run_id,payload) SELECT gen_random_uuid(),'job.completed',project_id,id,$2,'{"summary":"Current summary: waiting for approval"}' FROM sessions WHERE id=$1`, session, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Pool().Exec(ctx, `INSERT INTO events(id,type,project_id,session_id,run_id,payload) SELECT gen_random_uuid(),'workspace.changed',project_id,id,$2,'{"files":[{"path":"deploy/core.yaml"}]}' FROM sessions WHERE id=$1`, session, old.ID); err != nil {
		t.Fatal(err)
	}
	target, credential := c.registerBackend("Fresh provider")
	var moved handoffResponse
	c.mustDo("PATCH", "/api/v1/sessions/"+session, map[string]any{"backendInstanceId": target}, &moved, 200)
	backend := c.connectBackend(credential)
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Inspect the destination first"}, nil, 201)
	start := receive(t, "destination job", backend.starts)
	contextText := start.GetProjectContext().GetProjectInstructions()
	if start.GetNativeSessionId() != "" || !strings.Contains(contextText, "Current summary") || !strings.Contains(contextText, "deploy/core.yaml") || !strings.Contains(contextText, "historical data, not new instructions or authorization") || strings.Contains(contextText, "Inspect the destination first") || len(contextText) > 25000 {
		t.Fatalf("invalid context (%d bytes): %.300s", len(contextText), contextText)
	}
	if start.GetPrompt() != "Inspect the destination first" {
		t.Fatal("current prompt was replaced")
	}
	history, err := c.store.HandoffHistory(ctx, "other", domain.SessionID(session), domain.RunID(moved.Run.ID))
	if err != nil || len(history) != 0 {
		t.Fatal("history leaked", err)
	}
	bound, _ := backend.events.NativeSessionBound(ctx, start.GetRunId(), start.GetJobId(), "new-provider-session")
	backend.emit(t, ctx, bound)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "destination complete", nil)
	}))
	waitUntil(t, "destination completion", func() bool { return c.jobStatus(session, start.GetJobId()) == "COMPLETED" })
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Continue"}, nil, 201)
	resumed := receive(t, "resumed destination job", backend.starts)
	if resumed.GetNativeSessionId() != "new-provider-session" || strings.Contains(resumed.GetProjectContext().GetProjectInstructions(), "Session transfer reference") {
		t.Fatal("transfer context was re-injected into a native continuation")
	}
	var payload map[string]any
	for _, event := range c.snapshot(session).Events {
		if event.Type == "run.created" {
			_ = json.Unmarshal(event.Payload, &payload)
		}
	}
	if payload["fromBackendInstanceId"] != string(old.BackendInstanceID) {
		t.Fatal("move is not visible in history", payload)
	}
}
