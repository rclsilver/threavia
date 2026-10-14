package api_test

import (
	"context"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestGlobalDirectoriesAreSharedAcrossOwnedProjects(t *testing.T) {
	c, backend, projectID, _ := setup(t)
	var global domain.KnownDirectory
	c.mustDo("POST", "/api/v1/directories", map[string]any{"name": "shared", "description": "Notes", "gitRemote": "https://example.org/notes.git"}, &global, 201)
	if global.ProjectID != "" {
		t.Fatalf("global has a project: %+v", global)
	}
	c.mustDo("POST", "/api/v1/directories", map[string]any{"name": "shared"}, nil, 409)
	var other idOnly
	c.mustDo("POST", "/api/v1/projects", map[string]any{"name": "Other"}, &other, 201)
	for _, id := range []string{projectID, other.ID} {
		var dirs struct {
			Items []domain.KnownDirectory `json:"items"`
		}
		c.mustDo("GET", "/api/v1/projects/"+id+"/directories", nil, &dirs, 200)
		found := false
		for _, dir := range dirs.Items {
			if dir.ID == global.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("global absent from project %s: %+v", id, dirs)
		}
	}
	id := string(global.ID)
	c.mustDo("POST", "/api/v1/directories/"+id+"/bindings", map[string]any{"backendInstanceId": c.backendID, "path": "/work/shared"}, nil, 200)
	c.startSession(projectID, c.backendID, id, "Read notes")
	start := receive(t, "job", backend.starts)
	c.startSession(other.ID, c.backendID, id, "Read shared notes in another project")
	c.mustDo("DELETE", "/api/v1/directories/"+id, nil, nil, 409)
	listed := backend.callCoreTool(t, context.Background(), start.GetRunId(), start.GetJobId(), "known_directory_list", map[string]any{"scope": "global"})
	if len(listed["directories"].([]any)) != 1 {
		t.Fatalf("global tool list: %v", listed)
	}
	backend.callCoreToolExpectingFailure(t, context.Background(), start.GetRunId(), start.GetJobId(), "known_directory_register", map[string]any{"name": "bad-scope", "path": "/work/bad", "scope": "unknown"})
	registered := backend.callCoreTool(t, context.Background(), start.GetRunId(), start.GetJobId(), "known_directory_register", map[string]any{"name": "tool-global", "path": "/work/tool-global", "scope": "global"})
	if registered["scope"] != "global" {
		t.Fatalf("global registration: %v", registered)
	}
	backend.callCoreTool(t, context.Background(), start.GetRunId(), start.GetJobId(), "known_directory_delete", map[string]any{"knownDirectoryId": registered["knownDirectoryId"]})
	backend.callCoreTool(t, context.Background(), start.GetRunId(), start.GetJobId(), "known_directory_update", map[string]any{"knownDirectoryId": id, "description": "", "gitRemote": ""})
	global = domain.KnownDirectory{}
	c.mustDo("PATCH", "/api/v1/directories/"+id, map[string]any{"name": "shared-notes"}, &global, 200)
	if global.Description != "" || global.GitRemote != nil {
		t.Fatalf("optional metadata not cleared: %+v", global)
	}
	stranger := c.asUser("stranger")
	stranger.mustDo("PATCH", "/api/v1/directories/"+id, map[string]any{"name": "stolen"}, nil, 404)
	stranger.mustDo("DELETE", "/api/v1/directories/"+id, nil, nil, 404)
	stranger.mustDo("GET", "/api/v1/directories/"+id+"/bindings", nil, nil, 404)
	stranger.mustDo("DELETE", "/api/v1/directories/"+id+"/bindings/"+c.backendID, nil, nil, 404)
	stranger.mustDo("GET", "/api/v1/projects/"+projectID+"/directories", nil, nil, 404)
}

func TestDeletingProjectPreservesGlobalDirectoriesAndBindings(t *testing.T) {
	c, _, projectID, _ := setup(t)
	var global domain.KnownDirectory
	c.mustDo("POST", "/api/v1/directories", map[string]any{"name": "shared"}, &global, 201)
	id := string(global.ID)
	c.mustDo("POST", "/api/v1/directories/"+id+"/bindings", map[string]any{"backendInstanceId": c.backendID, "path": "/work/shared"}, nil, 200)
	c.mustDo("DELETE", "/api/v1/projects/"+projectID, nil, nil, 204)
	var bindings struct {
		Items []domain.KnownDirectoryBinding `json:"items"`
	}
	c.mustDo("GET", "/api/v1/directories/"+id+"/bindings", nil, &bindings, 200)
	if len(bindings.Items) != 1 {
		t.Fatalf("global bindings removed: %v", bindings)
	}
	c.mustDo("DELETE", "/api/v1/directories/"+id+"/bindings/"+c.backendID, nil, nil, 204)
	c.mustDo("DELETE", "/api/v1/directories/"+id+"/bindings/"+c.backendID, nil, nil, 204)
	c.mustDo("DELETE", "/api/v1/directories/"+id, nil, nil, 204)
}
