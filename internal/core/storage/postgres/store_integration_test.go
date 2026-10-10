package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// TestOwnershipIsolation pins that a user never sees another user's Project, and
// that an identifier they do not own is indistinguishable from one that does not
// exist.
func TestOwnershipIsolation(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	stranger := domain.UserID("stranger")
	if _, err := store.GetProject(ctx, stranger, f.project.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("reading another user's project: got %v, want ErrNotFound", err)
	}
	if _, err := store.GetSession(ctx, stranger, f.session.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("reading another user's session: got %v, want ErrNotFound", err)
	}
	if _, err := store.GetJob(ctx, stranger, f.job.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("reading another user's job: got %v, want ErrNotFound", err)
	}

	projects, err := store.ListProjects(ctx, stranger, true)
	if err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("a stranger sees %d projects, want 0", len(projects))
	}
}

// TestIdempotencyKeysArePerOwner pins that a key is unique for one person
// only: two people may send the same one, each finds only their own Job, and
// the same person cannot use it twice.
func TestIdempotencyKeysArePerOwner(t *testing.T) {
	store, ctx := newTestStore(t)
	mine := newFixture(t, store, ctx)
	theirs := newFixture(t, store, ctx)
	key := domain.NewUUID()

	first := domain.Job{ID: domain.NewJobID(), RunID: mine.run.ID, Status: domain.JobQueued, IdempotencyKey: &key}
	if err := store.CreateJob(ctx, &first); err != nil {
		t.Fatalf("creating the first keyed job: %v", err)
	}
	second := domain.Job{ID: domain.NewJobID(), RunID: theirs.run.ID, Status: domain.JobQueued, IdempotencyKey: &key}
	if err := store.CreateJob(ctx, &second); err != nil {
		t.Fatalf("another owner reusing the key: %v", err)
	}

	for owner, want := range map[domain.UserID]domain.JobID{mine.owner: first.ID, theirs.owner: second.ID} {
		found, err := store.JobByIdempotencyKey(ctx, owner, key)
		if err != nil || found.ID != want {
			t.Fatalf("the key of %s = %s (%v), want %s", owner, found.ID, err, want)
		}
	}
	if _, err := store.JobByIdempotencyKey(ctx, "stranger", key); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("a stranger looking the key up: got %v, want ErrNotFound", err)
	}

	again := domain.Job{ID: domain.NewJobID(), RunID: mine.run.ID, Status: domain.JobQueued, IdempotencyKey: &key}
	if err := store.CreateJob(ctx, &again); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("the same owner reusing the key: got %v, want ErrConflict", err)
	}
}

// TestOneActiveJobPerRun pins the invariant of specification section 3.4 at the
// storage level: queued Jobs may pile up, a second active one may not.
func TestOneActiveJobPerRun(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatalf("starting the first job: %v", err)
	}

	second := domain.Job{ID: domain.NewJobID(), RunID: f.run.ID, Status: domain.JobQueued}
	if err := store.CreateJob(ctx, &second); err != nil {
		t.Fatalf("queueing a second job: %v", err)
	}
	third := domain.Job{ID: domain.NewJobID(), RunID: f.run.ID, Status: domain.JobQueued}
	if err := store.CreateJob(ctx, &third); err != nil {
		t.Fatalf("queueing a third job: %v", err)
	}

	if _, err := store.TransitionJob(ctx, second.ID, domain.JobQueued, domain.JobRunning, nil); !errors.Is(err, postgres.ErrJobSlotTaken) {
		t.Fatalf("starting a second active job: got %v, want ErrJobSlotTaken", err)
	}

	// Queued Jobs are FIFO.
	next, err := store.NextQueuedJob(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("reading the next queued job: %v", err)
	}
	if next.ID != second.ID {
		t.Fatalf("next queued job = %s, want the oldest one %s", next.ID, second.ID)
	}

	// Once the active Job ends, the slot frees up.
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobRunning, domain.JobCompleted, nil); err != nil {
		t.Fatalf("completing the first job: %v", err)
	}
	if _, err := store.TransitionJob(ctx, second.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatalf("starting the next job: %v", err)
	}
}

// TestListedSessionsCarryTheJobHoldingThem pins what a sidebar reads to say
// which Session is working: the status of the oldest Job not yet finished, and
// nothing once every Job has ended.
func TestListedSessionsCarryTheJobHoldingThem(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	activeStatus := func() *domain.JobStatus {
		t.Helper()
		sessions, err := store.ListSessions(ctx, f.owner, f.project.ID, false)
		if err != nil {
			t.Fatalf("listing sessions: %v", err)
		}
		if len(sessions) != 1 {
			t.Fatalf("listed %d sessions, want 1", len(sessions))
		}
		return sessions[0].ActiveJobStatus
	}

	if status := activeStatus(); status == nil || *status != domain.JobQueued {
		t.Fatalf("with a queued job: active status = %v, want QUEUED", status)
	}

	queued := domain.Job{ID: domain.NewJobID(), RunID: f.run.ID, Status: domain.JobQueued}
	if err := store.CreateJob(ctx, &queued); err != nil {
		t.Fatalf("queueing a second job: %v", err)
	}
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatalf("starting the first job: %v", err)
	}
	if status := activeStatus(); status == nil || *status != domain.JobRunning {
		t.Fatalf("with a job running and one queued: active status = %v, want RUNNING", status)
	}

	for _, id := range []domain.JobID{f.job.ID, queued.ID} {
		from := domain.JobRunning
		if id == queued.ID {
			from = domain.JobQueued
		}
		if _, err := store.TransitionJob(ctx, id, from, domain.JobCancelled, nil); err != nil {
			t.Fatalf("ending job %s: %v", id, err)
		}
	}
	if status := activeStatus(); status != nil {
		t.Fatalf("with every job ended: active status = %s, want none", *status)
	}
}

// TestTransitionJobIsIdempotent pins that a replayed transition is a no-op
// rather than a corruption: the row only moves from the expected status.
func TestTransitionJobIsIdempotent(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatalf("starting the job: %v", err)
	}
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobRunning, nil); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("replaying the transition: got %v, want ErrNotFound", err)
	}

	job, err := store.JobByID(ctx, f.job.ID)
	if err != nil {
		t.Fatalf("reading the job: %v", err)
	}
	if job.Status != domain.JobRunning {
		t.Fatalf("job status = %s, want RUNNING", job.Status)
	}
	if job.StartedAt == nil {
		t.Error("starting a job must stamp startedAt")
	}
}

// TestBackendEventDeduplication pins that at-least-once backend delivery becomes
// exactly-once Core observation.
func TestBackendEventDeduplication(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	record := func() *events.Record {
		return &events.Record{
			Envelope: events.Envelope{
				Type:      events.TypeAgentMessage,
				ProjectID: &f.project.ID,
				SessionID: &f.session.ID,
				RunID:     &f.run.ID,
				JobID:     &f.job.ID,
				Payload:   json.RawMessage(`{"text":"bonjour"}`),
			},
			Origin: &events.BackendOrigin{
				BackendInstanceID: f.backend.ID,
				BackendEventID:    "evt-1",
				BackendSequence:   1,
			},
		}
	}

	first := record()
	if err := store.AppendEvent(ctx, first); err != nil {
		t.Fatalf("appending the event: %v", err)
	}
	if first.Sequence == 0 {
		t.Fatal("a persisted event must receive a global sequence")
	}

	if err := store.AppendEvent(ctx, record()); !errors.Is(err, postgres.ErrDuplicateEvent) {
		t.Fatalf("replaying the event: got %v, want ErrDuplicateEvent", err)
	}

	timeline, err := store.SessionEvents(ctx, f.owner, f.session.ID, 0, 50)
	if err != nil {
		t.Fatalf("reading the timeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("the timeline holds %d events, want 1: a replay must not duplicate history", len(timeline))
	}
}

// TestGlobalSequenceIsTheClientCursor pins the catch-up behaviour behind the SSE
// stream: a client reconnecting after a sequence receives exactly what it
// missed, in order.
func TestGlobalSequenceIsTheClientCursor(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	var sequences []domain.Sequence
	for i := range 5 {
		record := &events.Record{Envelope: events.Envelope{
			Type:      events.TypeAgentMessage,
			ProjectID: &f.project.ID,
			SessionID: &f.session.ID,
			Payload:   json.RawMessage(`{}`),
		}}
		if err := store.AppendEvent(ctx, record); err != nil {
			t.Fatalf("appending event %d: %v", i, err)
		}
		sequences = append(sequences, record.Sequence)
	}

	for i := 1; i < len(sequences); i++ {
		if sequences[i] <= sequences[i-1] {
			t.Fatalf("global sequence is not monotonic: %v", sequences)
		}
	}

	missed, err := store.EventsAfter(ctx, f.owner, sequences[1], 50)
	if err != nil {
		t.Fatalf("catching up: %v", err)
	}
	if len(missed) != 3 {
		t.Fatalf("catch-up returned %d events, want 3", len(missed))
	}
	if missed[0].Sequence != sequences[2] {
		t.Fatalf("catch-up starts at %d, want %d", missed[0].Sequence, sequences[2])
	}

	head, err := store.LatestSequence(ctx)
	if err != nil {
		t.Fatalf("reading the head sequence: %v", err)
	}
	if head != sequences[len(sequences)-1] {
		t.Fatalf("head sequence = %d, want %d", head, sequences[len(sequences)-1])
	}
}

// TestValidationResolutionIsAtomic pins that the first valid response wins and
// that a second client resolving the same request changes nothing.
func TestValidationResolutionIsAtomic(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	request := domain.ValidationRequest{
		ID: domain.NewValidationRequestID(),
		Scope: domain.Scope{
			ProjectID: f.project.ID, SessionID: f.session.ID,
			RunID: f.run.ID, JobID: f.job.ID,
		},
		BackendRequestID: "req-1",
		Title:            "Write roles/foo/tasks/main.yml",
		RequestPayload:   json.RawMessage(`{"tool":"Write"}`),
		PayloadSHA256:    "abc123",
	}
	if err := store.CreateValidationRequest(ctx, &request); err != nil {
		t.Fatalf("creating the validation request: %v", err)
	}

	// A replayed backend event resolves to the same request, never a second
	// prompt.
	replay := request
	replay.ID = domain.NewValidationRequestID()
	if err := store.CreateValidationRequest(ctx, &replay); err != nil {
		t.Fatalf("replaying the validation request: %v", err)
	}
	if replay.ID != request.ID {
		t.Fatalf("a replay created a second request %s, want %s", replay.ID, request.ID)
	}

	pending, err := store.PendingValidations(ctx, f.owner, f.session.ID)
	if err != nil {
		t.Fatalf("listing pending validations: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("%d pending validations, want 1", len(pending))
	}

	resolved, err := store.ResolveValidationRequest(ctx, request.ID, true, f.owner, "web", "")
	if err != nil {
		t.Fatalf("resolving the request: %v", err)
	}
	if resolved.Approved == nil || !*resolved.Approved {
		t.Fatal("the request must be recorded as approved")
	}

	if _, err := store.ResolveValidationRequest(ctx, request.ID, false, f.owner, "android", ""); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("a second resolution: got %v, want ErrNotFound", err)
	}

	pending, err = store.PendingValidations(ctx, f.owner, "")
	if err != nil {
		t.Fatalf("listing pending validations: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("%d validations still pending after resolution, want 0", len(pending))
	}
}

// TestJobContextResolvesTheBackendPath pins that the same logical directory
// resolves to the physical path of the backend that will execute the Job.
func TestJobContextResolvesTheBackendPath(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	dir := domain.KnownDirectory{
		ID: domain.NewKnownDirectoryID(), ProjectID: f.project.ID,
		Name: "puppet", Description: "Configuration Puppet du homelab",
	}
	if err := store.CreateKnownDirectory(ctx, &dir); err != nil {
		t.Fatalf("creating the known directory: %v", err)
	}

	other := domain.BackendInstance{
		ID: domain.NewBackendInstanceID(), OwnerID: &f.owner, Name: "work-laptop",
		OwnershipStatus: domain.BackendClaimed,
		Capabilities:    []domain.Capability{domain.CapabilityCode},
		Capacity:        domain.Capacity{MaxConcurrentRuns: 1},
	}
	if err := store.CreateBackendInstance(ctx, &other, "hash-"+domain.NewUUID(), nil, nil); err != nil {
		t.Fatalf("creating the second backend: %v", err)
	}

	for instance, path := range map[domain.BackendInstanceID]string{
		f.backend.ID: "/home/thomas/git/puppet",
		other.ID:     "/work/src/puppet",
	} {
		binding := domain.KnownDirectoryBinding{KnownDirectoryID: dir.ID, BackendInstanceID: instance, Path: path}
		if err := store.BindKnownDirectory(ctx, &binding); err != nil {
			t.Fatalf("binding the directory: %v", err)
		}
	}

	dirID := dir.ID
	if _, err := store.SetSessionWorkingDirectory(ctx, f.owner, f.session.ID, &dirID); err != nil {
		t.Fatalf("setting the session working directory: %v", err)
	}

	jc, err := store.LoadJobContext(ctx, f.job.ID)
	if err != nil {
		t.Fatalf("loading the job context: %v", err)
	}
	if jc.WorkingDirectoryPath == nil || *jc.WorkingDirectoryPath != "/home/thomas/git/puppet" {
		t.Fatalf("working directory = %v, want the path bound on the executing backend", jc.WorkingDirectoryPath)
	}
	if jc.ProjectID != f.project.ID || jc.OwnerID != f.owner {
		t.Fatalf("job context resolved to the wrong project: %+v", jc)
	}

	// The same directory on the other backend is a different path.
	path, err := store.ResolveBinding(ctx, dir.ID, other.ID)
	if err != nil {
		t.Fatalf("resolving the other binding: %v", err)
	}
	if path != "/work/src/puppet" {
		t.Fatalf("other binding = %q, want /work/src/puppet", path)
	}
}

// TestDeletingAProjectKeepsTheBackend pins specification section 21:
// BackendInstances are user-owned and survive the deletion of a Project.
func TestDeletingAProjectKeepsTheBackend(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	if err := store.DeleteProject(ctx, f.owner, f.project.ID); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("queued work must prevent deletion: %v", err)
	}
	if _, err := store.GetSession(ctx, f.owner, f.session.ID); err != nil {
		t.Fatalf("refused deletion must keep the session: %v", err)
	}
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobCancelled, nil); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteProject(ctx, f.owner, f.project.ID); err != nil {
		t.Fatalf("deleting the project: %v", err)
	}
	if _, err := store.GetSession(ctx, f.owner, f.session.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("the session must be gone: got %v", err)
	}
	if _, err := store.GetBackendInstance(ctx, f.owner, f.backend.ID); err != nil {
		t.Fatalf("the backend instance must survive: %v", err)
	}
}

// TestStartSessionIsAtomic pins that the first send creates everything or
// nothing, so no empty or partially-created Session can exist.
func TestStartSessionIsAtomic(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	sessionID := domain.NewSessionID()
	wanted := errors.New("backend refused")

	err := store.WithTx(ctx, func(tx *postgres.Store) error {
		session := domain.Session{ID: sessionID, ProjectID: f.project.ID, Title: "Draft", Status: domain.SessionActive}
		if err := tx.CreateSession(ctx, &session); err != nil {
			return err
		}
		run := domain.Run{ID: domain.NewRunID(), SessionID: sessionID, BackendInstanceID: f.backend.ID, ResumeStatus: domain.ResumeUnknown}
		if err := tx.CreateRun(ctx, &run); err != nil {
			return err
		}
		return wanted
	})
	if !errors.Is(err, wanted) {
		t.Fatalf("got %v, want the inner error", err)
	}

	if _, err := store.GetSession(ctx, f.owner, sessionID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("a failed first send must leave no session behind: got %v", err)
	}
}

// TestDisconnectionIsScopedToItsConnection pins the lease rule at the storage
// level: when a backend reconnects, the cleanup of the connection it replaced
// must not mark the instance OFFLINE, or Core would believe a live backend is
// gone and stop dispatching to it.
func TestDisconnectionIsScopedToItsConnection(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	connect := func(connectionID string) {
		t.Helper()
		if err := store.MarkBackendConnected(ctx, f.backend.ID, connectionID, 1,
			[]domain.Capability{domain.CapabilityCode}, 1, "go", "test", "claude", "test",
			[]domain.Feature{domain.FeatureJobInputNext}); err != nil {
			t.Fatalf("marking the backend connected: %v", err)
		}
	}

	connect("connection-1")
	connect("connection-2")

	// The superseded connection cleans up late.
	if err := store.MarkBackendDisconnected(ctx, f.backend.ID, "connection-1"); err != nil {
		t.Fatalf("releasing the superseded connection: %v", err)
	}

	instance, err := store.GetBackendInstance(ctx, f.owner, f.backend.ID)
	if err != nil {
		t.Fatalf("reading the backend instance: %v", err)
	}
	if instance.OperationalStatus == domain.BackendOffline {
		t.Fatal("a stale connection's cleanup must not take a live backend offline")
	}

	// The connection that actually holds the lease does take it offline.
	if err := store.MarkBackendDisconnected(ctx, f.backend.ID, "connection-2"); err != nil {
		t.Fatalf("releasing the active connection: %v", err)
	}
	instance, err = store.GetBackendInstance(ctx, f.owner, f.backend.ID)
	if err != nil {
		t.Fatalf("reading the backend instance: %v", err)
	}
	if instance.OperationalStatus != domain.BackendOffline {
		t.Fatalf("operational status = %s, want OFFLINE", instance.OperationalStatus)
	}
	if instance.ConnectionID != nil {
		t.Error("a disconnected backend must hold no connection lease")
	}
}

// TestRenamingABackendKeepsNamesUnique pins that a backend can be renamed, but
// not to a name another live backend of the same owner already holds.
func TestRenamingABackendKeepsNamesUnique(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	if err := store.RenameBackendInstance(ctx, f.backend.ID, "zenbook-claude"); err != nil {
		t.Fatalf("renaming the backend: %v", err)
	}
	// Renaming to the current name is not a conflict with itself.
	if err := store.RenameBackendInstance(ctx, f.backend.ID, "zenbook-claude"); err != nil {
		t.Fatalf("renaming the backend to its own name: %v", err)
	}
	instance, err := store.GetBackendInstance(ctx, f.owner, f.backend.ID)
	if err != nil {
		t.Fatalf("reading the backend instance: %v", err)
	}
	if instance.Name != "zenbook-claude" {
		t.Fatalf("name = %q, want the new one", instance.Name)
	}

	other := domain.BackendInstance{
		ID: domain.NewBackendInstanceID(), OwnerID: &f.owner, Name: "desktop",
		OwnershipStatus: domain.BackendClaimed,
	}
	if err := store.CreateBackendInstance(ctx, &other, "hash-"+domain.NewUUID(), nil, nil); err != nil {
		t.Fatalf("creating the second backend: %v", err)
	}
	if err := store.RenameBackendInstance(ctx, other.ID, "zenbook-claude"); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("renaming to a taken name: got %v, want ErrConflict", err)
	}
}
