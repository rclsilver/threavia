package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type scheduleResponse struct {
	ID          string      `json:"id"`
	Cron        string      `json:"cron"`
	Timezone    string      `json:"timezone"`
	Enabled     bool        `json:"enabled"`
	NextRunAt   *time.Time  `json:"nextRunAt"`
	LastOutcome string      `json:"lastOutcome"`
	Upcoming    []time.Time `json:"upcoming"`
}

// TestAScheduleSendsItsMessageOrSaysWhyNot pins what was decided for
// scheduled messages: on time and idle, the message becomes a Job marked as
// scheduled; busy, missed or late, it is skipped and the Session says so,
// never sent late or stacked.
func TestAScheduleSendsItsMessageOrSaysWhyNot(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	local := newWorkingRunner()
	t.Cleanup(local.stop)
	c.connectBackendWith(local, credential, t.TempDir())

	session := c.startSessionFrom(project, backendID, "web", "set things up")
	receive(t, "the first job to start", local.started)

	// Written in the zone the person lives in, read back in dates.
	var created scheduleResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/schedules", map[string]any{
		"cron": "0 8 * * *", "timezone": "Europe/Paris", "message": "check the backups",
	}, &created, http.StatusCreated)
	paris, _ := time.LoadLocation("Europe/Paris")
	if created.NextRunAt == nil || created.NextRunAt.In(paris).Hour() != 8 || len(created.Upcoming) != 3 {
		t.Fatalf("created = %+v, want the next three mornings at 8 in Paris", created)
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/schedules", map[string]any{
		"cron": "0 25 * * *", "message": "never",
	}, nil, http.StatusBadRequest)

	read := func() scheduleResponse {
		t.Helper()
		var list struct {
			Items []scheduleResponse `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/schedules", nil, &list, http.StatusOK)
		if len(list.Items) != 1 {
			t.Fatalf("%d schedules, want 1", len(list.Items))
		}
		return list.Items[0]
	}
	ctx := context.Background()

	// The first job is still running: the scheduled one does not pile up.
	due := *created.NextRunAt
	c.svc.RunDueSchedules(ctx, due.Add(10*time.Second))
	if got := read(); got.LastOutcome != "SKIPPED_BUSY" || !got.NextRunAt.After(due) {
		t.Fatalf("while busy: %+v, want skipped and planned for the next morning", got)
	}

	// Idle and on time: sent, as a message nobody typed.
	local.stop()
	waitUntil(t, "the first job to end", func() bool {
		var snapshot struct {
			Jobs []struct {
				Status string `json:"status"`
			} `json:"jobs"`
		}
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &snapshot, http.StatusOK)
		return len(snapshot.Jobs) == 1 && snapshot.Jobs[0].Status == "COMPLETED"
	})
	due = *read().NextRunAt
	c.svc.RunDueSchedules(ctx, due.Add(10*time.Second))
	if got := read(); got.LastOutcome != "SENT" {
		t.Fatalf("on time and idle: %+v, want sent", got)
	}

	// Core was down through the next one: skipped, not sent late.
	waitUntil(t, "the scheduled job to end", func() bool {
		var snapshot struct {
			Jobs []struct {
				Status string `json:"status"`
			} `json:"jobs"`
		}
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &snapshot, http.StatusOK)
		return len(snapshot.Jobs) == 2 && snapshot.Jobs[1].Status == "COMPLETED"
	})
	due = *read().NextRunAt
	c.svc.RunDueSchedules(ctx, due.Add(3*time.Hour))
	if got := read(); got.LastOutcome != "SKIPPED_MISSED" {
		t.Fatalf("after an outage: %+v, want skipped as missed", got)
	}

	var history struct {
		Items []struct {
			Type    string `json:"type"`
			Payload struct {
				Text       string `json:"text"`
				ScheduleID string `json:"scheduleId"`
				Outcome    string `json:"outcome"`
			} `json:"payload"`
		} `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/events", nil, &history, http.StatusOK)
	var sent, skipped []string
	for _, event := range history.Items {
		switch {
		case event.Type == "user.message" && event.Payload.ScheduleID == created.ID:
			sent = append(sent, event.Payload.Text)
		case event.Type == "schedule.skipped" && event.Payload.ScheduleID == created.ID:
			skipped = append(skipped, event.Payload.Outcome)
		}
	}
	if len(sent) != 1 || sent[0] != "check the backups" {
		t.Errorf("scheduled messages in the session = %v, want the one sent", sent)
	}
	if len(skipped) != 2 || skipped[0] != "SKIPPED_BUSY" || skipped[1] != "SKIPPED_MISSED" {
		t.Errorf("skips said in the session = %v, want busy then missed", skipped)
	}

	// Paused, it has no next run and nothing fires.
	var paused scheduleResponse
	c.mustDo(http.MethodPatch, "/api/v1/schedules/"+created.ID, map[string]any{"enabled": false}, &paused, http.StatusOK)
	if paused.Enabled || paused.NextRunAt != nil {
		t.Fatalf("paused = %+v, want no next run", paused)
	}
	c.mustDo(http.MethodDelete, "/api/v1/schedules/"+created.ID, nil, nil, http.StatusNoContent)
}
