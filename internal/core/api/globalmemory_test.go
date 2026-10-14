package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func (c *core) globalDecisions(includeSuperseded bool) []domain.Decision {
	c.t.Helper()
	var listed struct {
		Items []domain.Decision `json:"items"`
	}
	path := "/api/v1/me/decisions"
	if includeSuperseded {
		path += "?includeSuperseded=true"
	}
	c.mustDo("GET", path, nil, &listed, 200)
	return listed.Items
}

func TestGlobalMemorySurvivesProjectsAndKeepsItsReplacementHistory(t *testing.T) {
	c := newCore(t)
	project := c.createProject("Temporary project")
	var original, replacement domain.Decision
	c.mustDo("POST", "/api/v1/me/decisions", map[string]any{"title": "Postgres backups", "content": "Test restores every month", "importance": "IMPORTANT"}, &original, 201)
	if original.ProjectID != "" {
		t.Fatalf("global decision has a project: %+v", original)
	}
	c.mustDo("POST", "/api/v1/projects/"+project+"/decisions", map[string]any{"title": "Postgres project detail"}, nil, 201)
	if got := c.globalDecisions(false); len(got) != 1 || got[0].ID != original.ID {
		t.Fatalf("global memory = %+v", got)
	}
	if got := c.decisions(project); len(got) != 1 || got[0].Title != "Postgres project detail" {
		t.Fatalf("project memory = %+v", got)
	}

	c.mustDo("POST", "/api/v1/me/decisions", map[string]any{"title": "Postgres restore weekly", "supersedes": original.ID}, &replacement, 201)
	if got := c.globalDecisions(false); len(got) != 1 || got[0].ID != replacement.ID {
		t.Fatalf("active global memory = %+v", got)
	}
	if got := c.globalDecisions(true); len(got) != 2 {
		t.Fatalf("global history = %+v", got)
	}
	var matches struct {
		Items []domain.Decision `json:"items"`
	}
	c.mustDo("GET", "/api/v1/me/memory/search?q=Postgres", nil, &matches, 200)
	if len(matches.Items) != 1 || matches.Items[0].ID != replacement.ID {
		t.Fatalf("search must exclude replaced and project decisions: %+v", matches)
	}
	c.mustDo("GET", "/api/v1/me/memory/search?q=", nil, nil, 400)

	c.mustDo("DELETE", "/api/v1/decisions/"+string(replacement.ID), nil, nil, 204)
	if got := c.globalDecisions(false); len(got) != 1 || got[0].ID != original.ID || got[0].Status != domain.DecisionActive {
		t.Fatalf("original was not revived: %+v", got)
	}
	c.mustDo("PATCH", "/api/v1/decisions/"+string(original.ID), map[string]any{"importance": "NORMAL"}, nil, 200)
	c.mustDo("DELETE", "/api/v1/projects/"+project, nil, nil, 204)
	if got := c.globalDecisions(false); len(got) != 1 || got[0].Importance != domain.DecisionNormal {
		t.Fatalf("deleting project lost global memory: %+v", got)
	}
}

func TestGlobalMemoryIsOwnerScopedAndCannotBeDemoted(t *testing.T) {
	c := newCore(t)
	other := c.asUser("another-owner")
	project := c.createProject("Project")
	var global, local domain.Decision
	c.mustDo("POST", "/api/v1/me/decisions", map[string]any{"title": "Private global preference"}, &global, 201)
	c.mustDo("POST", "/api/v1/projects/"+project+"/decisions", map[string]any{"title": "Local fact"}, &local, 201)
	if got := other.globalDecisions(false); len(got) != 0 {
		t.Fatalf("another owner sees global memory: %+v", got)
	}
	other.mustDo("PATCH", "/api/v1/decisions/"+string(global.ID), map[string]any{"importance": "IMPORTANT"}, nil, 404)
	other.mustDo("DELETE", "/api/v1/decisions/"+string(global.ID), nil, nil, 404)
	other.mustDo("POST", "/api/v1/me/decisions", map[string]any{"title": "Replace foreign memory", "supersedes": global.ID}, nil, 400)
	other.mustDo("POST", "/api/v1/me/decisions", map[string]any{"title": "Replace foreign project memory", "supersedes": local.ID}, nil, 400)
	c.mustDo("POST", "/api/v1/projects/"+project+"/decisions", map[string]any{"title": "Replace global from local", "supersedes": global.ID}, nil, 400)
	var found struct {
		Items []domain.Decision `json:"items"`
	}
	other.mustDo("GET", "/api/v1/me/memory/search?q=Private", nil, &found, 200)
	if len(found.Items) != 0 {
		t.Fatalf("search leaked another owner's memory: %+v", found)
	}
	if len(c.globalDecisions(false)) != 1 || len(other.globalDecisions(false)) != 0 {
		t.Fatal("rejected replacements changed memory")
	}
}

func TestAgentsShareGlobalFactsAndSearchThemWithoutSharingProjectHistory(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.startSession(project, c.backendID, dir, "Remember shared facts")
	start := receive(t, "first job", backend.starts)
	ctx := context.Background()
	created := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{
		"title": "Postgres restore policy", "content": "Restore backups monthly", "importance": "IMPORTANT", "scope": "global",
	})
	if created["scope"] != "global" {
		t.Fatalf("global create result: %v", created)
	}
	backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Postgres shared notes", "scope": "global"})
	backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Postgres local notes"})
	otherProject := c.createProject("Other project")
	c.mustDo("POST", "/api/v1/projects/"+otherProject+"/decisions", map[string]any{"title": "Postgres other project", "importance": "IMPORTANT"}, nil, 201)
	search := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "memory_search", map[string]any{"query": "Postgres"})
	if got := search["decisions"].([]any); len(got) != 3 {
		t.Fatalf("search must include project and globals only: %v", got)
	}
	search = backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "memory_search", map[string]any{"query": "Postgres", "scope": "global"})
	if got := search["decisions"].([]any); len(got) != 2 {
		t.Fatalf("global search: %v", got)
	}
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "memory_search", map[string]any{"query": "Postgres", "scope": "all"})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Invalid scope", "scope": "all"})

	c.startSession(otherProject, c.backendID, "", "Read shared facts")
	next := receive(t, "job in another project", backend.starts)
	decisions := next.GetProjectContext().GetDecisions()
	if len(decisions) != 2 {
		t.Fatalf("context must have one important global and one local: %v", decisions)
	}
	globalFound := false
	for _, entry := range decisions {
		if entry.GetId() == created["decisionId"] {
			globalFound = strings.HasPrefix(entry.GetTitle(), "[Core memory] ") && entry.GetContent() == "Restore backups monthly"
		}
	}
	if !globalFound {
		t.Fatalf("global fact absent or lacks scope: %v", decisions)
	}

	c.mustDo("PATCH", "/api/v1/decisions/"+created["decisionId"].(string), map[string]any{"importance": "NORMAL"}, nil, 200)
	c.startSession(otherProject, c.backendID, "", "Read current memory")
	unpinned := receive(t, "job after unpin", backend.starts)
	if got := unpinned.GetProjectContext().GetDecisions(); len(got) != 1 || got[0].GetTitle() != "Postgres other project" {
		t.Fatalf("unpinned global fact still injected: %v", got)
	}
}

func TestGlobalFactsKeepASeparateContextBudget(t *testing.T) {
	c, backend, project, dir := setup(t)
	for n := 0; n < 25; n++ {
		c.mustDo("POST", "/api/v1/me/decisions", map[string]any{"title": fmt.Sprintf("Global fact %d", n), "importance": "IMPORTANT"}, nil, 201)
	}
	c.mustDo("POST", "/api/v1/projects/"+project+"/decisions", map[string]any{"title": "Essential project fact", "importance": "IMPORTANT"}, nil, 201)
	c.startSession(project, c.backendID, dir, "Use both memories")
	start := receive(t, "job", backend.starts)
	decisions := start.GetProjectContext().GetDecisions()
	if len(decisions) != 21 {
		t.Fatalf("context has %d facts, want 20 global plus the project fact", len(decisions))
	}
	for _, entry := range decisions {
		if entry.GetTitle() == "Essential project fact" {
			return
		}
	}
	t.Fatal("global memory displaced project memory")
}
