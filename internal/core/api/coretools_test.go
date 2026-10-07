package api_test

import (
	"context"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"testing"
)

// TestAgentRecordsProjectKnowledge covers what the Core Tools exist for: an
// agent accumulating knowledge that outlives its own session.
func TestAgentRecordsProjectKnowledge(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()

	// The agent records a ruling it wants every later session to know.
	created := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"decision_create", map[string]any{
			"title":      "Deploy with Puppet",
			"content":    "Ansible was tried and rejected: no agent on the nodes.",
			"importance": "IMPORTANT",
		})
	decisionID, _ := created["decisionId"].(string)
	if decisionID == "" {
		t.Fatalf("decision_create returned %v, want an identifier", created)
	}

	// And files work it found but will not do now.
	filed := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"task_create", map[string]any{
			"title":       "Rewire the puppet manifest",
			"description": "roles/foo still points at the old hiera key",
		})
	taskID, _ := filed["taskId"].(string)
	if taskID == "" {
		t.Fatalf("task_create returned %v, want an identifier", filed)
	}

	// Both are immediately visible to a human through the API.
	var decisions struct {
		Items []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Importance string `json:"importance"`
		} `json:"items"`
	}
	c.mustDo("GET", "/api/v1/projects/"+projectID+"/decisions", nil, &decisions, 200)
	if len(decisions.Items) != 1 || decisions.Items[0].ID != decisionID {
		t.Fatalf("decisions = %+v, want the one the agent recorded", decisions.Items)
	}
	if decisions.Items[0].Importance != "IMPORTANT" {
		t.Errorf("importance = %q, want IMPORTANT", decisions.Items[0].Importance)
	}

	// And the knowledge reaches the next Run, which is the point.
	second := c.startSession(projectID, c.backendID, dirID, "Reprends le travail")
	_ = second
	next := receive(t, "the second job", backend.starts)
	context := next.GetProjectContext()

	if len(context.GetDecisions()) != 1 || context.GetDecisions()[0].GetTitle() != "Deploy with Puppet" {
		t.Fatalf("the run context carries %d decisions, want the IMPORTANT one", len(context.GetDecisions()))
	}
	if len(context.GetTasks()) != 1 || context.GetTasks()[0].GetTitle() != "Rewire the puppet manifest" {
		t.Fatalf("the run context carries %d tasks, want the open one", len(context.GetTasks()))
	}
	if len(context.GetTools()) == 0 {
		t.Fatal("the run context must declare the core tools, so no backend hardcodes them")
	}
}

// TestCompletingATaskReportsWhatItUnblocked pins that the dependency graph
// answers the question it exists for, without the agent having to ask again.
func TestCompletingATaskReportsWhatItUnblocked(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()

	first := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"task_create", map[string]any{"title": "pose the foundation"})
	foundation, _ := first["taskId"].(string)

	backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"task_create", map[string]any{
			"title":     "raise the walls",
			"dependsOn": []any{foundation},
		})

	// Only the unblocked task is ready.
	ready := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "task_ready", map[string]any{})
	if tasks, _ := ready["tasks"].([]any); len(tasks) != 1 {
		t.Fatalf("%d ready tasks, want only the foundation", len(tasks))
	}

	// Completing it reports what that released.
	completed := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"task_complete", map[string]any{"taskId": foundation})
	unblocked, _ := completed["unblocked"].([]any)
	if len(unblocked) != 1 {
		t.Fatalf("completing the foundation unblocked %d tasks, want 1", len(unblocked))
	}
	entry, _ := unblocked[0].(map[string]any)
	if entry["title"] != "raise the walls" {
		t.Fatalf("unblocked %v, want the walls", entry)
	}
}

// TestHistorySearchFindsEarlierWork pins the episodic half of project memory:
// what was said in an earlier session is findable, with no vector database.
func TestHistorySearchFindsEarlierWork(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Configure le reverse proxy avec Caddy")
	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()

	waitUntil(t, "the message to be persisted", func() bool {
		return c.countEvents(session, "user.message") == 1
	})

	found := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(),
		"project_history_search", map[string]any{"query": "reverse proxy Caddy"})
	results, _ := found["results"].([]any)
	if len(results) == 0 {
		t.Fatal("the agent must be able to find what was already said in this project")
	}
	hit, _ := results[0].(map[string]any)
	if excerpt, _ := hit["excerpt"].(string); !strings.Contains(strings.ToLower(excerpt), "caddy") {
		t.Fatalf("excerpt = %q, want the matching text", hit["excerpt"])
	}
}

// TestToolCallsCannotLeaveTheirProject pins the boundary: a tool call is scoped
// by the Job that issued it, and nothing in the input can widen that.
func TestToolCallsCannotLeaveTheirProject(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()

	// A second project, with a task of its own.
	var other idOnly
	c.mustDo("POST", "/api/v1/projects", map[string]any{"name": "autre"}, &other, 201)
	var foreign struct {
		ID string `json:"id"`
	}
	c.mustDo("POST", "/api/v1/projects/"+other.ID+"/tasks",
		map[string]any{"title": "secret du voisin"}, &foreign, 201)

	// The agent of the first project cannot touch it, even knowing its id.
	err := backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(),
		"task_complete", map[string]any{"taskId": foreign.ID})
	if !strings.Contains(err.Error(), "another project") {
		t.Fatalf("error = %v, want a refusal naming the project boundary", err)
	}

	// Nor can it invent a tool.
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(),
		"delete_everything", map[string]any{})
}

// TestExecutionPolicyReachesTheBackend pins that a policy set on a Session
// travels with every Job dispatched from it. The enforcement itself is in the
// backend gate; this is the half Core owns.
func TestExecutionPolicyReachesTheBackend(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	first := receive(t, "the first job", backend.starts)

	// The default is the restrained one: ask before acting, refuse to push.
	if first.GetExecutionPolicy().GetMode().String() != "EXECUTION_MODE_INTERACTIVE" {
		t.Errorf("default mode = %s, want INTERACTIVE", first.GetExecutionPolicy().GetMode())
	}
	if first.GetExecutionPolicy().GetAllowGitPush() {
		t.Error("the default policy must not allow pushing")
	}

	c.mustDo("PUT", "/api/v1/sessions/"+session+"/policy", map[string]any{
		"mode":                 "AUTONOMOUS",
		"allowFilesystemWrite": true,
		"allowGitCommit":       true,
		"allowGitPush":         false,
		"allowNetwork":         false,
		"maxActions":           50,
	}, nil, 200)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, first.GetRunId(), first.GetJobId(), "fait")
	}))
	waitUntil(t, "the first job to complete", func() bool {
		return c.jobStatus(session, first.GetJobId()) == "COMPLETED"
	})

	var second struct {
		ID string `json:"id"`
	}
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages",
		map[string]any{"message": "continue"}, &second, 201)

	next := receive(t, "the second job", backend.starts)
	policy := next.GetExecutionPolicy()
	if policy.GetMode().String() != "EXECUTION_MODE_AUTONOMOUS" {
		t.Errorf("mode = %s, want AUTONOMOUS", policy.GetMode())
	}
	if policy.GetAllowGitPush() || policy.GetAllowNetwork() {
		t.Error("the forbidden capabilities must stay forbidden")
	}
	if policy.GetMaxActions() != 50 {
		t.Errorf("maxActions = %d, want 50", policy.GetMaxActions())
	}
}

// TestAutonomousNeedsALimit pins the one combination section 17 exists to
// prevent: an autonomous Run nobody can stop.
func TestAutonomousNeedsALimit(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	receive(t, "the dispatched job", backend.starts)

	c.mustDo("PUT", "/api/v1/sessions/"+session+"/policy", map[string]any{
		"mode": "AUTONOMOUS", "allowFilesystemWrite": true,
	}, nil, 400)
}

// TestPolicyChangesAreAudited pins that loosening what an agent may do leaves a
// record, as does every validation decision.
func TestPolicyChangesAreAudited(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	receive(t, "the dispatched job", backend.starts)

	c.mustDo("PUT", "/api/v1/sessions/"+session+"/policy", map[string]any{
		"mode": "GUARDED", "allowFilesystemWrite": true, "allowGitCommit": true,
	}, nil, 200)

	var audit struct {
		Items []struct {
			Action    string `json:"action"`
			SubjectID string `json:"subjectId"`
		} `json:"items"`
	}
	c.mustDo("GET", "/api/v1/me/audit", nil, &audit, 200)
	if len(audit.Items) != 1 || audit.Items[0].Action != "execution_policy.set" {
		t.Fatalf("audit = %+v, want the policy change", audit.Items)
	}
	if audit.Items[0].SubjectID != session {
		t.Errorf("subject = %q, want the session", audit.Items[0].SubjectID)
	}
}
