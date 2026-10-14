package api_test

import (
	"context"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func (c *core) activeProjectDecisions(projectID string) []domain.Decision {
	c.t.Helper()
	var listed struct{ Items []domain.Decision }
	c.mustDo("GET", "/api/v1/projects/"+projectID+"/decisions", nil, &listed, 200)
	return listed.Items
}

func TestMakeDecisionGlobalPreservesFactAndHistory(t *testing.T) {
	c, _, project, _ := setup(t)
	var local, global domain.Decision
	c.mustDo("POST", "/api/v1/projects/"+project+"/decisions", map[string]any{"title": "Shared policy", "content": "Keep the reasoning", "importance": "IMPORTANT"}, &local, 201)
	url := "/api/v1/decisions/" + string(local.ID) + "/global"
	c.asUser("stranger").mustDo("POST", url, nil, nil, 404)
	c.mustDo("POST", url, nil, &global, 200)
	if global.ProjectID != "" || global.Title != local.Title || global.Content != local.Content || global.Importance != local.Importance || global.Supersedes == nil || *global.Supersedes != local.ID {
		t.Fatalf("promotion changed the fact: %+v", global)
	}
	if len(c.activeProjectDecisions(project)) != 0 || len(c.globalDecisions(false)) != 1 {
		t.Fatal("promotion duplicated active memory")
	}
	var history struct{ Items []domain.Decision }
	c.mustDo("GET", "/api/v1/projects/"+project+"/decisions?includeSuperseded=true", nil, &history, 200)
	if len(history.Items) != 1 || history.Items[0].Status != domain.DecisionSuperseded {
		t.Fatalf("project history lost: %+v", history)
	}
	c.mustDo("POST", url, nil, nil, 409)
	var again domain.Decision
	c.mustDo("POST", "/api/v1/decisions/"+string(global.ID)+"/global", nil, &again, 200)
	if again.ID != global.ID || len(c.globalDecisions(false)) != 1 {
		t.Fatal("already-global decision was duplicated")
	}
	c.mustDo("DELETE", "/api/v1/decisions/"+string(global.ID), nil, nil, 204)
	if len(c.activeProjectDecisions(project)) != 0 {
		t.Fatal("deleting the global fact revived its historical project copy")
	}
}

func TestMakeDirectoryGlobalPreservesBindingsAndSessions(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.startSession(project, c.backendID, dir, "Share directory")
	start := receive(t, "job", backend.starts)
	var before, after struct {
		Items []domain.KnownDirectoryBinding
	}
	c.mustDo("GET", "/api/v1/directories/"+dir+"/bindings", nil, &before, 200)
	url := "/api/v1/directories/" + dir + "/global"
	c.asUser("stranger").mustDo("POST", url, nil, nil, 404)
	result := backend.callCoreTool(t, context.Background(), start.GetRunId(), start.GetJobId(), "known_directory_make_global", map[string]any{"knownDirectoryId": dir})
	if result["knownDirectoryId"] != dir || result["scope"] != "global" {
		t.Fatalf("directory promotion: %v", result)
	}
	var global domain.KnownDirectory
	c.mustDo("POST", url, nil, &global, 200)
	if string(global.ID) != dir || global.ProjectID != "" {
		t.Fatalf("directory identity changed: %+v", global)
	}
	c.mustDo("GET", "/api/v1/directories/"+dir+"/bindings", nil, &after, 200)
	if len(before.Items) == 0 || len(after.Items) != len(before.Items) || before.Items[0].Path != after.Items[0].Path {
		t.Fatal("promotion changed directory paths")
	}
	other := c.createProject("Other scope")
	c.startSession(other, c.backendID, dir, "Use shared directory")
	c.mustDo("DELETE", "/api/v1/directories/"+dir, nil, nil, 409)
	var colliding domain.KnownDirectory
	c.mustDo("POST", "/api/v1/projects/"+project+"/directories", map[string]any{"name": global.Name}, &colliding, 201)
	c.mustDo("POST", "/api/v1/directories/"+string(colliding.ID)+"/global", nil, nil, 409)
	var directories struct{ Items []domain.KnownDirectory }
	c.mustDo("GET", "/api/v1/projects/"+project+"/directories", nil, &directories, 200)
	for _, d := range directories.Items {
		if d.ID == colliding.ID && d.ProjectID == "" {
			t.Fatal("failed promotion changed directory scope")
		}
	}
}

func TestMakeGlobalToolsRespectCallingProject(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.startSession(project, c.backendID, dir, "Promote a fact")
	start := receive(t, "job", backend.starts)
	ctx := context.Background()
	local := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Reusable fact", "content": "Original", "importance": "IMPORTANT"})
	global := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_make_global", map[string]any{"decisionId": local["decisionId"]})
	if global["scope"] != "global" || len(c.activeProjectDecisions(project)) != 0 {
		t.Fatalf("memory tool did not promote: %v", global)
	}
	other := c.createProject("Other")
	var foreign domain.Decision
	c.mustDo("POST", "/api/v1/projects/"+other+"/decisions", map[string]any{"title": "Foreign fact"}, &foreign, 201)
	var foreignDir domain.KnownDirectory
	c.mustDo("POST", "/api/v1/projects/"+other+"/directories", map[string]any{"name": "Foreign directory"}, &foreignDir, 201)
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "decision_make_global", map[string]any{"decisionId": string(foreign.ID)})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "known_directory_make_global", map[string]any{"knownDirectoryId": string(foreignDir.ID)})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "decision_make_global", map[string]any{})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "known_directory_make_global", map[string]any{})
	if len(c.decisions(other)) != 1 || len(c.globalDecisions(false)) != 1 {
		t.Fatal("refused tools changed memory")
	}
}
