package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestAgentManagesPersistentSessionSchedules(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	sessionID := c.startSession(projectID, c.backendID, dirID, "Schedule backups")
	start := receive(t, "the job", backend.starts)
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		t.Helper()
		return backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	fail := func(name string, input map[string]any) {
		t.Helper()
		backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	declared := map[string]bool{}
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	for _, tool := range start.GetProjectContext().GetTools() {
		declared[tool.GetName()] = true
	}
	for _, name := range []string{"schedule_list", "schedule_create", "schedule_update", "schedule_delete"} {
		if !declared[name] {
			t.Fatalf("%s not declared", name)
		}
	}
	if list := call("schedule_list", nil)["schedules"].([]any); len(list) != 0 {
		t.Fatal(list)
	}
	for _, input := range []map[string]any{{"cron": "bad", "message": "hello"}, {"cron": "0 8 * * *", "message": "hello", "timezone": "invalid"}, {"cron": "0 8 * * *", "message": " "}, {"cron": "0 8 * * *", "message": "hello", "enabled": "false"}, {"cron": false, "message": "hello"}} {
		fail("schedule_create", input)
	}
	created := call("schedule_create", map[string]any{"cron": "0 8 * * *", "message": "check backups"})
	id := created["scheduleId"].(string)
	notice := awaitFrame(t, web, "schedule.created").event
	if got := awaitFrame(t, editor, "schedule.created").event; got.Sequence != notice.Sequence {
		t.Fatal("clients received different schedule events")
	}
	var payload map[string]any
	if err := json.Unmarshal(notice.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	saved, err := c.store.SessionEventsAfter(ctx, "thomas", domain.SessionID(sessionID), domain.Sequence(notice.Sequence-1), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].JobID == nil || string(*saved[0].JobID) != start.GetJobId() || payload["actorJobId"] != start.GetJobId() || payload["scheduleId"] != id {
		t.Fatalf("incorrect event attribution: %+v %v", notice, payload)
	}
	if created["schedule"].(map[string]any)["timezone"] != "Europe/Paris" {
		t.Fatal(created)
	}
	paused := call("schedule_update", map[string]any{"scheduleId": id, "enabled": false})["schedule"].(map[string]any)
	if paused["enabled"] != false || paused["nextRunAt"] != nil || paused["message"] != "check backups" {
		t.Fatal(paused)
	}
	fail("schedule_update", map[string]any{"scheduleId": id, "message": nil})
	resumed := call("schedule_update", map[string]any{"scheduleId": id, "enabled": true, "cron": "15 9 * * *", "timezone": "UTC", "message": "verify backups"})["schedule"].(map[string]any)
	if resumed["enabled"] != true || resumed["nextRunAt"] == nil {
		t.Fatal(resumed)
	}
	other := c.startSession(projectID, c.backendID, dirID, "Another session")
	_ = receive(t, "other job", backend.starts)
	var foreign scheduleResponse
	c.mustDo("POST", "/api/v1/sessions/"+other+"/schedules", map[string]any{"cron": "0 8 * * *", "message": "other"}, &foreign, 201)
	fail("schedule_update", map[string]any{"scheduleId": foreign.ID, "enabled": false})
	fail("schedule_delete", map[string]any{"scheduleId": foreign.ID})
	fail("schedule_create", map[string]any{"sessionId": other, "cron": "0 8 * * *", "message": "wrong target"})
	temporary := call("schedule_create", map[string]any{"cron": "0 1 * * *", "message": "remove me", "enabled": false})["scheduleId"].(string)
	call("schedule_delete", map[string]any{"scheduleId": temporary})
	if list := call("schedule_list", nil)["schedules"].([]any); len(list) != 1 {
		t.Fatal(list)
	}
	for kind, count := range map[string]int{"schedule.created": 2, "schedule.updated": 2, "schedule.deleted": 1} {
		if got := c.countEvents(sessionID, kind); got != count {
			t.Fatalf("%s = %d, want %d", kind, got, count)
		}
	}
	var audit struct {
		Items []struct{ Action, ActorID, JobID, SubjectID, Channel string } `json:"items"`
	}
	c.mustDo("GET", "/api/v1/me/audit?projectId="+projectID, nil, &audit, 200)
	agentChanges := 0
	for _, entry := range audit.Items {
		if entry.SubjectID != id && entry.SubjectID != temporary {
			continue
		}
		if entry.ActorID != "agent" || entry.JobID != start.GetJobId() || entry.Channel != "agent" {
			t.Fatalf("audit attribution: %+v", entry)
		}
		agentChanges++
	}
	if agentChanges != 5 {
		t.Fatalf("agent changes = %d, want 5", agentChanges)
	}
	completeDelegatedJob(t, c, backend, start)
	due, err := time.Parse(time.RFC3339, resumed["nextRunAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	c.svc.RunDueSchedules(ctx, due.Add(10*time.Second))
	next := receive(t, "scheduled job after the originating job ended", backend.starts)
	if next.GetSessionId() != sessionID {
		t.Fatal(next)
	}
	var scheduleList struct {
		Items []scheduleResponse `json:"items"`
	}
	c.mustDo("GET", "/api/v1/sessions/"+sessionID+"/schedules", nil, &scheduleList, 200)
	if scheduleList.Items[0].LastOutcome != "SENT" {
		t.Fatal(scheduleList)
	}
	// A finished Job cannot keep mutating its session.
	fail("schedule_delete", map[string]any{"scheduleId": id})
}

func TestScheduleMutationsRollBackIfTheirHistoryCannotBeSaved(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	sessionID := c.startSession(projectID, c.backendID, dirID, "Manage schedules")
	start := receive(t, "job", backend.starts)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := c.db.Pool().Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	fail := func(name string, input map[string]any) {
		t.Helper()
		backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	exec(`ALTER TABLE events ADD CONSTRAINT reject_schedule CHECK (type <> 'schedule.created') NOT VALID`)
	fail("schedule_create", map[string]any{"cron": "0 8 * * *", "message": "create"})
	exec(`ALTER TABLE events DROP CONSTRAINT reject_schedule`)
	if got := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "schedule_list", nil)["schedules"].([]any); len(got) != 0 {
		t.Fatal("failed creation persisted", got)
	}
	created := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "schedule_create", map[string]any{"cron": "0 8 * * *", "message": "preserve"})
	id := created["scheduleId"].(string)
	exec(`ALTER TABLE audit_entries ADD CONSTRAINT reject_schedule CHECK (action <> 'schedule.updated') NOT VALID`)
	fail("schedule_update", map[string]any{"scheduleId": id, "enabled": false, "message": "discard"})
	exec(`ALTER TABLE audit_entries DROP CONSTRAINT reject_schedule`)
	exec(`ALTER TABLE events ADD CONSTRAINT reject_schedule CHECK (type <> 'schedule.deleted') NOT VALID`)
	fail("schedule_delete", map[string]any{"scheduleId": id})
	exec(`ALTER TABLE events DROP CONSTRAINT reject_schedule`)
	list := backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "schedule_list", nil)["schedules"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["message"] != "preserve" || list[0].(map[string]any)["enabled"] != true {
		t.Fatalf("failed edits did not roll back: %v", list)
	}
	for _, kind := range []string{"schedule.updated", "schedule.deleted"} {
		if c.countEvents(sessionID, kind) != 0 {
			t.Fatal("rolled-back event published", kind)
		}
	}
	var audit struct {
		Items []struct{ Action, SubjectID string } `json:"items"`
	}
	c.mustDo("GET", "/api/v1/me/audit?projectId="+projectID, nil, &audit, 200)
	changes := 0
	for _, entry := range audit.Items {
		if entry.Action == "schedule.created" || entry.Action == "schedule.updated" || entry.Action == "schedule.deleted" {
			changes++
			if entry.Action != "schedule.created" || entry.SubjectID != id {
				t.Fatal("rolled-back audit persisted", entry)
			}
		}
	}
	if changes != 1 {
		t.Fatalf("%d schedule audit entries, want 1", changes)
	}
}
