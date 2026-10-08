package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// newTask is a small helper so the graph tests read as graphs.
func newTask(t *testing.T, store *postgres.Store, f fixture, title string) domain.Task {
	t.Helper()
	task := domain.Task{
		ID: domain.NewTaskID(), ProjectID: f.project.ID,
		Title: title, Status: domain.TaskTodo,
	}
	if err := store.CreateTask(context.Background(), &task); err != nil {
		t.Fatalf("creating task %q: %v", title, err)
	}
	return task
}

// TestDependencyCyclesAreRejected pins the rule of specification section 14:
// Core refuses a dependency that would close a loop, at any depth.
func TestDependencyCyclesAreRejected(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	a := newTask(t, store, f, "a")
	b := newTask(t, store, f, "b")
	c := newTask(t, store, f, "c")

	// a -> b -> c is a legitimate chain.
	if err := store.AddTaskDependency(ctx, a.ID, b.ID); err != nil {
		t.Fatalf("a depends on b: %v", err)
	}
	if err := store.AddTaskDependency(ctx, b.ID, c.ID); err != nil {
		t.Fatalf("b depends on c: %v", err)
	}

	// c -> a would close the loop, two edges away.
	if err := store.AddTaskDependency(ctx, c.ID, a.ID); !errors.Is(err, postgres.ErrDependencyCycle) {
		t.Fatalf("closing the loop: got %v, want ErrDependencyCycle", err)
	}
	// So would the direct back edge.
	if err := store.AddTaskDependency(ctx, b.ID, a.ID); !errors.Is(err, postgres.ErrDependencyCycle) {
		t.Fatalf("direct back edge: got %v, want ErrDependencyCycle", err)
	}
	// And so would a self-edge.
	if err := store.AddTaskDependency(ctx, a.ID, a.ID); !errors.Is(err, postgres.ErrDependencyCycle) {
		t.Fatalf("self edge: got %v, want ErrDependencyCycle", err)
	}

	// A second, independent parent is fine: dependencies are a graph, not a tree.
	d := newTask(t, store, f, "d")
	if err := store.AddTaskDependency(ctx, d.ID, c.ID); err != nil {
		t.Fatalf("a second task may depend on c: %v", err)
	}
}

// TestReadyTasksDeriveBlockedness pins that blocked is computed from the graph
// rather than stored, so it can never drift.
func TestReadyTasksDeriveBlockedness(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	foundation := newTask(t, store, f, "pose the foundation")
	walls := newTask(t, store, f, "raise the walls")
	roof := newTask(t, store, f, "put the roof on")

	if err := store.AddTaskDependency(ctx, walls.ID, foundation.ID); err != nil {
		t.Fatalf("walls depend on the foundation: %v", err)
	}
	if err := store.AddTaskDependency(ctx, roof.ID, walls.ID); err != nil {
		t.Fatalf("the roof depends on the walls: %v", err)
	}

	ready, err := store.ReadyTasks(ctx, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing ready tasks: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != foundation.ID {
		t.Fatalf("ready = %d tasks, want only the foundation", len(ready))
	}

	// Finishing the foundation unblocks the walls, and nothing else.
	if _, err := store.UpdateTask(ctx, foundation.ID, "", "", domain.TaskDone); err != nil {
		t.Fatalf("completing the foundation: %v", err)
	}
	ready, err = store.ReadyTasks(ctx, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing ready tasks: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != walls.ID {
		t.Fatalf("ready = %+v, want only the walls", ready)
	}

	// The derived view agrees with the stored graph.
	full, err := store.GetTask(ctx, f.owner, roof.ID)
	if err != nil {
		t.Fatalf("reading the roof task: %v", err)
	}
	statuses := map[domain.TaskID]domain.TaskStatus{foundation.ID: domain.TaskDone, walls.ID: domain.TaskTodo}
	if !full.Blocked(statuses) {
		t.Error("the roof is still blocked by the walls")
	}
}

// TestCompletionTimestampFollowsTheStatus pins the shape the schema enforces: a
// DONE task has a completion time and no other status does.
func TestCompletionTimestampFollowsTheStatus(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	task := newTask(t, store, f, "something")

	done, err := store.UpdateTask(ctx, task.ID, "", "", domain.TaskDone)
	if err != nil {
		t.Fatalf("completing the task: %v", err)
	}
	if done.CompletedAt == nil {
		t.Fatal("a completed task must carry a completion time")
	}

	reopened, err := store.UpdateTask(ctx, task.ID, "", "", domain.TaskInProgress)
	if err != nil {
		t.Fatalf("reopening the task: %v", err)
	}
	if reopened.CompletedAt != nil {
		t.Fatal("a reopened task must not keep a completion time")
	}
}

// TestDecisionSupersession pins section 13: a superseded Decision stays
// historical but stops being current, and only active IMPORTANT ones are
// carried into a Run context.
func TestDecisionSupersession(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	old := domain.Decision{
		ID: domain.NewDecisionID(), ProjectID: f.project.ID,
		Title: "Deploy with Ansible", Importance: domain.DecisionImportant, Status: domain.DecisionActive,
	}
	if err := store.CreateDecision(ctx, &old); err != nil {
		t.Fatalf("creating the first decision: %v", err)
	}
	normal := domain.Decision{
		ID: domain.NewDecisionID(), ProjectID: f.project.ID,
		Title: "Prefer tabs", Importance: domain.DecisionNormal, Status: domain.DecisionActive,
	}
	if err := store.CreateDecision(ctx, &normal); err != nil {
		t.Fatalf("creating the normal decision: %v", err)
	}

	important, err := store.ImportantDecisions(ctx, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing important decisions: %v", err)
	}
	if len(important) != 1 || important[0].ID != old.ID {
		t.Fatalf("%d important decisions, want only the IMPORTANT one", len(important))
	}

	// A new ruling replaces the old one.
	replacement := domain.Decision{
		ID: domain.NewDecisionID(), ProjectID: f.project.ID,
		Title: "Deploy with Puppet", Importance: domain.DecisionImportant,
		Status: domain.DecisionActive, Supersedes: &old.ID,
	}
	if err := store.CreateDecision(ctx, &replacement); err != nil {
		t.Fatalf("creating the replacement: %v", err)
	}
	if _, err := store.SupersedeDecision(ctx, old.ID); err != nil {
		t.Fatalf("superseding the old decision: %v", err)
	}

	important, err = store.ImportantDecisions(ctx, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing important decisions: %v", err)
	}
	if len(important) != 1 || important[0].ID != replacement.ID {
		t.Fatalf("the run context must carry only the current ruling, got %+v", important)
	}

	// Superseded is history, not deletion.
	all, err := store.ListDecisions(ctx, f.owner, f.project.ID, true)
	if err != nil {
		t.Fatalf("listing every decision: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("%d decisions, want 3: superseding must not delete", len(all))
	}

	// Superseding twice changes nothing.
	if _, err := store.SupersedeDecision(ctx, old.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("superseding twice: got %v, want ErrNotFound", err)
	}
}

// TestDecisionPinning pins a Decision to every Job and back: the importance is
// the one thing that changes, and only for the owner of the Project.
func TestDecisionPinning(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	decision := domain.Decision{
		ID: domain.NewDecisionID(), ProjectID: f.project.ID,
		Title: "Pin chart versions", Content: "A floating version broke a rollout.",
		Importance: domain.DecisionNormal, Status: domain.DecisionActive,
	}
	if err := store.CreateDecision(ctx, &decision); err != nil {
		t.Fatalf("creating the decision: %v", err)
	}

	pinned, err := store.SetDecisionImportance(ctx, f.owner, decision.ID, domain.DecisionImportant)
	if err != nil {
		t.Fatalf("pinning: %v", err)
	}
	if pinned.Importance != domain.DecisionImportant || pinned.Content != decision.Content {
		t.Fatalf("pinned decision = %+v, want IMPORTANT with its content unchanged", pinned)
	}
	important, err := store.ImportantDecisions(ctx, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing important decisions: %v", err)
	}
	if len(important) != 1 || important[0].ID != decision.ID {
		t.Fatalf("a pinned decision must travel with every Job, got %+v", important)
	}

	if _, err := store.SetDecisionImportance(ctx, f.owner, decision.ID, domain.DecisionNormal); err != nil {
		t.Fatalf("unpinning: %v", err)
	}
	important, err = store.ImportantDecisions(ctx, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing important decisions: %v", err)
	}
	if len(important) != 0 {
		t.Fatalf("an unpinned decision must stop travelling, got %+v", important)
	}

	// Someone else's decision is not found, rather than changed.
	if _, err := store.SetDecisionImportance(ctx, "someone-else", decision.ID, domain.DecisionImportant); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("pinning another user's decision: got %v, want ErrNotFound", err)
	}
}

// TestKnowledgeSearch pins the searchable half of project memory: NORMAL
// decisions and the message history are found without being injected anywhere.
func TestKnowledgeSearch(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	decision := domain.Decision{
		ID: domain.NewDecisionID(), ProjectID: f.project.ID,
		Title:      "Use PostgreSQL for the control plane",
		Content:    "Relational state, no event sourcing.",
		Importance: domain.DecisionNormal, Status: domain.DecisionActive,
	}
	if err := store.CreateDecision(ctx, &decision); err != nil {
		t.Fatalf("creating the decision: %v", err)
	}

	hits, err := store.SearchDecisions(ctx, f.project.ID, "postgresql", 10)
	if err != nil {
		t.Fatalf("searching decisions: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != decision.ID {
		t.Fatalf("%d decision hits, want the one mentioning PostgreSQL", len(hits))
	}

	// The episodic layer: an agent message becomes searchable history.
	record := &events.Record{Envelope: events.Envelope{
		Type:      events.TypeAgentMessage,
		ProjectID: &f.project.ID,
		SessionID: &f.session.ID,
		Payload:   json.RawMessage(`{"text":"I rewired the puppet manifest for the homelab"}`),
	}}
	if err := store.AppendEvent(ctx, record); err != nil {
		t.Fatalf("appending the event: %v", err)
	}

	history, err := store.SearchHistory(ctx, f.project.ID, "puppet manifest", 10)
	if err != nil {
		t.Fatalf("searching history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("%d history hits, want 1", len(history))
	}
	if history[0].Excerpt == "" {
		t.Error("a history hit must carry the matching excerpt")
	}

	// A query that matches nothing returns nothing rather than everything.
	empty, err := store.SearchHistory(ctx, f.project.ID, "kubernetes operator", 10)
	if err != nil {
		t.Fatalf("searching history: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("%d hits for an unrelated query, want 0", len(empty))
	}
}
