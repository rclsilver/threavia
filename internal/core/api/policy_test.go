package api_test

import (
	"net/http"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// sessionPolicy is the shape a client reads: what applies, where it came from,
// and what it would fall back to.
type sessionPolicy struct {
	Effective executionPolicy `json:"effective"`
	Inherited bool            `json:"inherited"`
	Project   executionPolicy `json:"project"`
}

type executionPolicy struct {
	Mode                 string           `json:"mode"`
	AllowFilesystemWrite bool             `json:"allowFilesystemWrite"`
	AllowGitCommit       bool             `json:"allowGitCommit"`
	AllowGitPush         bool             `json:"allowGitPush"`
	AllowNetwork         bool             `json:"allowNetwork"`
	Rules                []permissionRule `json:"rules,omitempty"`
	MaxDurationSeconds   int              `json:"maxDurationSeconds,omitempty"`
	MaxActions           int              `json:"maxActions,omitempty"`
}

type permissionRule struct {
	Effect     string `json:"effect"`
	Capability string `json:"capability"`
	Match      string `json:"match,omitempty"`
	Note       string `json:"note,omitempty"`
}

func guarded() map[string]any {
	return map[string]any{
		"mode": "GUARDED", "allowFilesystemWrite": true, "allowGitCommit": true,
		"allowGitPush": false, "allowNetwork": true,
	}
}

// TestASessionFollowsItsProject pins the level that did not exist before: the
// same answer, given once, instead of once per Session.
func TestASessionFollowsItsProject(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", guarded(), nil, http.StatusOK)

	session := c.startSession(projectID, c.backendID, dirID, "Regarde le dépôt")
	start := receive(t, "the dispatched job", backend.starts)

	if mode := start.GetExecutionPolicy().GetMode(); mode != backendv1.ExecutionMode_EXECUTION_MODE_GUARDED {
		t.Fatalf("the job runs under %v, want the project default GUARDED", mode)
	}

	var view sessionPolicy
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/policy", nil, &view, http.StatusOK)
	if !view.Inherited {
		t.Error("the session reports an override it never set")
	}
	if view.Effective.Mode != "GUARDED" {
		t.Errorf("effective mode = %q, want GUARDED", view.Effective.Mode)
	}
}

// TestASessionMayLoosenASwitch pins the half that is a default: a Session that
// needs to push once says so, without the project being opened for everyone.
func TestASessionMayLoosenASwitch(t *testing.T) {
	c, _, projectID, dirID := setup(t)

	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", guarded(), nil, http.StatusOK)
	session := c.startSession(projectID, c.backendID, dirID, "Publie la release")

	loosened := guarded()
	loosened["allowGitPush"] = true
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", loosened, nil, http.StatusOK)

	var view sessionPolicy
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/policy", nil, &view, http.StatusOK)
	if !view.Effective.AllowGitPush {
		t.Error("the session was not allowed to loosen a project default")
	}
	if view.Inherited {
		t.Error("a session that set a policy still reports as inheriting")
	}
	// The project is untouched by what one session needed.
	if view.Project.AllowGitPush {
		t.Error("loosening a session changed the project")
	}
}

// TestASessionCannotLiftAProjectRefusal pins the half that is a limit, and the
// reason the Project level exists at all.
func TestASessionCannotLiftAProjectRefusal(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	project := guarded()
	project["rules"] = []map[string]any{{
		"effect": "DENY", "capability": "SHELL",
		"match": "kubectl delete *", "note": "shared cluster",
	}}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", project, nil, http.StatusOK)

	session := c.startSession(projectID, c.backendID, dirID, "Nettoie le cluster")

	// Saying the opposite is refused where it is written, rather than accepted
	// and quietly dropped.
	override := guarded()
	override["rules"] = []map[string]any{{
		"effect": "ALLOW", "capability": "SHELL", "match": "kubectl delete *",
	}}
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", override, nil, http.StatusBadRequest)

	// And the refusal is in force whatever else the session sets.
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", guarded(), nil, http.StatusOK)

	var view sessionPolicy
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/policy", nil, &view, http.StatusOK)
	if len(view.Effective.Rules) != 1 || view.Effective.Rules[0].Effect != "DENY" {
		t.Fatalf("effective rules = %+v, want the project refusal", view.Effective.Rules)
	}

	// The backend is told, because that is where it is enforced.
	start := receive(t, "the dispatched job", backend.starts)
	rules := start.GetExecutionPolicy().GetRules()
	if len(rules) != 1 {
		t.Fatalf("%d rules reached the backend, want 1", len(rules))
	}
	if rules[0].GetEffect() != backendv1.PermissionEffect_PERMISSION_EFFECT_DENY ||
		rules[0].GetCapability() != backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL ||
		rules[0].GetMatch() != "kubectl delete *" {
		t.Fatalf("the rule reached the backend as %+v", rules[0])
	}
	if rules[0].GetNote() != "shared cluster" {
		t.Error("the backend was not told why, so it cannot say why when it refuses")
	}
}

// TestASessionGoesBackToTheProject pins that an override can be undone, which
// is what makes setting one safe to do.
func TestASessionGoesBackToTheProject(t *testing.T) {
	c, _, projectID, dirID := setup(t)

	c.mustDo(http.MethodPut, "/api/v1/projects/"+projectID+"/policy", guarded(), nil, http.StatusOK)
	session := c.startSession(projectID, c.backendID, dirID, "Une exception")

	loosened := guarded()
	loosened["allowGitPush"] = true
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", loosened, nil, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", map[string]any{}, nil, http.StatusOK)

	var view sessionPolicy
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/policy", nil, &view, http.StatusOK)
	if !view.Inherited {
		t.Fatal("an emptied session policy did not hand the session back to its project")
	}
	if view.Effective.AllowGitPush {
		t.Error("the session kept what it had loosened")
	}
}
