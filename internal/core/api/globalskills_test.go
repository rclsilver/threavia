package api_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/skills"
)

type globalSkillResponse struct {
	skillResponse
	DisabledProjectIDs []string `json:"disabledProjectIds"`
}

// jobSkills returns the Skills a dispatched Job was given, as name → id.
func jobSkills(t *testing.T, backend *fakeBackend) map[string]string {
	t.Helper()
	start := receive(t, "the dispatched job", backend.starts)
	out := map[string]string{}
	for _, skill := range start.GetProjectContext().GetSkills() {
		out[skill.GetName()] = skill.GetSkillId()
	}
	return out
}

// TestGlobalSkillReachesEveryProjectUntilTurnedOff pins the activation model of
// global Skills: installed once, on in every Project by default, and a Project
// can turn one off for its next Jobs without affecting the others.
func TestGlobalSkillReachesEveryProjectUntilTurnedOff(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	var global skillResponse
	c.mustUpload("/api/v1/me/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy-helm"), &global)
	if global.ProjectID != "" {
		t.Fatalf("projectId = %q, want none for a global skill", global.ProjectID)
	}

	// A global Skill is not one of the Project's own: the Project list stays
	// what the Project installed.
	var projectList struct {
		Items []skillResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/skills", nil, &projectList, http.StatusOK)
	if len(projectList.Items) != 0 {
		t.Fatalf("project skills = %+v, want none", projectList.Items)
	}

	c.startSession(projectID, c.backendID, dirID, "First")
	if got := jobSkills(t, backend); got["deploy-helm"] != global.ID {
		t.Fatalf("job skills = %v, want the global skill without any activation", got)
	}

	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/skills/"+global.ID+"/activation",
		map[string]any{"enabled": false}, nil, http.StatusNoContent)
	var listed struct {
		Items []globalSkillResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/skills", nil, &listed, http.StatusOK)
	if len(listed.Items) != 1 || !slices.Equal(listed.Items[0].DisabledProjectIDs, []string{projectID}) {
		t.Fatalf("global skills = %+v, want it turned off in the project", listed.Items)
	}

	c.startSession(projectID, c.backendID, dirID, "Second")
	if got := jobSkills(t, backend); len(got) != 0 {
		t.Fatalf("job skills = %v, want none once turned off", got)
	}

	// Another Project is not affected by that choice.
	other := c.createProject("other")
	c.startSession(other, c.backendID, "", "Elsewhere")
	if got := jobSkills(t, backend); got["deploy-helm"] != global.ID {
		t.Fatalf("job skills in another project = %v, want the global skill", got)
	}

	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/skills/"+global.ID+"/activation",
		map[string]any{"enabled": true}, nil, http.StatusNoContent)
	c.startSession(projectID, c.backendID, dirID, "Third")
	if got := jobSkills(t, backend); got["deploy-helm"] != global.ID {
		t.Fatalf("job skills = %v, want the global skill back", got)
	}

	c.mustDo(http.MethodDelete, "/api/v1/skills/"+global.ID, nil, nil, http.StatusNoContent)
	if objects.count() != 0 {
		t.Fatal("uninstalling a global skill must remove its bundle")
	}
}

// TestProjectSkillTakesPrecedenceOverGlobal pins that a Project can replace a
// global Skill with its own version under the same name, and that the Job is
// given one Skill per name.
func TestProjectSkillTakesPrecedenceOverGlobal(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	var global, local, other skillResponse
	c.mustUpload("/api/v1/me/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy-helm"), &global)
	c.mustUpload("/api/v1/me/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "review-code"), &other)
	c.mustUpload("/api/v1/projects/"+projectID+"/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy-helm"), &local)

	c.startSession(projectID, c.backendID, dirID, "Deploy")
	got := jobSkills(t, backend)
	if len(got) != 2 || got["deploy-helm"] != local.ID || got["review-code"] != other.ID {
		t.Fatalf("job skills = %v, want the project's deploy-helm and the global review-code", got)
	}
}

// TestMakeSkillGlobal pins that promoting a Project Skill keeps its identity
// and bundle, survives the deletion of its former Project, and refuses to
// shadow an existing global Skill silently.
func TestMakeSkillGlobal(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))
	_, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	first := c.createProject("first")
	second := c.createProject("second")

	var installed skillResponse
	c.mustUpload("/api/v1/projects/"+first+"/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy-helm"), &installed)

	// Turning off is for global Skills: a Project Skill is uninstalled.
	c.mustDo(http.MethodPut, "/api/v1/projects/"+first+"/skills/"+installed.ID+"/activation",
		map[string]any{"enabled": false}, nil, http.StatusBadRequest)

	var promoted skillResponse
	c.mustDo(http.MethodPost, "/api/v1/skills/"+installed.ID+"/global", nil, &promoted, http.StatusOK)
	if promoted.ID != installed.ID || promoted.ProjectID != "" || promoted.BundleSHA256 != installed.BundleSHA256 {
		t.Fatalf("promoted = %+v, want the same skill without a project", promoted)
	}
	var again skillResponse
	c.mustDo(http.MethodPost, "/api/v1/skills/"+installed.ID+"/global", nil, &again, http.StatusOK)
	if again.ID != installed.ID {
		t.Fatalf("promoting a global skill again = %+v, want it unchanged", again)
	}

	// The bundle no longer belongs to the Project it came from.
	c.mustDo(http.MethodDelete, "/api/v1/projects/"+first, nil, nil, http.StatusNoContent)
	var listed struct {
		Items []globalSkillResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/skills", nil, &listed, http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0].ID != installed.ID {
		t.Fatalf("global skills after deleting the project = %+v, want the promoted skill", listed.Items)
	}
	if objects.count() != 1 {
		t.Fatalf("stored objects = %d, want the bundle kept", objects.count())
	}
	if _, err := backend.sdk.FetchSkill(context.Background(), installed.ID, installed.InstalledRevision, installed.BundleSHA256); err != nil {
		t.Fatalf("fetching the global bundle: %v", err)
	}

	var colliding skillResponse
	c.mustUpload("/api/v1/projects/"+second+"/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy-helm"), &colliding)
	c.mustDo(http.MethodPost, "/api/v1/skills/"+colliding.ID+"/global", nil, nil, http.StatusConflict)
	// The refusal names the skill, so the person knows which one to uninstall.
	if !strings.Contains(c.lastBody, "deploy-helm") {
		t.Fatalf("conflict = %s, want it to name the skill", c.lastBody)
	}
}

// TestGlobalSkillsBelongToTheirOwner pins that another user neither sees nor
// turns off someone's global Skills.
func TestGlobalSkillsBelongToTheirOwner(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	var global skillResponse
	c.mustUpload("/api/v1/me/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy-helm"), &global)

	other := c.asUser("alice")
	project := other.createProject("hers")
	var listed struct {
		Items []globalSkillResponse `json:"items"`
	}
	other.mustDo(http.MethodGet, "/api/v1/me/skills", nil, &listed, http.StatusOK)
	if len(listed.Items) != 0 {
		t.Fatalf("another user's global skills = %+v, want none", listed.Items)
	}
	other.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/skills/"+global.ID+"/activation",
		map[string]any{"enabled": false}, nil, http.StatusNotFound)
	other.mustDo(http.MethodPost, "/api/v1/skills/"+global.ID+"/global", nil, nil, http.StatusNotFound)
}
