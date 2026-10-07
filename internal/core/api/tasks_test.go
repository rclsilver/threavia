package api_test

import (
	"net/http"
	"testing"
)

// task is what the client reads back from the task endpoints.
type task struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	DependsOn []string `json:"dependsOn"`
}

// TestTaskDependenciesAreEditable covers the graph being something a person can
// change after the fact: work turns out to need other work first, or stops
// needing it. Only creation could say so before.
func TestTaskDependenciesAreEditable(t *testing.T) {
	c := newCore(t)
	projectID := c.createProject("homelab")

	var foundation, walls task
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/tasks",
		map[string]any{"title": "pose the foundation"}, &foundation, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/tasks",
		map[string]any{"title": "raise the walls"}, &walls, http.StatusCreated)

	// Both can be started, since nothing waits on anything yet.
	if got := c.readyTasks(projectID); len(got) != 2 {
		t.Fatalf("%d ready tasks, want both", len(got))
	}

	var blocked task
	c.mustDo(http.MethodPost, "/api/v1/tasks/"+walls.ID+"/dependencies",
		map[string]any{"dependsOn": foundation.ID}, &blocked, http.StatusOK)
	if len(blocked.DependsOn) != 1 || blocked.DependsOn[0] != foundation.ID {
		t.Fatalf("dependsOn = %v, want the foundation", blocked.DependsOn)
	}

	ready := c.readyTasks(projectID)
	if len(ready) != 1 || ready[0].ID != foundation.ID {
		t.Fatalf("ready = %+v, want only the foundation", ready)
	}

	// The graph stays acyclic: nothing on a loop is ever ready, so closing one
	// would block both for good.
	c.mustDo(http.MethodPost, "/api/v1/tasks/"+foundation.ID+"/dependencies",
		map[string]any{"dependsOn": walls.ID}, nil, http.StatusBadRequest)

	var freed task
	c.mustDo(http.MethodDelete, "/api/v1/tasks/"+walls.ID+"/dependencies/"+foundation.ID,
		nil, &freed, http.StatusOK)
	if len(freed.DependsOn) != 0 {
		t.Fatalf("dependsOn = %v, want none left", freed.DependsOn)
	}
	if got := c.readyTasks(projectID); len(got) != 2 {
		t.Fatalf("%d ready tasks after freeing the walls, want both", len(got))
	}

	// Removing an edge that is not there is the state the caller asked for, so
	// a retry from a client that lost the answer is safe.
	c.mustDo(http.MethodDelete, "/api/v1/tasks/"+walls.ID+"/dependencies/"+foundation.ID,
		nil, nil, http.StatusOK)
}

// TestATaskCannotWaitOnAnotherProject keeps the graph inside one Project, where
// the context that explains it lives.
func TestATaskCannotWaitOnAnotherProject(t *testing.T) {
	c := newCore(t)
	here := c.createProject("homelab")
	elsewhere := c.createProject("laptop")

	var mine, theirs task
	c.mustDo(http.MethodPost, "/api/v1/projects/"+here+"/tasks",
		map[string]any{"title": "pose the foundation"}, &mine, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/projects/"+elsewhere+"/tasks",
		map[string]any{"title": "someone else's work"}, &theirs, http.StatusCreated)

	c.mustDo(http.MethodPost, "/api/v1/tasks/"+mine.ID+"/dependencies",
		map[string]any{"dependsOn": theirs.ID}, nil, http.StatusBadRequest)
}

// TestTheCallerIsTold covers the one thing a client cannot work out on its own:
// who Core thinks it is talking to, and whether anybody proved it.
func TestTheCallerIsTold(t *testing.T) {
	c := newCore(t)

	var me struct {
		UserID   string `json:"userId"`
		AuthMode string `json:"authMode"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me", nil, &me, http.StatusOK)

	if me.UserID == "" {
		t.Fatal("no user id, although every row is attributed to one")
	}
	// The harness runs without authentication, and saying so is the point: a
	// client that only read the id would show a configured development identity
	// as a signed-in person.
	if me.AuthMode != "none" {
		t.Fatalf("authMode = %q, want none", me.AuthMode)
	}
}

// TestDeletingATaskTakesItsEdgesWithIt covers filing being cheap: an agent
// files tasks freely, so taking one back has to be cheap too, and an edge
// pointing at work that no longer exists would block its dependents on nothing.
func TestDeletingATaskTakesItsEdgesWithIt(t *testing.T) {
	c := newCore(t)
	projectID := c.createProject("homelab")

	var foundation, walls task
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/tasks",
		map[string]any{"title": "pose the foundation"}, &foundation, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/tasks",
		map[string]any{"title": "raise the walls"}, &walls, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/tasks/"+walls.ID+"/dependencies",
		map[string]any{"dependsOn": foundation.ID}, nil, http.StatusOK)

	c.mustDo(http.MethodDelete, "/api/v1/tasks/"+foundation.ID, nil, nil, http.StatusNoContent)

	var left struct {
		Items []task `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/tasks", nil, &left, http.StatusOK)
	if len(left.Items) != 1 || left.Items[0].ID != walls.ID {
		t.Fatalf("tasks = %+v, want only the walls", left.Items)
	}
	if len(left.Items[0].DependsOn) != 0 {
		t.Fatalf("dependsOn = %v, want the edge gone with the task", left.Items[0].DependsOn)
	}

	// Nothing waits on anything now, so the walls can be started.
	ready := c.readyTasks(projectID)
	if len(ready) != 1 || ready[0].ID != walls.ID {
		t.Fatalf("ready = %+v, want the walls released", ready)
	}

	// A second delete is a 404 rather than a silent success: the caller asked
	// about a task, and there is none.
	c.mustDo(http.MethodDelete, "/api/v1/tasks/"+foundation.ID, nil, nil, http.StatusNotFound)
}
