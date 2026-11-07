package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
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
	c.mustDo(http.MethodPut, "/api/v1/backends/"+c.backendID+"/instructions", instructions{Supervision: long}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", instructions{Supervision: long}, nil, http.StatusBadRequest)
}

// TestProjectAgentTextHasNoLengthLimit pins that the project agent text is
// bounded nowhere, as through the project form, which wrote it long before the
// limit existed. Only the project supervision is.
func TestProjectAgentTextHasNoLengthLimit(t *testing.T) {
	c, _, projectID, _ := setup(t)
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+projectID, map[string]any{"instructions": strings.Repeat("b", 5000)}, nil, http.StatusOK)

	changed := strings.Repeat("c", 5000)
	var view instructionsView
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions",
		instructions{Agent: changed, Supervision: "Stop before any push."}, &view, http.StatusOK)
	if view.Own.Agent != changed || view.Own.Supervision != "Stop before any push." {
		t.Fatalf("project view = %+v", view)
	}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions",
		instructions{Agent: changed, Supervision: strings.Repeat("a", 4001)}, nil, http.StatusBadRequest)
}

// TestInstructionsRequireBothTexts pins that a PUT names both texts: it
// replaces both, so a client that only knew one would wipe the other.
func TestInstructionsRequireBothTexts(t *testing.T) {
	c, _, projectID, _ := setup(t)
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+projectID, map[string]any{"instructions": "Use worktrees."}, nil, http.StatusOK)

	for _, path := range []string{
		"/api/v1/me/instructions",
		"/api/v1/backends/" + c.backendID + "/instructions",
		"/api/v1/projects/" + projectID + "/instructions",
	} {
		for _, body := range []any{
			nil,
			map[string]any{},
			map[string]any{"agent": "x"},
			map[string]any{"supervision": "x"},
		} {
			c.mustDo(http.MethodPut, path, body, nil, http.StatusBadRequest)
		}
	}
	var view instructionsView
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/instructions", nil, &view, http.StatusOK)
	if view.Own.Agent != "Use worktrees." {
		t.Fatalf("a refused PUT changed the project agent text: %+v", view)
	}
}

// TestProjectInstructionsAnnounceTheProjectUpdate pins the event clients
// refresh their copy of a Project from: without it, a form left open on the
// old agent text would PATCH it back over the new one.
func TestProjectInstructionsAnnounceTheProjectUpdate(t *testing.T) {
	c, _, projectID, _ := setup(t)
	path := "/api/v1/projects/" + projectID + "/instructions"
	updates := func() [][]string {
		var out [][]string
		for _, event := range lifecycleEvents(t, c, "project.updated") {
			var payload struct {
				ChangedFields []string `json:"changedFields"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			out = append(out, payload.ChangedFields)
		}
		return out
	}

	c.mustDo(http.MethodPut, path, instructions{Agent: "Use worktrees."}, nil, http.StatusOK)
	if got := updates(); len(got) != 1 || !slices.Equal(got[0], []string{"instructions"}) {
		t.Fatalf("agent change announced as %v", got)
	}
	c.mustDo(http.MethodPut, path, instructions{Agent: "Use worktrees.", Supervision: "Stop before any push."}, nil, http.StatusOK)
	if got := updates(); len(got) != 2 || !slices.Equal(got[1], []string{"supervisionInstructions"}) {
		t.Fatalf("supervision change announced as %v", got)
	}
	c.mustDo(http.MethodPut, path, instructions{Agent: "Use worktrees.", Supervision: "Stop before any push."}, nil, http.StatusOK)
	if got := updates(); len(got) != 2 {
		t.Fatalf("an identical PUT was announced: %v", got)
	}
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
		var refusal struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(c.lastBody), &refusal); err != nil {
			t.Fatal(err)
		}
		if want := "invalid request: supervision is set with the instructions now: use /instructions"; refusal.Error.Message != want {
			t.Fatalf("PUT %s refusal = %q, want %q", path, refusal.Error.Message, want)
		}
	}
	// Blank supervision is what old clients send when nothing is written: it
	// is accepted and not stored.
	blank := guarded()
	blank["supervision"] = "  "
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", blank, nil, http.StatusOK)
	var stored map[string]any
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/policy", nil, &stored, http.StatusOK)
	if _, ok := stored["supervision"]; ok {
		t.Fatalf("a blank supervision was stored: %v", stored)
	}

	session := c.startSession(projectID, c.backendID, dirID, "Inspect")
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", policy, nil, http.StatusOK)
}
