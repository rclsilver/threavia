package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/core/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recoveringProvider struct {
	authenticated atomic.Bool
	starts        chan runner.StartParams
	attempts      atomic.Int32
}

func (r *recoveringProvider) Available() error                  { return nil }
func (r *recoveringProvider) Cancel(string) error               { return runner.ErrUnknownJob }
func (r *recoveringProvider) Inject(string, string, bool) error { return runner.ErrUnknownJob }
func (r *recoveringProvider) Authenticated(context.Context) (bool, bool) {
	return r.authenticated.Load(), true
}
func (r *recoveringProvider) Run(ctx context.Context, p runner.StartParams, sink runner.Sink) error {
	r.starts <- p
	if err := sink.JobStarted(ctx, p.RunID, p.JobID); err != nil {
		return err
	}
	if r.attempts.Add(1) == 1 {
		r.authenticated.Store(false)
		return runner.ReportProviderBlock(ctx, sink, p.RunID, p.JobID, &runner.ProviderBlock{Reason: "AUTHENTICATION_REQUIRED", Message: "Run claude auth login on this backend.", ActiveSeconds: 1}, nil)
	}
	return sink.JobCompleted(ctx, p.RunID, p.JobID, "Recovered", nil)
}

func TestRealAdapterRecoversTheSameJobAfterLocalReauthentication(t *testing.T) {
	c := newCore(t)
	project := c.createProject("recovery")
	backendID, credential := c.registerBackend("recovery")
	r := &recoveringProvider{starts: make(chan runner.StartParams, 4)}
	r.authenticated.Store(true)
	a := c.connectBackendWith(r, credential, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := c.startSessionFrom(project, backendID, "web", "Continue the original assignment")
	first := receive(t, "real adapter first attempt", r.starts)
	waitUntil(t, "real adapter waiting", func() bool { return c.jobStatus(session, first.JobID) == "WAITING_BACKEND" })
	providerDue(t, c, first.JobID)
	c.svc.RecoverProviderJobs(ctx)
	expectNothing(t, "retry while authentication is invalid", r.starts)
	r.authenticated.Store(true)
	// Run the same probe normally performed on the minute tick/reconnection.
	if err := a.OnConnected(ctx, &backendv1.Welcome{ProviderRecovery: true}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "provider readiness persisted", func() bool {
		backend, err := c.store.BackendInstanceByID(ctx, domain.BackendInstanceID(backendID))
		return err == nil && backend.ProviderAuthState == domain.ProviderAuthenticated
	})
	c.svc.RecoverProviderJobs(ctx)
	second := receive(t, "real adapter second attempt", r.starts)
	if first.JobID != second.JobID || second.ActiveSecondsUsed != 1 {
		t.Fatal(first, second)
	}
	waitUntil(t, "real adapter completion", func() bool { return c.jobStatus(session, first.JobID) == "COMPLETED" })
	if c.countEvents(session, "job.failed") != 0 || c.countEvents(session, "job.waiting_backend") != 1 {
		t.Fatal("recovery became a failure or duplicated the wait")
	}
}

func providerStatus(c *core, reason string, ready bool) {
	auth := backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED
	status := backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY
	var conditions []*backendv1.Condition
	if !ready {
		status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
		kind := "ProviderQuotaAvailable"
		if reason == "AUTHENTICATION_REQUIRED" {
			kind = "ProviderAuthenticated"
			auth = backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED
		}
		conditions = []*backendv1.Condition{{Type: kind, Status: "False", Reason: reason, Message: "Provider requires recovery on the backend machine."}}
	}
	c.svc.StatusUpdate(context.Background(), domain.BackendInstanceID(c.backendID), &backendv1.StatusUpdate{Status: status, ProviderAuthState: auth, Conditions: conditions})
}

func providerDue(t *testing.T, c *core, jobID string) {
	t.Helper()
	at, _ := json.Marshal(time.Now().Add(-time.Second))
	_, err := c.db.Pool().Exec(context.Background(), `UPDATE jobs SET backend_wait=jsonb_set(backend_wait,'{retryAt}',$2::jsonb) WHERE id=$1`, jobID, at)
	if err != nil {
		t.Fatal(err)
	}
}

func emitProviderWait(t *testing.T, c *core, backend *fakeBackend, start *backendv1.StartJob, reason string) *backendv1.JobEvent {
	t.Helper()
	ctx := context.Background()
	wait, err := backend.events.JobWaitingBackend(ctx, start.GetRunId(), start.GetJobId(), &backendv1.JobWaitingBackend{Reason: reason, Message: "Recover the provider locally.", RetryAt: timestamppb.New(time.Now().Add(time.Hour)), ActiveSeconds: 2, Usage: &backendv1.Usage{InputTokens: 7}})
	if err != nil {
		t.Fatal(err)
	}
	backend.emit(t, ctx, wait)
	waitUntil(t, "provider wait", func() bool { return c.jobStatus(start.GetSessionId(), start.GetJobId()) == "WAITING_BACKEND" })
	return wait
}

func TestProviderRecoveryPreservesJobQueueModelBudgetAndUsage(t *testing.T) {
	for _, reason := range []string{"AUTHENTICATION_REQUIRED", "QUOTA_EXCEEDED", "RATE_LIMITED"} {
		t.Run(reason, func(t *testing.T) {
			c, backend, project, dir := setup(t)
			ctx := context.Background()
			session := c.startSession(project, c.backendID, dir, "Original task")
			start := receive(t, "first attempt", backend.starts)
			began, _ := backend.events.JobStarted(ctx, start.GetRunId(), start.GetJobId())
			began.GetJobStarted().Model = "original-model"
			began.GetJobStarted().Effort = "low"
			backend.emit(t, ctx, began)
			bound, _ := backend.events.NativeSessionBound(ctx, start.GetRunId(), start.GetJobId(), "native-recovery")
			backend.emit(t, ctx, bound)
			tool, _ := backend.events.ToolStarted(ctx, start.GetRunId(), start.GetJobId(), "tool-1", "Read", nil)
			backend.emit(t, ctx, tool)
			frames, closeStream := c.openStream(t, "")
			defer closeStream()
			providerStatus(c, reason, false)
			wait := emitProviderWait(t, c, backend, start, reason)
			frame := awaitFrame(t, frames, "job.waiting_backend")
			if _, err := c.svc.JobEvent(ctx, domain.BackendInstanceID(c.backendID), wait); err != nil {
				t.Fatal(err)
			}
			if c.countEvents(session, "job.waiting_backend") != 1 {
				t.Fatal("duplicate wait event")
			}
			job, err := c.store.GetJob(ctx, "thomas", domain.JobID(start.GetJobId()))
			if err != nil || job.BackendWait == nil || job.BackendWait.Reason != reason || job.EndedAt != nil || job.Error != nil {
				t.Fatal(job, err)
			}
			var queued struct {
				ID     string
				Status string
			}
			c.mustDo("POST", "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Next independent job"}, &queued, 201)
			if queued.Status != "QUEUED" {
				t.Fatal(queued)
			}
			// A persisted next-job preference must not change this retry's model.
			if _, err := c.store.SetSessionModel(ctx, "thomas", domain.SessionID(session), "next-job-model", "high"); err != nil {
				t.Fatal(err)
			}
			c.svc.RecoverProviderJobs(ctx)
			expectNothing(t, "dispatch before provider recovery", backend.starts)
			providerDue(t, c, start.GetJobId())
			c.svc.RecoverProviderJobs(ctx)
			expectNothing(t, "dispatch with provider still blocked", backend.starts)
			// Reconciliation of a stopped attempt must not settle it as FAILED.
			c.svc.ReconcileState(ctx, domain.BackendInstanceID(c.backendID), &backendv1.ReconcileState{Runs: []*backendv1.RunState{{RunId: start.GetRunId(), Jobs: []*backendv1.JobState{{JobId: start.GetJobId(), Status: backendv1.JobStatus_JOB_STATUS_WAITING_BACKEND, LastBackendSequence: wait.BackendSequence}}}}})
			providerStatus(c, reason, true)
			c.svc.RecoverProviderJobs(ctx)
			again := receive(t, "resumed attempt", backend.starts)
			if again.JobId != start.JobId || again.NativeSessionId != "native-recovery" || again.Model != "original-model" || again.Effort != "low" || again.ActionsUsed != 1 || again.ActiveSecondsUsed != 2 || !strings.Contains(again.Prompt, "Original task") || !strings.Contains(again.Prompt, "avoid repeating actions") {
				t.Fatalf("recovery changed the assignment: %v", again)
			}
			expectNothing(t, "duplicate concurrent retry", backend.starts)
			resumed, _ := backend.events.JobStarted(ctx, start.GetRunId(), start.GetJobId())
			backend.emit(t, ctx, resumed)
			waitUntil(t, "wait cleared", func() bool {
				job, _ := c.store.GetJob(ctx, "thomas", domain.JobID(start.JobId))
				return job.Status == domain.JobRunning && job.BackendWait == nil
			})
			// Remove the intentionally unsupported next-job model for the queue.
			_, _ = c.store.SetSessionModel(ctx, "thomas", domain.SessionID(session), "", "")
			done, _ := backend.events.JobCompleted(ctx, start.RunId, start.JobId, "done", &backendv1.Usage{OutputTokens: 3})
			backend.emit(t, ctx, done)
			waitUntil(t, "recovered job completed", func() bool { return c.jobStatus(session, start.JobId) == "COMPLETED" })
			if next := receive(t, "next queued job", backend.starts); next.JobId != queued.ID {
				t.Fatal(next)
			}
			var snapshot struct {
				Events []struct {
					Type    string
					Payload struct {
						Usage struct{ InputTokens, OutputTokens uint64 }
					}
				}
			}
			c.mustDo("GET", "/api/v1/sessions/"+session, nil, &snapshot, 200)
			for _, event := range snapshot.Events {
				if event.Type == "job.completed" && (event.Payload.Usage.InputTokens != 7 || event.Payload.Usage.OutputTokens != 3) {
					t.Fatal("lost earlier attempt usage", event)
				}
			}
			replay, closeReplay := c.openStream(t, fmt.Sprint(frame.event.Sequence-1))
			defer closeReplay()
			if got := awaitFrame(t, replay, "job.waiting_backend"); got.id != frame.id {
				t.Fatal("wait event cannot replay")
			}
		})
	}
}

func TestWaitingProviderJobCanBeCancelledWithoutAProcess(t *testing.T) {
	c, backend, project, dir := setup(t)
	session := c.startSession(project, c.backendID, dir, "Wait")
	start := receive(t, "attempt", backend.starts)
	providerStatus(c, "QUOTA_EXCEEDED", false)
	emitProviderWait(t, c, backend, start, "QUOTA_EXCEEDED")
	c.mustDo("POST", "/api/v1/jobs/"+start.JobId+"/cancel", map[string]any{}, nil, 200)
	if got := c.jobStatus(session, start.JobId); got != "CANCELLED" {
		t.Fatal(got)
	}
	var snapshot struct {
		Events []struct {
			Type    string
			Payload struct {
				Usage struct {
					InputTokens uint64 `json:"inputTokens"`
				} `json:"usage"`
			}
		}
	}
	c.mustDo("GET", "/api/v1/sessions/"+session, nil, &snapshot, 200)
	for _, event := range snapshot.Events {
		if event.Type == "job.cancelled" && event.Payload.Usage.InputTokens != 7 {
			t.Fatal("cancel lost earlier attempt usage")
		}
	}
	expectNothing(t, "unnecessary provider cancellation", backend.cancels)
	providerStatus(c, "", true)
	providerDue(t, c, start.JobId)
	c.svc.RecoverProviderJobs(context.Background())
	expectNothing(t, "retry after cancellation", backend.starts)
}

func TestProviderWaitRollsBackAndCannotCrossBackendOwnership(t *testing.T) {
	c, backend, project, dir := setup(t)
	ctx := context.Background()
	session := c.startSession(project, c.backendID, dir, "Keep running")
	start := receive(t, "attempt", backend.starts)
	wait, _ := backend.events.JobWaitingBackend(ctx, start.RunId, start.JobId, &backendv1.JobWaitingBackend{Reason: "AUTHENTICATION_REQUIRED", Message: "login", ActiveSeconds: 3})
	if _, err := c.svc.JobEvent(ctx, domain.BackendInstanceID(domain.NewUUID()), wait); err == nil {
		t.Fatal("foreign backend changed the job")
	}
	if _, err := c.db.Pool().Exec(ctx, `ALTER TABLE events ADD CONSTRAINT reject_provider_wait CHECK(type <> 'job.waiting_backend') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.JobEvent(ctx, domain.BackendInstanceID(c.backendID), wait); err == nil {
		t.Fatal("wait with missing history accepted")
	}
	job, err := c.store.GetJob(ctx, "thomas", domain.JobID(start.JobId))
	if err != nil || job.Status != domain.JobRunning || job.BackendWait != nil {
		t.Fatal(job, err)
	}
	if c.countEvents(session, "job.waiting_backend") != 0 {
		t.Fatal("uncommitted wait persisted")
	}
}

func TestKnownProviderAuthenticationParksNewWorkUntilReady(t *testing.T) {
	c, backend, project, dir := setup(t)
	ctx := context.Background()
	providerStatus(c, "AUTHENTICATION_REQUIRED", false)
	session := c.startSession(project, c.backendID, dir, "Start after login")
	var snapshot struct{ Jobs []domain.Job }
	c.mustDo("GET", "/api/v1/sessions/"+session, nil, &snapshot, 200)
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].Status != domain.JobWaitingBackend || snapshot.Jobs[0].BackendWait == nil {
		t.Fatal(snapshot)
	}
	expectNothing(t, "inference before authentication", backend.starts)
	providerDue(t, c, string(snapshot.Jobs[0].ID))
	providerStatus(c, "", true)
	c.svc.RecoverProviderJobs(ctx)
	if start := receive(t, "first attempt after login", backend.starts); start.NativeSessionId != "" || start.Prompt != "Start after login" {
		t.Fatal(start)
	}
}

func TestProviderFailureMessagesArePersistedAsStructuredWaitingState(t *testing.T) {
	c, backend, project, dir := setup(t)
	session := c.startSession(project, c.backendID, dir, "Observe recovery")
	start := receive(t, "attempt", backend.starts)
	emitProviderWait(t, c, backend, start, "RATE_LIMITED")
	var snapshot map[string]any
	c.mustDo("GET", "/api/v1/sessions/"+session, nil, &snapshot, 200)
	encoded, _ := json.Marshal(snapshot["jobs"])
	if !strings.Contains(string(encoded), `"backendWait"`) || !strings.Contains(string(encoded), `"retryAt"`) {
		t.Fatal(string(encoded))
	}
}
