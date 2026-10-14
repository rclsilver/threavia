package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
	"github.com/rclsilver/threavia/migrations"
)

const sshTitle = "Git access to stash.ovh.net goes through SSH with the agent at ~/.ssh-agent/ssh-agent.sock; never borrow another session's agent"
const sshContent = "Set SSH_AUTH_SOCK for Git. Bitbucket token is for REST only."

func TestMemorySearchTechnicalAndNaturalLanguage(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	local := domain.Decision{ID: domain.NewDecisionID(), ProjectID: f.project.ID, Title: sshTitle, Content: sshContent, Importance: domain.DecisionNormal, Status: domain.DecisionActive}
	global := local
	global.ID, global.ProjectID, global.OwnerID = domain.NewDecisionID(), "", f.owner
	for _, decision := range []*domain.Decision{&local, &global} {
		if err := store.CreateDecision(ctx, decision); err != nil {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(map[string]string{"text": sshTitle + " " + sshContent})
	record := events.Record{Envelope: events.Envelope{Type: events.TypeAgentMessage, ProjectID: &f.project.ID, Payload: payload}}
	if err := store.AppendEvent(ctx, &record); err != nil {
		t.Fatal(err)
	}
	queries := []string{"SSH_AUTH_SOCK", "Bitbucket token", "stash.ovh.net", "stash", "git access stash SSH agent", "Git access to stash.ovh.net goes through SSH with the agent at ~/.ssh-agent/ssh-agent.sock", sshTitle, "please explain git access stash SSH agent configuration missingword"}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			t.Run("memory", func(t *testing.T) {
				got, err := store.SearchMemory(ctx, f.owner, f.project.ID, query, 20)
				if err != nil || len(got) != 2 {
					t.Fatalf("got %v, %v; want project and global decisions", got, err)
				}
			})
			t.Run("global", func(t *testing.T) {
				got, err := store.SearchMemory(ctx, f.owner, "", query, 20)
				if err != nil || len(got) != 1 || got[0].ID != global.ID {
					t.Fatalf("got %v, %v; want global decision", got, err)
				}
			})
			t.Run("decisions", func(t *testing.T) {
				got, err := store.SearchDecisions(ctx, f.project.ID, query, 20)
				if err != nil || len(got) != 1 || got[0].ID != local.ID {
					t.Fatalf("got %v, %v; want project decision", got, err)
				}
			})
			t.Run("history", func(t *testing.T) {
				got, err := store.SearchHistory(ctx, f.project.ID, query, 20)
				if err != nil || len(got) != 1 || got[0].Envelope.ID != record.ID {
					t.Fatalf("got %v, %v; want matching event", got, err)
				}
			})
		})
	}
}

func TestMemorySearchEmptyQueriesAndScope(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	other := newFixture(t, store, ctx)
	local := domain.Decision{ID: domain.NewDecisionID(), ProjectID: f.project.ID, Title: "SSH agent", Status: domain.DecisionActive, Importance: domain.DecisionNormal}
	global := local
	global.ID, global.ProjectID, global.OwnerID = domain.NewDecisionID(), "", f.owner
	foreign := local
	foreign.ID, foreign.ProjectID = domain.NewDecisionID(), other.project.ID
	superseded := local
	superseded.ID, superseded.Status = domain.NewDecisionID(), domain.DecisionSuperseded
	for _, decision := range []*domain.Decision{&local, &global, &foreign, &superseded} {
		if err := store.CreateDecision(ctx, decision); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"", "  ", "!?':-/\\"} {
		got, err := store.SearchMemory(ctx, f.owner, f.project.ID, query, 20)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty query %q: %v, %v", query, got, err)
		}
		decisions, err := store.SearchDecisions(ctx, f.project.ID, query, 20)
		if err != nil || len(decisions) != 0 {
			t.Fatalf("empty decision query %q: %v, %v", query, decisions, err)
		}
		history, err := store.SearchHistory(ctx, f.project.ID, query, 20)
		if err != nil || len(history) != 0 {
			t.Fatalf("empty history query %q: %v, %v", query, history, err)
		}
	}
	got, err := store.SearchMemory(ctx, f.owner, f.project.ID, "SSH agent missingword", 20)
	if err != nil || len(got) != 2 {
		t.Fatalf("active project and global memory: %v, %v", got, err)
	}
	for _, decision := range got {
		if decision.ID != local.ID && decision.ID != global.ID {
			t.Fatal("unexpected fact", decision)
		}
	}
	decisions, err := store.SearchDecisions(ctx, f.project.ID, "SSH agent missingword", 20)
	if err != nil || len(decisions) != 2 {
		t.Fatalf("project search must preserve historical facts: %v, %v", decisions, err)
	}
}

func TestMemorySearchMigrationReindexesExistingTextAndRollsBack(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)
	apply := func(name string) {
		t.Helper()
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool().Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	apply("000022_memory_search.down.sql")
	decision := domain.Decision{ID: domain.NewDecisionID(), ProjectID: f.project.ID, Title: sshTitle, Content: sshContent, Status: domain.DecisionActive, Importance: domain.DecisionNormal}
	if err := store.CreateDecision(ctx, &decision); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"text": sshTitle})
	record := events.Record{Envelope: events.Envelope{Type: events.TypeAgentMessage, ProjectID: &f.project.ID, Payload: payload}}
	if err := store.AppendEvent(ctx, &record); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		apply("000022_memory_search.up.sql")
		facts, err := store.SearchMemory(ctx, f.owner, f.project.ID, "stash", 10)
		if err != nil || len(facts) != 1 || facts[0].ID != decision.ID {
			t.Fatalf("existing fact not reindexed: %v, %v", facts, err)
		}
		hits, err := store.SearchHistory(ctx, f.project.ID, "stash", 10)
		if err != nil || len(hits) != 1 || hits[0].Envelope.ID != record.ID {
			t.Fatalf("existing event not reindexed: %v, %v", hits, err)
		}
		apply("000022_memory_search.down.sql")
		preserved, err := store.GetDecision(ctx, f.owner, decision.ID)
		if err != nil || preserved.Title != sshTitle || preserved.Content != sshContent {
			t.Fatalf("rollback changed stored fact: %+v, %v", preserved, err)
		}
		var eventText string
		if err := db.Pool().QueryRow(ctx, `SELECT payload->>'text' FROM events WHERE id = $1`, record.ID).Scan(&eventText); err != nil || eventText != sshTitle {
			t.Fatalf("rollback changed stored event: %q, %v", eventText, err)
		}
	}
}

func TestMemorySearchRanksCoverageBeforeRepetition(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	for _, title := range []string{"Git access stash SSH agent", "agent agent agent agent agent agent"} {
		decision := domain.Decision{ID: domain.NewDecisionID(), ProjectID: f.project.ID, Title: title, Status: domain.DecisionActive, Importance: domain.DecisionNormal}
		if err := store.CreateDecision(ctx, &decision); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"summary": title})
		record := events.Record{Envelope: events.Envelope{Type: events.TypeAgentMessage, ProjectID: &f.project.ID, Payload: payload}}
		if err := store.AppendEvent(ctx, &record); err != nil {
			t.Fatal(err)
		}
	}
	query := "git access stash SSH agent missingword"
	got, err := store.SearchMemory(ctx, f.owner, f.project.ID, query, 1)
	if err != nil || len(got) != 1 || got[0].Title != "Git access stash SSH agent" {
		t.Fatalf("ranking: %v, %v", got, err)
	}
	hits, err := store.SearchHistory(ctx, f.project.ID, query, 1)
	if err != nil || len(hits) != 1 || string(hits[0].Envelope.Payload) != `{"summary": "Git access stash SSH agent"}` {
		t.Fatalf("history ranking: %v, %v", hits, err)
	}
}
