package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
)

func TestProjectRenameAndDeleteSynchronizeClients(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	stranger := c.asUser("stranger")
	path := "/api/v1/projects/" + projectID
	stranger.mustDo(http.MethodPatch, path, map[string]any{"name": "stolen"}, nil, http.StatusNotFound)
	stranger.mustDo(http.MethodDelete, path, nil, nil, http.StatusNotFound)
	var renamed domain.Project
	c.mustDo(http.MethodPatch, path, map[string]any{"name": "  renamed project  "}, &renamed, http.StatusOK)
	if renamed.Name != "renamed project" || renamed.Description != "Homelab" {
		t.Fatalf("rename changed unrelated metadata: %+v", renamed)
	}
	replay, err := c.store.EventsAfter(context.Background(), "thomas", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, event := range replay {
		if event.Type == events.TypeProjectUpdated {
			seen = true
		}
	}
	if !seen {
		t.Fatal("rename notification missing")
	}
	sessionID := c.startSession(projectID, c.backendID, dirID, "Keep this work alive")
	_ = receive(t, "the job start", backend.starts)
	c.mustDo(http.MethodDelete, path, nil, nil, http.StatusConflict)
	snapshot := c.snapshot(sessionID)
	job := snapshot.Jobs[0]
	current, err := c.store.GetJob(context.Background(), "thomas", domain.JobID(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.TransitionJob(context.Background(), current.ID, current.Status, domain.JobCancelled, nil); err != nil {
		t.Fatal(err)
	}
	c.mustDo(http.MethodDelete, path, nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+sessionID, nil, nil, http.StatusNotFound)
	c.mustDo(http.MethodGet, "/api/v1/backends/"+c.backendID, nil, nil, http.StatusOK)
	replay, err = c.store.EventsAfter(context.Background(), "thomas", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen = false
	for _, event := range replay {
		if event.Type == events.TypeProjectDeleted && event.ProjectID == nil {
			seen = true
		}
	}
	if !seen {
		t.Fatal("deletion notification did not survive the deleted project")
	}
	foreign, err := c.store.EventsAfter(context.Background(), "stranger", 0, 100)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("project notifications leaked: %v (%v)", foreign, err)
	}
}
