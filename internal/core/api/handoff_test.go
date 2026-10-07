package api_test

import (
	"context"
	"net/http"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

type handoffResponse struct {
	Run struct {
		ID                string `json:"id"`
		BackendInstanceID string `json:"backendInstanceId"`
		ResumeStatus      string `json:"resumeStatus"`
	} `json:"run"`
	MissingSkills []string `json:"missingSkills"`
}

// TestMovingASessionStartsANewRun pins specification section 34: a backend
// change is explicit and visible, the timeline stays continuous, and the native
// provider session does not travel because it belongs to the machine holding it.
func TestMovingASessionStartsANewRun(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	laptopID, laptopCredential := c.registerBackend("laptop")
	laptop := c.connectBackend(laptopCredential)

	session := c.startSessionFrom(project, laptopID, "web", "deploy the chart")
	start := receive(t, "the job to start", laptop.starts)

	ctx := context.Background()
	backendID, _ := c.registerBackend("desktop")

	// A Job still running stays where it is: moving would leave work behind on a
	// backend nobody is watching any more.
	c.mustDo(http.MethodPatch, "/api/v1/sessions/"+session,
		map[string]any{"backendInstanceId": backendID}, nil, http.StatusConflict)

	laptop.emit(t, ctx, laptop.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return laptop.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "done")
	}))
	waitUntil(t, "the job to finish", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "COMPLETED"
	})

	var handoff handoffResponse
	c.mustDo(http.MethodPatch, "/api/v1/sessions/"+session,
		map[string]any{"backendInstanceId": backendID}, &handoff, http.StatusOK)

	if handoff.Run.BackendInstanceID != backendID {
		t.Fatalf("run backend = %q, want the new one", handoff.Run.BackendInstanceID)
	}
	if handoff.Run.ID == start.GetRunId() {
		t.Fatal("a backend change must start a new run")
	}
	if handoff.Run.ResumeStatus != "UNKNOWN" {
		t.Fatalf("resume status = %q, want UNKNOWN on a fresh run", handoff.Run.ResumeStatus)
	}

	// The timeline is continuous across Runs: the history of the first one is
	// still there.
	snapshot := c.snapshot(session)
	if len(snapshot.Runs) != 2 {
		t.Fatalf("runs = %d, want the previous one kept", len(snapshot.Runs))
	}
	if c.countEvents(session, "user.message") != 1 {
		t.Fatal("the history of the previous run must survive the move")
	}

	// Moving to where it already is says so rather than making a second run.
	c.mustDo(http.MethodPatch, "/api/v1/sessions/"+session,
		map[string]any{"backendInstanceId": backendID}, nil, http.StatusConflict)
}

// TestAHandoffReportsALostLocalSkill pins the handoff half of section 18: Core
// knows the metadata of a Skill that exists only on one backend, so it can say
// that moving the work loses it — without ever holding its content.
func TestAHandoffReportsALostLocalSkill(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	laptopID, laptopCredential := c.registerBackend("laptop")
	laptop := c.connectBackend(laptopCredential)
	desktopID, desktopCredential := c.registerBackend("desktop")
	desktop := c.connectBackend(desktopCredential)

	ctx := context.Background()
	if err := laptop.sdk.SendSkillInventory(ctx, []*backendv1.LocalSkill{
		{Name: "corp-deploy", Description: "Only reachable from the work network.", Available: true},
		{Name: "shared", Available: true},
	}); err != nil {
		t.Fatalf("reporting the laptop inventory: %v", err)
	}
	if err := desktop.sdk.SendSkillInventory(ctx, []*backendv1.LocalSkill{
		{Name: "shared", Available: true},
	}); err != nil {
		t.Fatalf("reporting the desktop inventory: %v", err)
	}

	var laptopSkills struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	waitUntil(t, "the inventories to be recorded", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/backends/"+laptopID+"/skills", nil, &laptopSkills, http.StatusOK)
		return len(laptopSkills.Items) == 2
	})

	session := c.startSessionFrom(project, laptopID, "web", "deploy the chart")
	start := receive(t, "the job to start", laptop.starts)
	laptop.emit(t, ctx, laptop.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return laptop.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "done")
	}))
	waitUntil(t, "the job to finish", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "COMPLETED"
	})

	var handoff handoffResponse
	c.mustDo(http.MethodPatch, "/api/v1/sessions/"+session,
		map[string]any{"backendInstanceId": desktopID}, &handoff, http.StatusOK)

	if len(handoff.MissingSkills) != 1 || handoff.MissingSkills[0] != "corp-deploy" {
		t.Fatalf("missing skills = %v, want the one only the laptop has", handoff.MissingSkills)
	}
}
