package api_test

import (
	"context"
	"encoding/json"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/monitor"
	"google.golang.org/protobuf/types/known/structpb"
	"net/http"
	"testing"
	"time"
)

func registerMonitor(t *testing.T, c *core, b *fakeBackend, start *backendv1.StartJob) monitor.View {
	t.Helper()
	ctx := context.Background()
	_, err := c.db.Pool().Exec(ctx, `UPDATE backend_instances SET features=array_append(features, 'PERSISTENT_MONITORS') WHERE id=$1`, c.backendID)
	if err != nil {
		t.Fatal(err)
	}
	result := b.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "monitor_register", map[string]any{"command": "watch-checks", "description": "Deployment checks", "timeoutSeconds": 60})
	raw, _ := json.Marshal(result["monitor"])
	var v monitor.View
	if err = json.Unmarshal(raw, &v); err != nil || v.ID == "" {
		t.Fatalf("registration=%v error=%v", result, err)
	}
	return v
}
func sendMonitor(t *testing.T, c *core, v monitor.Snapshot, backend string) *backendv1.MonitorControl {
	t.Helper()
	raw, _ := json.Marshal(v)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	st, _ := structpb.NewStruct(p)
	ctrl, err := c.svc.MonitorReport(context.Background(), domain.BackendInstanceID(backend), &backendv1.MonitorReport{Snapshot: st})
	if err != nil {
		t.Fatal(err)
	}
	return ctrl
}

func TestMonitorStartingIsVisibleWithoutWakingIdleSession(t *testing.T) {
	c, b, project, dir := setup(t)
	id := c.startSession(project, c.backendID, dir, "Watch")
	start := receive(t, "originating job", b.starts)
	v := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	v.State = "STARTING"
	v.Sequence = 1
	v.Notify = false
	v.UpdatedAt = time.Now().UTC()
	sendMonitor(t, c, v.Snapshot, c.backendID)
	var list struct {
		Items []monitor.View `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+id+"/monitors", nil, &list, http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].State != "STARTING" || !list.Items[0].Confirmed {
		t.Fatalf("scheduling monitor not visible: %+v", list)
	}
	expectNothing(t, "a model wake while scheduling", b.starts)
	v.State = "RUNNING"
	v.Sequence = 2
	sendMonitor(t, c, v.Snapshot, c.backendID)
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+id+"/monitors", nil, &list, http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].State != "RUNNING" {
		t.Fatal("monitor did not leave scheduling state")
	}
}
func TestMonitorSurvivesJobAndWakesSessionOnce(t *testing.T) {
	c, b, project, dir := setup(t)
	id := c.startSession(project, c.backendID, dir, "Watch the deployment")
	start := receive(t, "initial job", b.starts)
	v := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	v.State = "RUNNING"
	v.Sequence = 1
	v.Notify = false
	v.UpdatedAt = time.Now().UTC()
	sendMonitor(t, c, v.Snapshot, c.backendID)
	expectNothing(t, "a model call for idle monitoring", b.starts)
	var list struct {
		Items []monitor.View `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+id+"/monitors", nil, &list, http.StatusOK)
	if len(list.Items) != 1 || !list.Items[0].Confirmed || list.Items[0].State != "RUNNING" {
		t.Fatalf("idle monitors=%+v", list)
	}
	v.Sequence = 2
	v.Notify = true
	v.Output = "check completed"
	sendMonitor(t, c, v.Snapshot, c.backendID)
	wake := receive(t, "monitor wake", b.starts)
	if wake.SessionId != id {
		t.Fatal("wrong wake session")
	}
	sendMonitor(t, c, v.Snapshot, c.backendID)
	expectNothing(t, "duplicate wake on replay", b.starts)
	var snapshot snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+id, nil, &snapshot, http.StatusOK)
	found := false
	for _, e := range snapshot.Events {
		if e.Type == "user.message" {
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			if p["monitorId"] == v.ID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("monitor event lost provenance")
	}
	v.Sequence = 3
	v.Output = "next output"
	control := sendMonitor(t, c, v.Snapshot, c.backendID)
	if control.AckSequence != 0 {
		t.Fatal("coalesced event was acknowledged before delivery")
	}
	expectNothing(t, "parallel wake while first wake runs", b.starts)
	completeDelegatedJob(t, c, b, wake)
	sendMonitor(t, c, v.Snapshot, c.backendID)
	_ = receive(t, "coalesced next wake", b.starts)
	if c.countEvents(id, "monitor.changed") != 4 {
		t.Fatalf("monitor lifecycle replay duplicated: %d", c.countEvents(id, "monitor.changed"))
	}
}
func TestMonitorOwnershipScopeStopsAndReconnectReplay(t *testing.T) {
	c, b, project, dir := setup(t)
	id := c.startSession(project, c.backendID, dir, "Watch")
	start := receive(t, "initial job", b.starts)
	v := registerMonitor(t, c, b, start)
	other := c.asUser("foreign")
	other.mustDo(http.MethodGet, "/api/v1/sessions/"+id+"/monitors", nil, nil, http.StatusNotFound)
	other.mustDo(http.MethodPost, "/api/v1/monitors/"+v.ID+"/stop", nil, nil, http.StatusNotFound)
	unrelated := c.startSession(project, c.backendID, dir, "Other session")
	otherStart := receive(t, "other job", b.starts)
	b.callCoreToolExpectingFailure(t, context.Background(), otherStart.RunId, otherStart.JobId, "monitor_stop", map[string]any{"monitorId": v.ID})
	_ = unrelated
	completeDelegatedJob(t, c, b, start)
	v.State = "RUNNING"
	v.Sequence = 1
	raw, _ := json.Marshal(v.Snapshot)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	st, _ := structpb.NewStruct(p)
	if _, err := c.svc.MonitorReport(context.Background(), domain.BackendInstanceID(domain.NewUUID()), &backendv1.MonitorReport{Snapshot: st}); err == nil {
		t.Fatal("foreign backend reported monitor")
	}
	sendMonitor(t, c, v.Snapshot, c.backendID)
	// Stop is durable even when its backend cannot confirm immediately.
	c.mustDo(http.MethodPost, "/api/v1/monitors/"+v.ID+"/stop", map[string]any{}, nil, http.StatusAccepted)
	control := sendMonitor(t, c, v.Snapshot, c.backendID)
	if !control.Stop {
		t.Fatal("stop was lost across repeated reports")
	}
	v.State = "STOPPED"
	v.Sequence = 2
	v.Notify = false
	sendMonitor(t, c, v.Snapshot, c.backendID)
	expectNothing(t, "model wake for manual stop", b.starts)
	var list struct {
		Items []monitor.View `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/monitors", nil, &list, http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].State != "STOPPED" {
		t.Fatalf("terminal history=%+v", list)
	}
}
func TestMonitorTransportAndDeletedSessionStopsOrphan(t *testing.T) {
	c, b, project, dir := setup(t)
	id := c.startSession(project, c.backendID, dir, "Watch")
	start := receive(t, "initial job", b.starts)
	v := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	v.State = "RUNNING"
	v.Sequence = 1
	raw, _ := json.Marshal(v.Snapshot)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	st, _ := structpb.NewStruct(p)
	if err := b.sdk.SendMonitor(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "monitor report over real control stream", func() bool { return c.countEvents(id, "monitor.changed") == 2 })
	c.mustDo(http.MethodDelete, "/api/v1/sessions/"+id, nil, nil, http.StatusNoContent)
	if control := sendMonitor(t, c, v.Snapshot, c.backendID); !control.Stop {
		t.Fatal("orphan process not stopped")
	}
	v.State = "STOPPED"
	v.Sequence = 2
	if control := sendMonitor(t, c, v.Snapshot, c.backendID); control.AckSequence != 2 {
		t.Fatal("orphan terminal snapshot not acknowledged")
	}
}
