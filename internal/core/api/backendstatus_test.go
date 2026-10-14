package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
)

func backendStatusEvents(t *testing.T, c *core) []events.Envelope {
	t.Helper()
	all, err := c.store.EventsAfter(context.Background(), "thomas", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found []events.Envelope
	for _, event := range all {
		if event.Type == events.TypeBackendStatusChanged {
			found = append(found, event)
		}
	}
	return found
}

func TestBackendStatusReachesBothClientStreamsAndReplays(t *testing.T) {
	c := newCore(t)
	id, credential := c.registerBackend("status-backend")
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	stranger, closeStranger := c.asUser("stranger").openStream(t, "")
	defer closeStranger()

	backend := c.connectBackend(credential)
	starting := awaitFrame(t, web, "backend.status_changed")
	if got := awaitFrame(t, editor, "backend.status_changed"); got.event.Sequence != starting.event.Sequence {
		t.Fatal("clients received different connection events")
	}
	var payload struct{ BackendInstanceID, OperationalStatus, ProviderAuthState string }
	if err := json.Unmarshal(starting.event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.BackendInstanceID != id || payload.OperationalStatus != "STARTING" {
		t.Fatalf("invalid connection state: %+v", payload)
	}
	if err := backend.sdk.SendStatus(context.Background(),
		backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED,
		backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED,
		&backendv1.Condition{Type: "ProviderAuthenticated", Status: "False", Reason: "LoginRequired", Message: "Sign in on the backend."}); err != nil {
		t.Fatal(err)
	}
	degraded := awaitFrame(t, web, "backend.status_changed")
	if got := awaitFrame(t, editor, "backend.status_changed"); got.event.Sequence != degraded.event.Sequence {
		t.Fatal("clients received different status events")
	}
	var current domain.BackendInstance
	c.mustDo(http.MethodGet, "/api/v1/backends/"+id, nil, &current, http.StatusOK)
	if current.OperationalStatus != domain.BackendDegraded || len(current.Conditions) != 1 || current.Conditions[0].Reason != "LoginRequired" {
		t.Fatalf("event preceded its state: %+v", current)
	}
	replay, closeReplay := c.openStream(t, strconv.FormatInt(starting.event.Sequence, 10))
	defer closeReplay()
	if got := awaitFrame(t, replay, "backend.status_changed"); got.event.Sequence != degraded.event.Sequence {
		t.Fatal("a reconnecting client missed the status change")
	}
	expectNothing(t, "another owner's backend event", stranger)
}

func TestBackendReportsDeduplicateAndRollBackWithTheirEvent(t *testing.T) {
	c := newCore(t)
	id, _ := c.registerBackend("reports")
	ctx := context.Background()
	instanceID := domain.BackendInstanceID(id)
	if err := c.svc.Connected(ctx, instanceID, "lease", &backendv1.Hello{Capacity: &backendv1.Capacity{MaxConcurrentRuns: 1}}); err != nil {
		t.Fatal(err)
	}
	report := &backendv1.StatusUpdate{
		Status:            backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY,
		ProviderAuthState: backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED,
		Capacity:          &backendv1.Capacity{MaxConcurrentRuns: 1},
		Conditions:        []*backendv1.Condition{{Type: "ProviderAuthenticated", Status: "True", Message: "Signed in"}},
	}
	c.svc.StatusUpdate(ctx, instanceID, report)
	c.svc.StatusUpdate(ctx, instanceID, report)
	c.svc.Heartbeat(ctx, instanceID, time.Now(), report.Capacity)
	c.svc.Heartbeat(ctx, instanceID, time.Now().Add(time.Second), report.Capacity)
	if got := len(backendStatusEvents(t, c)); got != 2 {
		t.Fatalf("%d events, want connection and one report", got)
	}
	report.Conditions[0].Message = "Signed in with the renewed account"
	c.svc.StatusUpdate(ctx, instanceID, report)
	if got := len(backendStatusEvents(t, c)); got != 3 {
		t.Fatalf("condition-only change produced %d events, want 3", got)
	}
	if _, err := c.db.Pool().Exec(ctx, `ALTER TABLE events ADD CONSTRAINT reject_status_test CHECK (type <> 'backend.status_changed') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	report.Status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
	report.ProviderAuthState = backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED
	report.Conditions = nil
	c.svc.StatusUpdate(ctx, instanceID, report)
	var current domain.BackendInstance
	c.mustDo(http.MethodGet, "/api/v1/backends/"+id, nil, &current, http.StatusOK)
	if current.OperationalStatus != domain.BackendReady || current.ProviderAuthState != domain.ProviderAuthenticated || len(current.Conditions) != 1 {
		t.Fatalf("failed event did not roll back its state and conditions: %+v", current)
	}
	if got := len(backendStatusEvents(t, c)); got != 3 {
		t.Fatalf("failed report created an event: %d", got)
	}
}

func TestSupersededDisconnectDoesNotParkReplacementWork(t *testing.T) {
	c, backend, project, directory := setup(t)
	session := c.startSession(project, c.backendID, directory, "Keep working")
	job := receive(t, "the start", backend.starts)
	ctx := context.Background()
	id := backendInstance(t, c)
	before, err := c.store.BackendInstanceByID(ctx, id)
	if err != nil || before.ConnectionID == nil {
		t.Fatalf("missing lease: %v", err)
	}
	status := c.jobStatus(session, job.GetJobId())
	if err := c.svc.Connected(ctx, id, "replacement", &backendv1.Hello{Capacity: &backendv1.Capacity{MaxConcurrentRuns: 1}}); err != nil {
		t.Fatal(err)
	}
	count := len(backendStatusEvents(t, c))
	c.svc.Disconnected(ctx, id, *before.ConnectionID)
	if c.jobStatus(session, job.GetJobId()) != status || len(backendStatusEvents(t, c)) != count {
		t.Fatal("superseded disconnect parked work or emitted an event")
	}
	c.svc.Disconnected(ctx, id, "replacement")
	if c.jobStatus(session, job.GetJobId()) != "WAITING_BACKEND" {
		t.Fatal("current disconnect did not park work")
	}
	all := backendStatusEvents(t, c)
	if len(all) != count+1 {
		t.Fatalf("disconnect emitted %d new events, want 1", len(all)-count)
	}
	var payload map[string]string
	if err := json.Unmarshal(all[len(all)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["operationalStatus"] != "OFFLINE" {
		t.Fatalf("disconnect payload: %v", payload)
	}
}
