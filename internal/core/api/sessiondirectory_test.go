package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func TestAgentRegistersDirectoryAndAttachesItsSessionForTheNextJob(t *testing.T) {
	t.Parallel()
	c, backend, projectID, original := setup(t)
	sessionID := c.startSession(projectID, c.backendID, original, "Remember the docs directory and work there")
	active := receive(t, "first job", backend.starts)
	initial := active.GetProjectContext().GetWorkingDirectoryPath()
	other := c.startSession(projectID, c.backendID, original, "Other session")
	receive(t, "other job", backend.starts)
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, active.GetRunId(), active.GetJobId(), name, input)
	}
	snapshot := func(id string) service.Snapshot {
		var result service.Snapshot
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+id, nil, &result, http.StatusOK)
		return result
	}
	call("known_directory_register", map[string]any{"name": "reference", "path": "/work/reference"})
	if got := snapshot(sessionID); got.Session.WorkingDirectoryID == nil || *got.Session.WorkingDirectoryID != original {
		t.Fatal("plain registration changed the session")
	}

	attached := call("known_directory_register", map[string]any{"name": "docs", "path": "/work/docs", "scope": "global", "useForSession": true})
	id := attached["knownDirectoryId"].(string)
	if attached["sessionId"] != sessionID || attached["effectiveFrom"] != "next_job" || attached["path"] != "/work/docs" {
		t.Fatalf("attachment result: %v", attached)
	}
	got := snapshot(sessionID)
	if got.Session.WorkingDirectoryID == nil || *got.Session.WorkingDirectoryID != id {
		t.Fatal("attachment was not persisted")
	}
	if got := snapshot(other); got.Session.WorkingDirectoryID == nil || *got.Session.WorkingDirectoryID != original {
		t.Fatal("another session's directory changed")
	}
	if active.GetProjectContext().GetWorkingDirectoryPath() != initial {
		t.Fatal("the running job's context was changed")
	}
	dir, err := c.store.GetKnownDirectory(ctx, domain.UserID("thomas"), domain.KnownDirectoryID(id))
	if err != nil {
		t.Fatal(err)
	}
	if dir.ProjectID != "" {
		t.Fatal("global registration lost its scope")
	}
	events := 0
	for _, event := range got.Events {
		if event.Type != "working_directory.changed" {
			continue
		}
		var payload struct {
			KnownDirectoryID string `json:"knownDirectoryId"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.KnownDirectoryID != id {
			t.Fatalf("wrong directory event: %s", event.Payload)
		}
		events++
	}
	if events != 1 {
		t.Fatalf("got %d directory events, want 1", events)
	}
	audit, err := c.store.ListAudit(ctx, "thomas", domain.ProjectID(projectID), 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if entry.Action == "session.working_directory_changed" && entry.SessionID == sessionID {
			if entry.JobID != active.GetJobId() {
				t.Fatal("audit lost the calling job")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("attachment audit absent")
	}

	c.mustDo(http.MethodPost, "/api/v1/sessions/"+sessionID+"/messages", map[string]any{"message": "Continue there"}, nil, http.StatusCreated)
	expectNothing(t, "another launch while running", backend.starts)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, active.GetRunId(), active.GetJobId(), "ready", nil)
	}))
	active = receive(t, "next job", backend.starts)
	if active.GetProjectContext().GetKnownDirectoryId() != id || active.GetProjectContext().GetWorkingDirectoryPath() != "/work/docs" {
		t.Fatalf("next job did not start in the remembered directory: %v", active.GetProjectContext())
	}
	changed := call("working_directory_set", map[string]any{"knownDirectoryId": original})
	if changed["knownDirectoryId"] != original || changed["effectiveFrom"] != "next_job" {
		t.Fatalf("existing directory result: %v", changed)
	}
	if got := snapshot(sessionID); got.Session.WorkingDirectoryID == nil || *got.Session.WorkingDirectoryID != original {
		t.Fatal("existing directory not attached")
	}
	call("working_directory_set", map[string]any{"knownDirectoryId": original})
	if count := c.countEvents(sessionID, "working_directory.changed"); count != 2 {
		t.Fatalf("repeating the same attachment emitted another event: %d", count)
	}

	var foreign domain.KnownDirectory
	otherProject := c.createProject("foreign")
	c.mustDo(http.MethodPost, "/api/v1/projects/"+otherProject+"/directories", map[string]any{"name": "foreign"}, &foreign, http.StatusCreated)
	backend.callCoreToolExpectingFailure(t, ctx, active.GetRunId(), active.GetJobId(), "working_directory_set", map[string]any{"knownDirectoryId": string(foreign.ID)})
}

func TestDirectoryRegistrationRollsBackWhenSessionAttachmentFails(t *testing.T) {
	t.Parallel()
	c, backend, projectID, original := setup(t)
	sessionID := c.startSession(projectID, c.backendID, original, "Attach")
	active := receive(t, "job", backend.starts)
	ctx := context.Background()
	// A real database failure after metadata and binding inserts must roll back
	// both, rather than leave a directory whose session attachment never landed.
	_, err := c.db.Pool().Exec(ctx, `CREATE FUNCTION refuse_test_directory() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'test attachment failure'; END $$;
 CREATE TRIGGER refuse_test_directory BEFORE UPDATE OF working_directory_id ON sessions FOR EACH ROW EXECUTE FUNCTION refuse_test_directory();`)
	if err != nil {
		t.Fatal(err)
	}
	backend.callCoreToolExpectingFailure(t, ctx, active.GetRunId(), active.GetJobId(), "known_directory_register", map[string]any{"name": "rolled-back", "path": "/work/rolled-back", "useForSession": true})
	dirs, err := c.store.ListKnownDirectories(ctx, "thomas", domain.ProjectID(projectID))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("partial directory registration remained: %v", dirs)
	}
	var bindings int
	if err = c.db.Pool().QueryRow(ctx, "SELECT count(*) FROM known_directory_bindings").Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 1 {
		t.Fatal("a partial backend binding remained")
	}
	session, err := c.store.GetSession(ctx, "thomas", domain.SessionID(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkingDirectoryID == nil || *session.WorkingDirectoryID != original {
		t.Fatal("failed attachment changed session")
	}
	if c.countEvents(sessionID, "working_directory.changed") != 0 {
		t.Fatal("failed transaction published a directory change")
	}
}

func TestWorkingDirectoryPatchDistinguishesOmissionFromNull(t *testing.T) {
	t.Parallel()
	c, backend, projectID, original := setup(t)
	id := c.startSession(projectID, c.backendID, original, "Keep the directory")
	receive(t, "job", backend.starts)
	path := "/api/v1/sessions/" + id
	var session domain.Session
	c.mustDo(http.MethodPatch, path, map[string]any{"title": "Renamed"}, &session, http.StatusOK)
	if session.WorkingDirectoryID == nil || *session.WorkingDirectoryID != original {
		t.Fatal("omitting workingDirectoryId cleared it")
	}
	c.mustDo(http.MethodPatch, path, map[string]any{"workingDirectoryId": nil}, nil, http.StatusOK)
	stored, err := c.store.GetSession(context.Background(), "thomas", domain.SessionID(id))
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorkingDirectoryID != nil {
		t.Fatal("null did not clear directory")
	}
	c.mustDo(http.MethodPatch, path, map[string]any{"workingDirectoryId": original}, nil, http.StatusOK)
	c.mustDo(http.MethodPatch, path, map[string]any{"workingDirectoryId": 42}, nil, http.StatusBadRequest)
}
