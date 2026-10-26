package api_test

import (
	"context"
	"encoding/json"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/monitor"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net/http"
	"sync"
	"testing"
	"time"
)

func reportOf(t *testing.T, v monitor.Snapshot) *backendv1.MonitorReport {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var portable map[string]any
	if err = json.Unmarshal(raw, &portable); err != nil {
		t.Fatal(err)
	}
	st, err := structpb.NewStruct(portable)
	if err != nil {
		t.Fatal(err)
	}
	return &backendv1.MonitorReport{Snapshot: st}
}
func TestConcurrentMonitorReportsQueueExactlyOneJob(t *testing.T) {
	c, b, p, d := setup(t)
	session := c.startSession(p, c.backendID, d, "Watch")
	start := receive(t, "original job", b.starts)
	v := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	v.State = "RUNNING"
	v.Sequence = 1
	v.Notify = true
	v.Output = "ready"
	report := reportOf(t, v.Snapshot)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.svc.MonitorReport(context.Background(), domain.BackendInstanceID(c.backendID), report)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = receive(t, "only monitor wake", b.starts)
	expectNothing(t, "a duplicate concurrent wake", b.starts)
	if got := c.countEvents(session, "user.message"); got != 2 {
		t.Fatalf("messages=%d", got)
	}
	stored, err := c.store.Monitor(context.Background(), v.ID, false)
	if err != nil || stored.WakeCount != 1 || stored.WakeSequence != 1 {
		t.Fatalf("wake state=%+v error=%v", stored, err)
	}
}
func TestMonitorWakeRollsBackWithItsMessageAndRetries(t *testing.T) {
	c, b, p, d := setup(t)
	session := c.startSession(p, c.backendID, d, "Watch")
	start := receive(t, "original job", b.starts)
	v := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	ctx := context.Background()
	_, err := c.db.Pool().Exec(ctx, `CREATE FUNCTION reject_monitor_wake() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='user.message' AND NEW.payload ? 'monitorId' THEN RAISE EXCEPTION 'test wake failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_monitor_wake BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION reject_monitor_wake();`)
	if err != nil {
		t.Fatal(err)
	}
	v.State = "COMPLETED"
	v.Sequence = 1
	v.Notify = true
	v.Output = "done"
	if _, err = c.svc.MonitorReport(ctx, domain.BackendInstanceID(c.backendID), reportOf(t, v.Snapshot)); err == nil {
		t.Fatal("failed message was acknowledged")
	}
	stored, err := c.store.Monitor(ctx, v.ID, false)
	if err != nil || stored.WakeCount != 0 || stored.WakeJobID != nil {
		t.Fatal("failed wake changed delivery state", stored, err)
	}
	if c.countEvents(session, "job.created") != 1 {
		t.Fatal("a Job survived the failed monitoring message")
	}
	if _, err = c.db.Pool().Exec(ctx, `DROP TRIGGER reject_monitor_wake ON events`); err != nil {
		t.Fatal(err)
	}
	// The backend may have restarted before retrying this terminal observation.
	// An empty inventory must still deliver Core's durable pending event.
	_, err = c.svc.MonitorReport(ctx, domain.BackendInstanceID(c.backendID), &backendv1.MonitorReport{InventoryComplete: true, ConnectedAt: timestamppb.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_ = receive(t, "recovered pending monitor wake", b.starts)
	if c.countEvents(session, "user.message") != 2 {
		t.Fatal("failed event was lost or duplicated")
	}
}
func TestMonitorInventoryDistinguishesOldMissingAndNewLiveWatches(t *testing.T) {
	c, b, p, d := setup(t)
	session := c.startSession(p, c.backendID, d, "Watch")
	start := receive(t, "original job", b.starts)
	missing := registerMonitor(t, c, b, start)
	live := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	missing.State = "RUNNING"
	missing.Sequence = 1
	sendMonitor(t, c, missing.Snapshot, c.backendID)
	live.State = "RUNNING"
	live.Sequence = 1
	sendMonitor(t, c, live.Snapshot, c.backendID)
	_, err := c.svc.MonitorReport(context.Background(), domain.BackendInstanceID(c.backendID), &backendv1.MonitorReport{InventoryComplete: true, MonitorIds: []string{live.ID}, ConnectedAt: timestamppb.Now()})
	if err != nil {
		t.Fatal(err)
	}
	wake := receive(t, "missing process notification", b.starts)
	values, err := c.svc.ListMonitors(context.Background(), auth.Identity{UserID: "thomas"}, session, "")
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, v := range values {
		states[v.ID] = v.State
	}
	if states[missing.ID] != "INTERRUPTED" || states[live.ID] != "RUNNING" {
		t.Fatalf("inventory states=%v", states)
	}
	completeDelegatedJob(t, c, b, wake)
	// An older connection cutoff cannot interrupt newer watches.
	_, err = c.svc.MonitorReport(context.Background(), domain.BackendInstanceID(c.backendID), &backendv1.MonitorReport{InventoryComplete: true, ConnectedAt: timestamppb.New(time.Now().Add(-time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	expectNothing(t, "an inventory wake for a newer watch", b.starts)
}
func TestMonitorPolicyChangeAndStaleObservationAreVisible(t *testing.T) {
	c, b, p, d := setup(t)
	session := c.startSession(p, c.backendID, d, "Watch")
	start := receive(t, "original job", b.starts)
	v := registerMonitor(t, c, b, start)
	completeDelegatedJob(t, c, b, start)
	v.State = "RUNNING"
	v.Sequence = 1
	sendMonitor(t, c, v.Snapshot, c.backendID)
	if _, err := c.db.Pool().Exec(context.Background(), `UPDATE monitors SET last_seen_at=now()-interval '45 seconds' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []monitor.View `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/monitors", nil, &list, http.StatusOK)
	if list.Items[0].Confirmed {
		t.Fatal("stale monitor claimed confirmed activity")
	}
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy", map[string]any{"mode": "GUARDED", "allowNetwork": false}, nil, http.StatusOK)
	if ctrl := sendMonitor(t, c, v.Snapshot, c.backendID); !ctrl.Stop {
		t.Fatal("monitor kept old permissions after policy changed")
	}
	expectNothing(t, "model call for a policy stop", b.starts)
}
