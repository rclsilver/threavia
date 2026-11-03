package api_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// TestAQuietScheduleRunThatFoundNothingTellsNobody pins what a quiet schedule
// is for: a daily check that speaks only when something changed. A run its
// agent dismisses must not ring a phone, mark the Session unread or move it up
// the list; a run of the same schedule that has something to say does all
// three, as any message would. The way out is offered only to such a run: any
// other Job was asked for an answer.
func TestAQuietScheduleRunThatFoundNothingTellsNobody(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	ctx := context.Background()

	// Nobody is watching, and a phone is subscribed from the start: a
	// notification is sent in the background and reads the subscriptions when
	// it goes, so the first Job's own Done must be counted, not raced.
	pushes := newPushService(t)
	c.svc.SetPushClient(pushes.server.Client())
	c.mustDo(http.MethodPost, "/api/v1/me/push/subscriptions", map[string]any{
		"endpoint": pushes.server.URL + "/push/phone",
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(pushes.browser.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(pushes.auth),
		},
	}, nil, http.StatusCreated)

	session := c.startSession(projectID, c.backendID, dirID, "watch my on-call schedule")
	first := receive(t, "the first job", backend.starts)

	if hasTool(first, "schedule_dismiss") {
		t.Fatal("a Job a person asked for was offered to end without an answer")
	}
	if err := backend.callCoreToolExpectingFailure(t, ctx, first.GetRunId(), first.GetJobId(), "schedule_dismiss", nil); !strings.Contains(err.Error(), "quiet schedule") {
		t.Fatalf("dismissing an ordinary Job: %v, want a refusal naming quiet schedules", err)
	}

	backend.callCoreToolExpectingFailure(t, ctx, first.GetRunId(), first.GetJobId(), "schedule_create",
		map[string]any{"cron": "0 9 * * 1-5", "message": "check", "quiet": "yes"})
	created := backend.callCoreTool(t, ctx, first.GetRunId(), first.GetJobId(), "schedule_create", map[string]any{
		"cron": "0 9 * * 1-5", "message": "Check my on-call schedule; dismiss if nothing changed.", "quiet": true,
	})
	schedule := created["schedule"].(map[string]any)
	if schedule["quiet"] != true {
		t.Fatalf("created = %v, want a quiet schedule", schedule)
	}
	scheduleID := created["scheduleId"].(string)
	completeDelegatedJob(t, c, backend, first)
	waitUntil(t, "the notification of the first job", func() bool { return len(pushes.messages()) >= 1 })

	listed := func() (unread bool, updatedAt time.Time) {
		t.Helper()
		var list struct {
			Items []struct {
				ID        string    `json:"id"`
				Unread    bool      `json:"unread"`
				UpdatedAt time.Time `json:"updatedAt"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/sessions", nil, &list, http.StatusOK)
		for _, item := range list.Items {
			if item.ID == session {
				return item.Unread, item.UpdatedAt
			}
		}
		t.Fatalf("session %s is not listed", session)
		return false, time.Time{}
	}
	events := c.snapshot(session).Events
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/read", map[string]any{"sequence": events[len(events)-1].Sequence}, nil, http.StatusNoContent)
	_, before := listed()

	// run plays one scheduled run and says whether its end was marked
	// dismissed, which is what every client folds and stays silent on.
	run := func(start *backendv1.StartJob, dismiss bool) bool {
		t.Helper()
		if !hasTool(start, "schedule_dismiss") {
			t.Fatal("a run of a quiet schedule was not offered schedule_dismiss")
		}
		backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
			return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), "Checking the schedule.")
		}))
		if dismiss {
			if got := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "schedule_dismiss", nil); got["dismissed"] != true {
				t.Fatalf("schedule_dismiss = %v", got)
			}
		}
		completeDelegatedJob(t, c, backend, start)
		var history struct {
			Items []struct {
				Type    string `json:"type"`
				JobID   string `json:"jobId"`
				Payload struct {
					Dismissed bool `json:"dismissed"`
				} `json:"payload"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/events", nil, &history, http.StatusOK)
		for _, event := range history.Items {
			if event.Type == "job.completed" && event.JobID == start.GetJobId() {
				return event.Payload.Dismissed
			}
		}
		t.Fatalf("job %s has no job.completed", start.GetJobId())
		return false
	}

	// 09:00, nothing changed: the agent dismisses the run.
	var list struct {
		Items []scheduleResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/schedules", nil, &list, http.StatusOK)
	c.svc.RunDueSchedules(ctx, list.Items[0].NextRunAt.Add(10*time.Second))
	if !run(receive(t, "the quiet scheduled job", backend.starts), true) {
		t.Fatal("the dismissed run did not end marked dismissed")
	}
	if unread, updatedAt := listed(); unread || !updatedAt.Equal(before) {
		t.Fatalf("after a dismissed run: unread=%v updatedAt=%v (was %v), want the session left as it was", unread, updatedAt, before)
	}

	// Something changed: the same schedule, run now, answers as usual.
	c.mustDo(http.MethodPost, "/api/v1/schedules/"+scheduleID+"/run", nil, nil, http.StatusCreated)
	if run(receive(t, "the run-now job", backend.starts), false) {
		t.Fatal("a run that answered ended marked dismissed")
	}
	waitUntil(t, "the notification of the run that had something to say", func() bool { return len(pushes.messages()) >= 2 })
	if got := pushes.messages(); len(got) != 2 || !strings.HasPrefix(got[1]["title"], "Done") {
		t.Fatalf("notifications = %v, want the first job's Done and only the Done of the run that answered", got)
	}
	if unread, updatedAt := listed(); !unread || !updatedAt.After(before) {
		t.Fatalf("after an answered run: unread=%v updatedAt=%v (was %v), want unread and moved up", unread, updatedAt, before)
	}
}

// TestADismissedDelegatedRunDoesNotWakeItsManager pins the same silence for a
// worker conversation: its manager reports to the person, so waking it for a
// run that had nothing to report would only move the notification one hop.
func TestADismissedDelegatedRunDoesNotWakeItsManager(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	ctx := context.Background()
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Watch the on-call schedule"})
	completeDelegatedJob(t, c, backend, manager)
	completeDelegatedJob(t, c, backend, child)
	completeDelegatedJob(t, c, backend, receive(t, "the manager woken by the worker's first answer", backend.starts))

	var created scheduleResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+childID+"/schedules", map[string]any{
		"cron": "0 9 * * 1-5", "message": "Check the schedule; dismiss if nothing changed.", "quiet": true,
	}, &created, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/schedules/"+created.ID+"/run", nil, nil, http.StatusCreated)
	quiet := receive(t, "the quiet run", backend.starts)
	backend.callCoreTool(t, ctx, quiet.GetRunId(), quiet.GetJobId(), "schedule_dismiss", nil)
	completeDelegatedJob(t, c, backend, quiet)

	expectNothing(t, "a manager woken by a run that had nothing to report", backend.starts)
	if jobs := len(c.snapshot(managerID).Jobs); jobs != 2 {
		t.Fatalf("manager has %d jobs, want its own and the one woken by the first answer", jobs)
	}
}

func hasTool(start *backendv1.StartJob, name string) bool {
	for _, tool := range start.GetProjectContext().GetTools() {
		if tool.GetName() == name {
			return true
		}
	}
	return false
}
