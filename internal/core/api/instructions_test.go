package api_test

import (
	"net/http"
	"strings"
	"testing"
)

type instructions struct {
	Agent       string `json:"agent"`
	Supervision string `json:"supervision"`
}

type instructionsView struct {
	Own       instructions `json:"own"`
	Inherited instructions `json:"inherited"`
}

func TestInstructionsEndpoints(t *testing.T) {
	c, _, projectID, _ := setup(t)
	var view instructionsView

	global := instructions{Agent: "Answer in French.", Supervision: "Reading is ordinary work."}
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", global, &view, http.StatusOK)
	if view.Own != global || view.Inherited != (instructions{}) {
		t.Fatalf("global view after PUT = %+v", view)
	}
	c.mustDo(http.MethodGet, "/api/v1/me/instructions", nil, &view, http.StatusOK)
	if view.Own != global {
		t.Fatalf("global view = %+v", view)
	}

	project := instructions{Agent: "Use worktrees.", Supervision: "Never touch production."}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", project, &view, http.StatusOK)
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/instructions", nil, &view, http.StatusOK)
	if view.Own != project || view.Inherited != global {
		t.Fatalf("project view = %+v", view)
	}
	// The project agent text is the one the project form has always edited.
	var read struct {
		Instructions string `json:"instructions"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID, nil, &read, http.StatusOK)
	if read.Instructions != project.Agent {
		t.Fatalf("project instructions = %q, want %q", read.Instructions, project.Agent)
	}

	backend := instructions{Agent: "The laptop has no GPU.", Supervision: "Local builds are fine."}
	c.mustDo(http.MethodPut, "/api/v1/backends/"+c.backendID+"/instructions", backend, &view, http.StatusOK)
	c.mustDo(http.MethodGet, "/api/v1/backends/"+c.backendID+"/instructions", nil, &view, http.StatusOK)
	if view.Own != backend || view.Inherited.Supervision != global.Supervision || view.Inherited.Agent != global.Agent {
		t.Fatalf("backend view = %+v", view)
	}

	other := c.asUser("other")
	other.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/instructions", nil, nil, http.StatusNotFound)
	other.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", project, nil, http.StatusNotFound)
	other.mustDo(http.MethodGet, "/api/v1/backends/"+c.backendID+"/instructions", nil, nil, http.StatusNotFound)
	other.mustDo(http.MethodPut, "/api/v1/backends/"+c.backendID+"/instructions", backend, nil, http.StatusNotFound)
	other.mustDo(http.MethodGet, "/api/v1/me/instructions", nil, &view, http.StatusOK)
	if view.Own != (instructions{}) {
		t.Fatalf("global instructions leaked across accounts: %+v", view)
	}
}

func TestInstructionsTooLong(t *testing.T) {
	c, _, projectID, _ := setup(t)
	long := strings.Repeat("a", 4001)
	limit := strings.Repeat("a", 4000)

	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Agent: long}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Supervision: long}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Agent: limit, Supervision: limit}, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/backends/"+c.backendID+"/instructions", instructions{Agent: long}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", instructions{Supervision: long}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", instructions{Agent: long}, nil, http.StatusBadRequest)
}

// TestLongProjectAgentTextSurvivesAnInstructionsEdit pins the absence of a
// retroactive limit: a project agent text written through the project form
// before the limit existed can be sent back unchanged with a new supervision,
// but not changed into another over-long text.
func TestLongProjectAgentTextSurvivesAnInstructionsEdit(t *testing.T) {
	c, _, projectID, _ := setup(t)
	legacy := strings.Repeat("b", 5000)
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+projectID, map[string]any{"instructions": legacy}, nil, http.StatusOK)

	var view instructionsView
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions",
		instructions{Agent: legacy, Supervision: "Stop before any push."}, &view, http.StatusOK)
	if view.Own.Agent != legacy || view.Own.Supervision != "Stop before any push." {
		t.Fatalf("project view = %+v", view)
	}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions",
		instructions{Agent: legacy + "c"}, nil, http.StatusBadRequest)
}

// TestPolicySupervisionRejectedAboveSession pins what an old client gets when
// it still writes supervision into a default policy: a refusal pointing to the
// new place, never a silent drop.
func TestPolicySupervisionRejectedAboveSession(t *testing.T) {
	c, _, projectID, dirID := setup(t)
	policy := guarded()
	policy["mode"] = "SUPERVISED"
	policy["supervision"] = "Reading is allowed"

	for _, path := range []string{
		"/api/v1/me/policy",
		"/api/v1/backends/" + c.backendID + "/policy",
		"/api/v1/projects/" + projectID + "/policy",
	} {
		c.mustDo(http.MethodPut, path, policy, nil, http.StatusBadRequest)
		if !strings.Contains(c.lastBody, "/instructions") {
			t.Fatalf("PUT %s refusal does not point to /instructions: %s", path, c.lastBody)
		}
	}
	// Blank supervision is what old clients send when nothing is written.
	blank := guarded()
	blank["supervision"] = "  "
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", blank, nil, http.StatusOK)

	session := c.startSession(projectID, c.backendID, dirID, "Inspect")
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", policy, nil, http.StatusOK)
}
