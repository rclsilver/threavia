package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestAgentPromotesProjectDecisionAtomically(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.startSession(project, c.backendID, dir, "Promote memory")
	start := receive(t, "job", backend.starts)
	ctx := context.Background()
	local := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Git SSH agent policy", "importance": "IMPORTANT"})
	global := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Git SSH agent policy", "importance": "IMPORTANT", "scope": "global", "supersedes": local["decisionId"]})
	if global["scope"] != "global" || len(c.activeProjectDecisions(project)) != 0 {
		t.Fatalf("promotion left an active project copy: %v", global)
	}
	if got := c.globalDecisions(false); len(got) != 1 || string(got[0].ID) != global["decisionId"] || got[0].Supersedes == nil || string(*got[0].Supersedes) != local["decisionId"] {
		t.Fatalf("global memory: %v", got)
	}
	err := backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Duplicate promotion", "scope": "global", "supersedes": local["decisionId"]})
	if !strings.Contains(err.Error(), "FailedPrecondition") {
		t.Fatalf("conflict code: %v", err)
	}
	if len(c.globalDecisions(false)) != 1 {
		t.Fatal("failed promotion left a new decision")
	}
	search := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "memory_search", map[string]any{"query": "SSH"})
	if len(search["decisions"].([]any)) != 1 {
		t.Fatalf("duplicate active memory: %v", search)
	}
	// Deleting the promoted fact must not silently reintroduce the local copy.
	c.mustDo("DELETE", "/api/v1/decisions/"+global["decisionId"].(string), nil, nil, 204)
	if len(c.activeProjectDecisions(project)) != 0 || len(c.globalDecisions(false)) != 0 {
		t.Fatal("deletion revived promoted memory")
	}
}

func TestAgentCannotDemoteGlobalOrPromoteAnotherProjectsDecision(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.startSession(project, c.backendID, dir, "Scoped memory")
	start := receive(t, "job", backend.starts)
	global := backend.callCoreTool(t, context.Background(), start.GetRunId(), start.GetJobId(), "decision_create", map[string]any{"title": "Global fact", "scope": "global"})
	otherProject := c.createProject("Other project")
	var local domain.Decision
	c.mustDo("POST", "/api/v1/projects/"+otherProject+"/decisions", map[string]any{"title": "Other project fact"}, &local, 201)
	for _, input := range []map[string]any{
		{"title": "Demote", "supersedes": global["decisionId"]},
		{"title": "Promote foreign project", "scope": "global", "supersedes": local.ID},
		{"title": "Bad scope", "scope": "all"},
	} {
		err := backend.callCoreToolExpectingFailure(t, context.Background(), start.GetRunId(), start.GetJobId(), "decision_create", input)
		if !strings.Contains(err.Error(), "InvalidArgument") {
			t.Fatalf("invalid request code: %v", err)
		}
	}
	if len(c.globalDecisions(false)) != 1 || len(c.decisions(otherProject)) != 1 {
		t.Fatal("refusal changed memory")
	}
}
