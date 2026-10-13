package api_test

import (
	"context"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestInstanceBackendProjectPolicyInheritance(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	instance := guarded()
	instance["rules"] = []map[string]any{{"effect": "DENY", "capability": "SHELL", "match": "kubectl delete *"}}
	c.mustDo("PUT", "/api/v1/me/policy", instance, nil, 200)
	var view map[string]any
	c.mustDo("GET", "/api/v1/backends/"+c.backendID+"/policy", nil, &view, 200)
	if view["inherited"] != true {
		t.Fatalf("backend should inherit: %v", view)
	}
	session := c.startSession(projectID, c.backendID, dirID, "Inspect")
	start := receive(t, "instance defaults dispatched", backend.starts)
	if start.GetExecutionPolicy().GetMode() != backendv1.ExecutionMode_EXECUTION_MODE_GUARDED {
		t.Fatal("instance defaults did not reach the job")
	}
	backendPolicy := guarded()
	backendPolicy["mode"] = "SUPERVISED"
	backendPolicy["supervision"] = "Reading is allowed"
	c.mustDo("PUT", "/api/v1/backends/"+c.backendID+"/policy", backendPolicy, nil, 200)
	update := receive(t, "running job policy update", backend.policies)
	if update.GetExecutionPolicy().GetMode() != backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED {
		t.Fatal("new backend defaults were not pushed to the running job")
	}
	c.mustDo("GET", "/api/v1/sessions/"+session+"/policy", nil, &view, 200)
	if view["effective"].(map[string]any)["mode"] != "SUPERVISED" {
		t.Fatalf("backend override missing: %v", view)
	}
	project := guarded()
	project["mode"] = "INTERACTIVE"
	c.mustDo("PUT", "/api/v1/projects/"+projectID+"/policy", project, nil, 200)
	c.mustDo("GET", "/api/v1/sessions/"+session+"/policy", nil, &view, 200)
	effective := view["effective"].(map[string]any)
	if effective["mode"] != "INTERACTIVE" || len(effective["rules"].([]any)) != 1 {
		t.Fatalf("project precedence or instance refusal lost: %v", view)
	}
	c.mustDo("PUT", "/api/v1/projects/"+projectID+"/policy", map[string]any{}, nil, 200)
	c.mustDo("GET", "/api/v1/projects/"+projectID+"/policy/origin", nil, &view, 200)
	if view["inherited"] != true {
		t.Fatal("project reset copied defaults")
	}
	c.mustDo("PUT", "/api/v1/backends/"+c.backendID+"/policy", map[string]any{}, nil, 200)
	c.mustDo("GET", "/api/v1/sessions/"+session+"/policy", nil, &view, 200)
	if view["effective"].(map[string]any)["mode"] != "GUARDED" {
		t.Fatal("backend reset did not restore instance inheritance")
	}
	other := c.asUser("other")
	other.mustDo("GET", "/api/v1/me/policy", nil, &view, 200)
	if view["effective"].(map[string]any)["mode"] != "INTERACTIVE" {
		t.Fatal("instance settings leaked across accounts")
	}
	other.mustDo("PUT", "/api/v1/backends/"+c.backendID+"/policy", guarded(), nil, 404)
	c.mustDo("PUT", "/api/v1/me/policy", map[string]any{}, nil, 200)
	c.mustDo("GET", "/api/v1/sessions/"+session+"/policy", nil, &view, 200)
	if view["effective"].(map[string]any)["mode"] != "INTERACTIVE" {
		t.Fatal("instance reset did not restore built-in defaults")
	}
}

func TestPolicyUsesTheJobsBackendAndNarrowerOverrides(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.mustDo("PUT", "/api/v1/me/policy", guarded(), nil, 200)
	session := c.startSession(projectID, c.backendID, dirID, "Inspect")
	start := receive(t, "first job", backend.starts)
	c.mustDo("PUT", "/api/v1/backends/"+c.backendID+"/policy", map[string]any{"mode": "SUPERVISED", "supervision": "Read freely"}, nil, 200)
	ctx := context.Background()
	jobID := domain.JobID(start.GetJobId())
	effective, err := c.store.EffectivePolicy(ctx, jobID)
	if err != nil || effective.Mode != domain.ExecutionSupervised || effective.Supervision != "Read freely" {
		t.Fatalf("job backend policy: %+v, %v", effective, err)
	}
	c.mustDo("PUT", "/api/v1/sessions/"+session+"/policy", guarded(), nil, 200)
	effective, err = c.store.EffectivePolicy(ctx, jobID)
	if err != nil || effective.Mode != domain.ExecutionGuarded {
		t.Fatalf("session override: %+v, %v", effective, err)
	}
	override := domain.DefaultExecutionPolicy()
	if err := c.store.SetJobExecutionPolicy(ctx, jobID, &override); err != nil {
		t.Fatal(err)
	}
	effective, err = c.store.EffectivePolicy(ctx, jobID)
	if err != nil || effective.Mode != domain.ExecutionInteractive {
		t.Fatalf("job override: %+v, %v", effective, err)
	}
}

func TestBackendPolicyFollowsAHandoffWithoutChangingEarlierJobs(t *testing.T) {
	c, laptop, projectID, dirID := setup(t)
	laptopID := c.backendID
	desktopID, credential := c.registerBackend("desktop")
	desktop := c.connectBackend(credential)
	c.mustDo("PUT", "/api/v1/backends/"+laptopID+"/policy", guarded(), nil, 200)
	c.mustDo("PUT", "/api/v1/backends/"+desktopID+"/policy", map[string]any{"mode": "SUPERVISED"}, nil, 200)
	session := c.startSession(projectID, laptopID, dirID, "Inspect")
	first := receive(t, "laptop job", laptop.starts)
	ctx := context.Background()
	laptop.emit(t, ctx, laptop.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return laptop.events.JobCompleted(ctx, first.GetRunId(), first.GetJobId(), "done", nil)
	}))
	waitUntil(t, "first job ends", func() bool { return c.jobStatus(session, first.GetJobId()) == "COMPLETED" })
	c.mustDo("PATCH", "/api/v1/sessions/"+session, map[string]any{"backendInstanceId": desktopID}, nil, 200)
	var view sessionPolicy
	c.mustDo("GET", "/api/v1/sessions/"+session+"/policy", nil, &view, 200)
	if view.Effective.Mode != "SUPERVISED" {
		t.Fatalf("handoff retained laptop policy: %+v", view)
	}
	old, err := c.store.EffectivePolicy(ctx, domain.JobID(first.GetJobId()))
	if err != nil || old.Mode != domain.ExecutionGuarded {
		t.Fatalf("earlier job changed backend: %+v, %v", old, err)
	}
	c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Continue"}, nil, 201)
	second := receive(t, "desktop job", desktop.starts)
	if second.GetExecutionPolicy().GetMode() != backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED {
		t.Fatal("new job did not use desktop defaults")
	}
}
