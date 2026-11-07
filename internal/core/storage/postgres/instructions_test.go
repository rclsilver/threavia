package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
	"github.com/rclsilver/threavia/migrations"
)

func TestInstructionsRoundTrip(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	if got, err := store.InstanceInstructions(ctx, f.owner); err != nil || got != (domain.Instructions{}) {
		t.Fatalf("instructions of an owner who never wrote any = %+v, %v", got, err)
	}

	global := domain.Instructions{Agent: "global agent", Supervision: "global supervision"}
	project := domain.Instructions{Agent: "project agent", Supervision: "project supervision"}
	backend := domain.Instructions{Agent: "backend agent", Supervision: "backend supervision"}
	if err := store.SetInstanceInstructions(ctx, f.owner, global); err != nil {
		t.Fatalf("setting the global instructions: %v", err)
	}
	// A second write replaces the first rather than conflicting with it.
	global.Agent = "global agent, revised"
	if err := store.SetInstanceInstructions(ctx, f.owner, global); err != nil {
		t.Fatalf("replacing the global instructions: %v", err)
	}
	if err := store.SetProjectInstructions(ctx, f.owner, f.project.ID, project); err != nil {
		t.Fatalf("setting the project instructions: %v", err)
	}
	if err := store.SetBackendInstructions(ctx, f.owner, f.backend.ID, backend); err != nil {
		t.Fatalf("setting the backend instructions: %v", err)
	}

	if got, err := store.InstanceInstructions(ctx, f.owner); err != nil || got != global {
		t.Errorf("global = %+v, %v; want %+v", got, err, global)
	}
	if got, err := store.ProjectInstructionsOf(ctx, f.owner, f.project.ID); err != nil || got != project {
		t.Errorf("project = %+v, %v; want %+v", got, err, project)
	}
	if got, err := store.BackendInstructions(ctx, f.owner, f.backend.ID); err != nil || got != backend {
		t.Errorf("backend = %+v, %v; want %+v", got, err, backend)
	}
	// The project agent text is the one the project form edits.
	if got, err := store.GetProject(ctx, f.owner, f.project.ID); err != nil || got.Instructions != project.Agent {
		t.Errorf("projects.instructions = %q, %v; want %q", got.Instructions, err, project.Agent)
	}

	stranger := domain.UserID("user-" + domain.NewUUID())
	if got, err := store.InstanceInstructions(ctx, stranger); err != nil || got != (domain.Instructions{}) {
		t.Errorf("another owner reads global %+v, %v", got, err)
	}
	if _, err := store.ProjectInstructionsOf(ctx, stranger, f.project.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Errorf("another owner reading the project: %v, want ErrNotFound", err)
	}
	if _, err := store.BackendInstructions(ctx, stranger, f.backend.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Errorf("another owner reading the backend: %v, want ErrNotFound", err)
	}
	if err := store.SetProjectInstructions(ctx, stranger, f.project.ID, project); !errors.Is(err, postgres.ErrNotFound) {
		t.Errorf("another owner writing the project: %v, want ErrNotFound", err)
	}
	if err := store.SetBackendInstructions(ctx, stranger, f.backend.ID, backend); !errors.Is(err, postgres.ErrNotFound) {
		t.Errorf("another owner writing the backend: %v, want ErrNotFound", err)
	}
}

func TestJobInstructionLayers(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	global := domain.Instructions{Agent: "ga", Supervision: "gs"}
	project := domain.Instructions{Agent: "pa", Supervision: "ps"}
	backend := domain.Instructions{Agent: "ba", Supervision: "bs"}
	if err := store.SetInstanceInstructions(ctx, f.owner, global); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProjectInstructions(ctx, f.owner, f.project.ID, project); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBackendInstructions(ctx, f.owner, f.backend.ID, backend); err != nil {
		t.Fatal(err)
	}
	session := domain.DefaultExecutionPolicy()
	session.Supervision = "ss"
	if err := store.SetSessionExecutionPolicy(ctx, f.owner, f.session.ID, &session); err != nil {
		t.Fatal(err)
	}
	job := domain.DefaultExecutionPolicy()
	job.Supervision = "js"
	if err := store.SetJobExecutionPolicy(ctx, f.job.ID, &job); err != nil {
		t.Fatal(err)
	}

	got, err := store.JobInstructionLayers(ctx, f.job.ID)
	if err != nil {
		t.Fatalf("reading the layers: %v", err)
	}
	want := postgres.JobLayers{
		ProjectName: f.project.Name, BackendName: f.backend.Name,
		Global: global, Project: project, Backend: backend,
		SessionSupervision: "ss", JobSupervision: "js",
	}
	if got != want {
		t.Errorf("layers = %+v\nwant %+v", got, want)
	}

	if _, err := store.JobInstructionLayers(ctx, domain.NewJobID()); !errors.Is(err, postgres.ErrNotFound) {
		t.Errorf("layers of an unknown job: %v, want ErrNotFound", err)
	}
}

// migrationFile runs one file of migration 000037 against the test schema,
// which pgtest has already brought to the latest version: running the down
// file first gives the schema of version 36 to write old-shaped data into.
func migrationFile(t *testing.T, db *postgres.DB, name string) {
	t.Helper()
	body, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(context.Background(), string(body)); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

const (
	instructionsDown = "000037_layered_instructions.down.sql"
	instructionsUp   = "000037_layered_instructions.up.sql"
)

// hasSupervisionKey reports whether a stored policy still carries the field.
func hasSupervisionKey(t *testing.T, db *postgres.DB, query string, arg any) bool {
	t.Helper()
	var has bool
	if err := db.Pool().QueryRow(context.Background(), query, arg).Scan(&has); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return has
}

// TestMigrationMovesSupervision pins that an installation upgraded with
// supervision in its instance, backend and project policies still has that
// text afterwards, at its new level, and that rolling back puts it back.
func TestMigrationMovesSupervision(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)
	migrationFile(t, db, instructionsDown)

	policy := func(mode domain.ExecutionMode, supervision string) *domain.ExecutionPolicy {
		p := domain.DefaultExecutionPolicy()
		p.Mode = mode
		p.Supervision = supervision
		return &p
	}
	if err := store.SetInstanceExecutionPolicy(ctx, f.owner, policy(domain.ExecutionSupervised, "global rule")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBackendExecutionPolicy(ctx, f.owner, f.backend.ID, policy(domain.ExecutionGuarded, "backend rule")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProjectExecutionPolicy(ctx, f.owner, f.project.ID, policy(domain.ExecutionSupervised, "project rule")); err != nil {
		t.Fatal(err)
	}

	migrationFile(t, db, instructionsUp)

	if got, err := store.InstanceInstructions(ctx, f.owner); err != nil || got.Supervision != "global rule" {
		t.Errorf("global = %+v, %v", got, err)
	}
	if got, err := store.BackendInstructions(ctx, f.owner, f.backend.ID); err != nil || got.Supervision != "backend rule" {
		t.Errorf("backend = %+v, %v", got, err)
	}
	if got, err := store.ProjectInstructionsOf(ctx, f.owner, f.project.ID); err != nil || got.Supervision != "project rule" {
		t.Errorf("project = %+v, %v", got, err)
	}
	queries := map[string]any{
		`SELECT execution_policy ? 'supervision' FROM instance_execution_policies WHERE owner_id = $1`: f.owner,
		`SELECT execution_policy ? 'supervision' FROM backend_instances WHERE id = $1`:                 f.backend.ID,
		`SELECT execution_policy ? 'supervision' FROM projects WHERE id = $1`:                          f.project.ID,
	}
	for query, arg := range queries {
		if hasSupervisionKey(t, db, query, arg) {
			t.Errorf("the policy still carries supervision: %s", query)
		}
	}
	// The rest of each policy is untouched.
	if got, err := store.BackendPolicy(ctx, f.owner, f.backend.ID); err != nil || got == nil || got.Mode != domain.ExecutionGuarded {
		t.Errorf("backend policy after migration = %+v, %v", got, err)
	}

	migrationFile(t, db, instructionsDown)
	if got, err := store.InstancePolicy(ctx, f.owner); err != nil || got == nil || got.Supervision != "global rule" {
		t.Errorf("instance policy after rollback = %+v, %v", got, err)
	}
	if got, err := store.BackendPolicy(ctx, f.owner, f.backend.ID); err != nil || got == nil || got.Supervision != "backend rule" {
		t.Errorf("backend policy after rollback = %+v, %v", got, err)
	}
	if got, err := store.ProjectPolicy(ctx, f.project.ID); err != nil || got == nil || got.Supervision != "project rule" {
		t.Errorf("project policy after rollback = %+v, %v", got, err)
	}
	migrationFile(t, db, instructionsUp)
}

// TestMigrationStripsInheritedCopies pins that a Session or Job policy holding
// a copy of the supervision it inherited loses the copy, audited, so the text
// does not reach the reviewer twice once levels add up; that a text of its own
// is kept; and that a Session copy matched against a backend is kept unless
// the Session never ran anywhere else.
func TestMigrationStripsInheritedCopies(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)

	// A second Project with no policy of its own, so its Sessions inherit from
	// the backend or the instance, and a second backend.
	bare := domain.Project{
		ID: domain.NewProjectID(), OwnerID: f.owner, Name: "bare",
		Status: domain.ProjectActive,
	}
	if err := store.CreateProject(ctx, &bare); err != nil {
		t.Fatal(err)
	}
	desktop := domain.BackendInstance{
		ID: domain.NewBackendInstanceID(), OwnerID: &f.owner, Name: "desktop",
		OwnershipStatus: domain.BackendClaimed,
		Capabilities:    []domain.Capability{domain.CapabilityCode},
		Capacity:        domain.Capacity{MaxConcurrentRuns: 1},
	}
	if err := store.CreateBackendInstance(ctx, &desktop, "hash-"+domain.NewUUID(), nil, nil); err != nil {
		t.Fatal(err)
	}
	session := func(project domain.ProjectID, title string, backends ...domain.BackendInstanceID) domain.SessionID {
		t.Helper()
		s := domain.Session{ID: domain.NewSessionID(), ProjectID: project, Title: title, Status: domain.SessionActive}
		if err := store.CreateSession(ctx, &s); err != nil {
			t.Fatal(err)
		}
		for _, backend := range backends {
			run := domain.Run{ID: domain.NewRunID(), SessionID: s.ID, BackendInstanceID: backend, ResumeStatus: domain.ResumeUnknown}
			if err := store.CreateRun(ctx, &run); err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	other := session(f.project.ID, "Another session")
	backendOnly := session(bare.ID, "Always on the laptop", f.backend.ID, f.backend.ID)
	twoBackends := session(bare.ID, "Laptop and desktop", desktop.ID, f.backend.ID)
	instanceCopy := session(bare.ID, "Never ran")
	noRuns := session(bare.ID, "Never ran either")

	migrationFile(t, db, instructionsDown)

	policy := func(supervision string) *domain.ExecutionPolicy {
		p := domain.DefaultExecutionPolicy()
		p.Mode = domain.ExecutionSupervised
		p.Supervision = supervision
		return &p
	}
	if err := store.SetInstanceExecutionPolicy(ctx, f.owner, policy("Global rule.")); err != nil {
		t.Fatal(err)
	}
	// Both backends say the same, so twoBackends matches whichever Run Core
	// takes as its latest.
	for _, backend := range []domain.BackendInstanceID{f.backend.ID, desktop.ID} {
		if err := store.SetBackendExecutionPolicy(ctx, f.owner, backend, policy("Backend rule.")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetProjectExecutionPolicy(ctx, f.owner, f.project.ID, policy("Stop before deploys.")); err != nil {
		t.Fatal(err)
	}
	sessions := map[domain.SessionID]string{
		// Session A holds what "Override" pre-filled, give or take whitespace.
		f.session.ID: " Stop before deploys.\n",
		// Session B says something of its own.
		other: "Never touch prod.",
		// Copies of the backend's text: removable only where the Session never
		// left that backend.
		backendOnly: "Backend rule.",
		twoBackends: "Backend rule.",
		// A Session without Runs inherited from the instance, never a backend.
		instanceCopy: "Global rule.",
		noRuns:       "Backend rule.",
	}
	for id, text := range sessions {
		if err := store.SetSessionExecutionPolicy(ctx, f.owner, id, policy(text)); err != nil {
			t.Fatal(err)
		}
	}
	// The Job of Session A holds a copy of the Session's, which it inherited
	// from the Session rather than the Project: the Job is judged before the
	// Session loses its copy.
	if err := store.SetJobExecutionPolicy(ctx, f.job.ID, policy("Stop before deploys.")); err != nil {
		t.Fatal(err)
	}

	migrationFile(t, db, instructionsUp)

	removed := map[domain.SessionID]bool{f.session.ID: true, backendOnly: true, instanceCopy: true}
	for id, text := range sessions {
		got, err := store.SessionPolicy(ctx, id)
		if err != nil || got == nil || got.Mode != domain.ExecutionSupervised {
			t.Errorf("session %q lost more than its copy: %+v, %v", text, got, err)
			continue
		}
		switch {
		case removed[id] && got.Supervision != "":
			t.Errorf("session with %q kept its inherited copy", text)
		case !removed[id] && got.Supervision != text:
			t.Errorf("session with %q now has %q; its text must stay", text, got.Supervision)
		}
	}
	if hasSupervisionKey(t, db, `SELECT execution_policy ? 'supervision' FROM jobs WHERE id = $1`, f.job.ID) {
		t.Error("the job kept its copy of the session supervision")
	}
	if got, err := store.ProjectInstructionsOf(ctx, f.owner, f.project.ID); err != nil || got.Supervision != "Stop before deploys." {
		t.Errorf("project supervision = %+v, %v", got, err)
	}

	rows, err := db.Pool().Query(ctx, `
		SELECT owner_id, actor_id, project_id, session_id, COALESCE(job_id, ''), detail
		FROM audit_entries WHERE action = 'instructions.migration_copy_removed'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type entry struct {
		owner, actor, project, session, job string
		detail                              map[string]string
	}
	got := map[string]entry{}
	for rows.Next() {
		var e entry
		var raw []byte
		if err := rows.Scan(&e.owner, &e.actor, &e.project, &e.session, &e.job, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e.detail); err != nil {
			t.Fatal(err)
		}
		key := e.session
		if e.job != "" {
			key = e.job
		}
		if _, dup := got[key]; dup {
			t.Errorf("two audit entries for %s", key)
		}
		got[key] = e
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]entry{
		string(f.job.ID):     {project: string(f.project.ID), session: string(f.session.ID), job: string(f.job.ID)},
		string(f.session.ID): {project: string(f.project.ID), session: string(f.session.ID)},
		string(backendOnly):  {project: string(bare.ID), session: string(backendOnly)},
		string(instanceCopy): {project: string(bare.ID), session: string(instanceCopy)},
	}
	if len(got) != len(want) {
		t.Errorf("audit entries = %+v, want one per removed copy (%d)", got, len(want))
	}
	for key, w := range want {
		e, ok := got[key]
		if !ok {
			t.Errorf("no audit entry for %s", key)
			continue
		}
		if e.owner != string(f.owner) || e.actor != "migration" || e.project != w.project ||
			e.session != w.session || e.job != w.job || e.detail["supervision"] == "" {
			t.Errorf("audit entry for %s = %+v", key, e)
		}
	}
}
