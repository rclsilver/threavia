package api_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// TestMovingASessionTakesItsHistoryAndWhatItsAgentsCreated re-files a Session
// under another Project: its history, requests and files go with it, its
// directory of the old Project is detached, and the tasks and decisions its
// agent created follow, except those linked to one that stays behind.
func TestMovingASessionTakesItsHistoryAndWhatItsAgentsCreated(t *testing.T) {
	c, backend, from, dir := setup(t)
	to := c.createProject("Other")
	session := c.startSession(from, c.backendID, dir, "Range le projet")
	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()

	// What a person recorded belongs to no Session, and stays.
	var personTask, personDecision idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects/"+from+"/tasks", map[string]any{"title": "Person task"}, &personTask, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/projects/"+from+"/decisions", map[string]any{"title": "Person decision"}, &personDecision, http.StatusCreated)

	tool := func(name string, input map[string]any) string {
		result := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
		for _, key := range []string{"taskId", "decisionId"} {
			if id, ok := result[key].(string); ok {
				return id
			}
		}
		t.Fatalf("%s answered %v", name, result)
		return ""
	}
	free := tool("task_create", map[string]any{"title": "Free task"})
	first := tool("task_create", map[string]any{"title": "First"})
	second := tool("task_create", map[string]any{"title": "Second", "dependsOn": []any{first}})
	linked := tool("task_create", map[string]any{"title": "Linked", "dependsOn": []any{personTask.ID}})
	decision := tool("decision_create", map[string]any{"title": "Agent decision", "content": "x", "importance": "NORMAL"})
	replacing := tool("decision_create", map[string]any{"title": "Replacing", "content": "x", "importance": "NORMAL", "supersedes": personDecision.ID})

	// A question, answered, so the Session has a request of its own.
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(), &backendv1.UserInputRequested{RequestId: "ask-1", Prompt: "Lequel ?"})
	}))
	waitUntil(t, "the job to wait for input", func() bool { return c.jobStatus(session, start.GetJobId()) == "WAITING_INPUT" })
	var pending snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &pending, http.StatusOK)
	c.mustDo(http.MethodPost, "/api/v1/user-input/"+pending.Attention.UserInputs[0].ID+"/resolve", map[string]any{"value": "celui-ci"}, nil, http.StatusOK)

	path := "/api/v1/sessions/" + session + "/move"
	c.mustDo(http.MethodPost, path, map[string]any{"projectId": to}, nil, http.StatusConflict)
	c.mustDo(http.MethodPost, path, map[string]any{"projectId": "Other"}, nil, http.StatusBadRequest)

	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "fait", nil)
	}))
	waitUntil(t, "the job to complete", func() bool { return c.jobStatus(session, start.GetJobId()) == "COMPLETED" })

	var moved struct {
		Session struct {
			ProjectID          string  `json:"projectId"`
			WorkingDirectoryID *string `json:"workingDirectoryId"`
		} `json:"session"`
		Knowledge struct {
			MovedTasks     []string `json:"movedTasks"`
			LeftTasks      []string `json:"leftTasks"`
			MovedDecisions []string `json:"movedDecisions"`
			LeftDecisions  []string `json:"leftDecisions"`
		} `json:"knowledge"`
	}
	c.mustDo(http.MethodPost, path, map[string]any{"projectId": to}, &moved, http.StatusOK)
	if moved.Session.ProjectID != to || moved.Session.WorkingDirectoryID != nil {
		t.Fatalf("moved session = %+v, want it in %s without the old project's directory", moved.Session, to)
	}
	same := func(got []string, want ...string) bool {
		slices.Sort(got)
		slices.Sort(want)
		return slices.Equal(got, want)
	}
	k := moved.Knowledge
	if !same(k.MovedTasks, free, first, second) || !same(k.LeftTasks, linked) {
		t.Fatalf("tasks moved %v and left %v, want the linked one left behind", k.MovedTasks, k.LeftTasks)
	}
	if !same(k.MovedDecisions, decision) || !same(k.LeftDecisions, replacing) {
		t.Fatalf("decisions moved %v and left %v, want the replacing one left behind", k.MovedDecisions, k.LeftDecisions)
	}

	count := func(query string, args ...any) int {
		var n int
		if err := c.db.Pool().QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM tasks WHERE project_id = $1`, to); n != 3 {
		t.Fatalf("%d tasks in the destination, want 3", n)
	}
	if n := count(`SELECT count(*) FROM events WHERE session_id = $1 AND project_id <> $2`, session, to); n != 0 {
		t.Fatalf("%d events of the session left in the old project", n)
	}
	if n := count(`SELECT count(*) FROM events WHERE session_id = $1 AND type = 'session.project_moved'`, session); n != 1 {
		t.Fatalf("%d session.project_moved events, want 1", n)
	}

	// Deleting the Project it left takes nothing of it along.
	c.mustDo(http.MethodDelete, "/api/v1/projects/"+from, nil, nil, http.StatusNoContent)
	if n := count(`SELECT count(*) FROM user_input_requests WHERE session_id = $1`, session); n != 1 {
		t.Fatalf("%d input requests left after deleting the old project, want 1", n)
	}
	var after snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &after, http.StatusOK)
	if len(after.Events) == 0 {
		t.Fatal("the moved session lost its history with the old project")
	}

	// Moving where it already is changes nothing; an unknown Project is not found.
	c.mustDo(http.MethodPost, path, map[string]any{"projectId": to}, nil, http.StatusOK)
	c.mustDo(http.MethodPost, path, map[string]any{"projectId": "00000000-0000-0000-0000-000000000000"}, nil, http.StatusNotFound)
}
