package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// TestAgentInstructionsLayeredBeforeDynamicNotes pins that the levels open the
// agent text, labelled, and that what Core adds for one Job — here the
// delegation note — comes after them rather than inside the last section.
func TestAgentInstructionsLayeredBeforeDynamicNotes(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Agent: "G"}, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", instructions{Agent: "P"}, nil, http.StatusOK)

	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager job", backend.starts)
	text := manager.GetProjectContext().GetProjectInstructions()
	global, project := strings.Index(text, "[Global]\nG"), strings.Index(text, "[Project homelab]\nP")
	if !strings.HasPrefix(text, domain.LayeredPreamble) || global < 0 || project < global {
		t.Fatalf("agent instructions = %q, want the preamble then Global then Project", text)
	}

	_, child := delegatedSession(t, backend, manager, map[string]any{"message": "Fix the parser"})
	text = child.GetProjectContext().GetProjectInstructions()
	sections := strings.Index(text, "[Project homelab]\nP")
	note := strings.Index(text, "This session is delegated by manager session")
	if !strings.HasPrefix(text, domain.LayeredPreamble) || sections < 0 || note < 0 {
		t.Fatalf("delegated agent instructions = %q, want the sections and the delegation note", text)
	}
	if note < sections+len("[Project homelab]\nP") || !strings.HasPrefix(text[sections:], "[Project homelab]\nP\n") {
		t.Fatalf("delegated agent instructions = %q, want the delegation note after the sections, on its own line", text)
	}
}

// TestAgentInstructionsSingleLayerUnchanged pins that an installation writing
// only the project instructions sends them as it always did: no header, no
// preamble.
func TestAgentInstructionsSingleLayerUnchanged(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+projectID, map[string]any{"instructions": "Use worktrees."}, nil, http.StatusOK)

	c.startSession(projectID, c.backendID, dirID, "Work")
	start := receive(t, "the job", backend.starts)
	if got := start.GetProjectContext().GetProjectInstructions(); got != "Use worktrees." {
		t.Fatalf("agent instructions = %q, want the project text unchanged", got)
	}
}

// TestGlobalSupervisionSurvivesProjectMode is the bug that motivated layered
// instructions: a Project that only switched to SUPERVISED replaced the global
// policy wholesale, supervision included.
func TestGlobalSupervisionSurvivesProjectMode(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Supervision: "S"}, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", map[string]any{"mode": "SUPERVISED"}, nil, http.StatusOK)

	c.startSession(projectID, c.backendID, dirID, "Work")
	start := receive(t, "the job", backend.starts)
	if got := start.GetExecutionPolicy().GetSupervision(); got != "S" {
		t.Fatalf("supervision = %q, want the global text", got)
	}
}

// TestSupervisionLayeredWithSession pins the session level under the others,
// both when a running Job is told of a policy change and when the next one
// starts.
func TestSupervisionLayeredWithSession(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Supervision: "S1"}, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/backends/"+c.backendID+"/instructions", instructions{Supervision: "S2"}, nil, http.StatusOK)

	session := c.startSession(projectID, c.backendID, dirID, "Work")
	first := receive(t, "the first job", backend.starts)
	want := domain.LayeredPreamble + "\n\n[Global]\nS1\n\n[Backend test-backend]\nS2"
	if got := first.GetExecutionPolicy().GetSupervision(); got != want {
		t.Fatalf("supervision = %q, want %q", got, want)
	}

	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", map[string]any{"mode": "SUPERVISED", "supervision": "S3"}, nil, http.StatusOK)
	want += "\n\n[Session]\nS3"
	pushed := receive(t, "the policy update", backend.policies)
	if got := pushed.GetExecutionPolicy().GetSupervision(); got != want {
		t.Fatalf("pushed supervision = %q, want %q", got, want)
	}

	completeDelegatedJob(t, c, backend, first)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Next"}, nil, http.StatusCreated)
	next := receive(t, "the next job", backend.starts)
	if got := next.GetExecutionPolicy().GetSupervision(); got != want {
		t.Fatalf("next supervision = %q, want %q", got, want)
	}
}

// TestDelegatedSessionDoesNotInheritManagerSupervision pins that a delegate
// takes its manager's permissions, not what the manager's reviewer was told:
// that text was about the manager's project.
func TestDelegatedSessionDoesNotInheritManagerSupervision(t *testing.T) {
	c, backend, managerProject, dirID := setup(t)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+managerProject+"/instructions", instructions{Supervision: "M"}, nil, http.StatusOK)
	managerID := c.startSession(managerProject, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager job", backend.starts)
	// The manager's own session policy holds supervision too, which is what an
	// effective policy copied into the delegate would carry.
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+managerID+"/policy", map[string]any{"mode": "SUPERVISED", "supervision": "M"}, nil, http.StatusOK)
	_ = receive(t, "the manager policy", backend.policies)

	childProject := c.createProject("alpha")
	c.mustDo(http.MethodPut, "/api/v1/projects/"+childProject+"/instructions", instructions{Supervision: "A"}, nil, http.StatusOK)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"projectId": childProject, "message": "Fix the parser"})
	if got := child.GetExecutionPolicy().GetSupervision(); got != "A" {
		t.Fatalf("delegate supervision = %q, want only its own project's", got)
	}
	if mode := child.GetExecutionPolicy().GetMode().String(); !strings.HasSuffix(mode, "SUPERVISED") {
		t.Fatalf("delegate mode = %s, want the manager's", mode)
	}
	var origin struct {
		Own map[string]any `json:"own"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+childID+"/policy", nil, &origin, http.StatusOK)
	if _, present := origin.Own["supervision"]; present || origin.Own == nil {
		t.Fatalf("delegate own policy = %v, want one without supervision", origin.Own)
	}
}

// TestInstructionsPreviewMatchesJob pins that the preview is what a Job
// started there reads, so an editor never shows one text and sends another.
func TestInstructionsPreviewMatchesJob(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.mustDo(http.MethodPut, "/api/v1/me/instructions", instructions{Agent: "G", Supervision: "S1"}, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/instructions", instructions{Agent: "P", Supervision: "SP"}, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/backends/"+c.backendID+"/instructions", instructions{Agent: "B", Supervision: "S2"}, nil, http.StatusOK)

	session := c.startSession(projectID, c.backendID, dirID, "Work")
	start := receive(t, "the job", backend.starts)

	preview := func(caller *core, project string, query url.Values, want int) instructions {
		t.Helper()
		var got instructions
		caller.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/instructions/preview?"+query.Encode(), nil, &got, want)
		return got
	}
	got := preview(c, projectID, url.Values{"backendId": {c.backendID}}, http.StatusOK)
	if got.Agent != start.GetProjectContext().GetProjectInstructions() || got.Supervision != start.GetExecutionPolicy().GetSupervision() {
		t.Fatalf("preview = %+v, job agent = %q, supervision = %q", got,
			start.GetProjectContext().GetProjectInstructions(), start.GetExecutionPolicy().GetSupervision())
	}
	if !strings.Contains(got.Agent, "[Backend test-backend]\nB") || !strings.Contains(got.Supervision, "[Project homelab]\nSP") {
		t.Fatalf("preview = %+v, want every level", got)
	}

	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", map[string]any{"mode": "SUPERVISED", "supervision": "S3"}, nil, http.StatusOK)
	got = preview(c, projectID, url.Values{"backendId": {c.backendID}, "sessionId": {session}}, http.StatusOK)
	if !strings.HasSuffix(got.Supervision, "[Session]\nS3") {
		t.Fatalf("preview supervision = %q, want the session level last", got.Supervision)
	}

	other := c.asUser("other")
	preview(other, projectID, url.Values{"backendId": {c.backendID}}, http.StatusNotFound)
	preview(c, projectID, url.Values{"backendId": {"00000000-0000-0000-0000-000000000000"}}, http.StatusNotFound)
	preview(c, c.createProject("elsewhere"), url.Values{"backendId": {c.backendID}, "sessionId": {session}}, http.StatusNotFound)
	preview(c, projectID, url.Values{}, http.StatusBadRequest)
}
