package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/service"
	"github.com/rclsilver/threavia/internal/core/skills"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

func lifecycleAudit(t *testing.T, c *core, prefix string) []postgres.AuditEntry {
	t.Helper()
	entries, err := c.store.ListAudit(context.Background(), "thomas", "", 500)
	if err != nil {
		t.Fatal(err)
	}
	var out []postgres.AuditEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Action, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func lifecycleEvents(t *testing.T, c *core, prefix string) []events.Envelope {
	t.Helper()
	entries, err := c.store.EventsAfter(context.Background(), "thomas", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []events.Envelope
	for _, entry := range entries {
		if strings.HasPrefix(string(entry.Type), prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func lifecycleSQL(t *testing.T, c *core, sql string) {
	t.Helper()
	if _, err := c.db.Pool().Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

func noLifecyclePublication(t *testing.T, subscription *events.Subscription, prefix string) {
	t.Helper()
	for {
		select {
		case event := <-subscription.Events():
			if strings.HasPrefix(string(event.Type), prefix) {
				t.Fatalf("rolled-back event published: %s", event.Type)
			}
		default:
			return
		}
	}
}

func skillCore(t *testing.T) (*core, *memoryObjects, string) {
	t.Helper()
	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))
	return c, objects, c.createProject("Skills lifecycle")
}

func TestSkillLifecycleEventsAuditAndReplay(t *testing.T) {
	c, _, project := skillCore(t)
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	var installed skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy"), &installed)
	first := awaitFrame(t, web, "skill.installed")
	if got := awaitFrame(t, editor, "skill.installed"); got.id != first.id {
		t.Fatal("clients disagree")
	}
	var payload map[string]any
	if err := json.Unmarshal(first.event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["skillId"] != installed.ID || payload["name"] != "deploy" || payload["replaced"] != false {
		t.Fatal(payload)
	}
	foreign := c.asUser("mallory")
	foreign.mustDo("DELETE", "/api/v1/skills/"+installed.ID, nil, nil, 404)
	var replaced skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz", "application/gzip", skillArchive(t, "deploy"), &replaced)
	if replaced.ID != installed.ID {
		t.Fatal("replacement changed skill identity")
	}
	second := awaitFrame(t, web, "skill.installed")
	if err := json.Unmarshal(second.event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["replaced"] != true {
		t.Fatal(payload)
	}
	c.mustDo("DELETE", "/api/v1/skills/"+installed.ID, nil, nil, 204, [2]string{"X-Threavia-Channel", "web"})
	removed := awaitFrame(t, web, "skill.removed")
	awaitFrame(t, editor, "skill.installed")
	if got := awaitFrame(t, editor, "skill.removed"); got.id != removed.id {
		t.Fatal("clients disagree on removal")
	}
	replay, closeReplay := c.openStream(t, second.id)
	defer closeReplay()
	if got := awaitFrame(t, replay, "skill.removed"); got.id != removed.id {
		t.Fatal("removal cannot replay")
	}
	entries := lifecycleAudit(t, c, "skill.")
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if entry.ActorID != "thomas" || entry.ProjectID != project || entry.SubjectID != installed.ID || entry.SessionID != "" {
			t.Fatal(entry)
		}
	}
	if entries[0].Action != "skill.removed" || entries[0].Channel != "web" {
		t.Fatal(entries[0])
	}
	for _, entry := range lifecycleEvents(t, c, "skill.") {
		if entry.ProjectID == nil || string(*entry.ProjectID) != project || entry.SessionID != nil {
			t.Fatal(entry)
		}
	}
	other, err := c.store.EventsAfter(context.Background(), "mallory", 0, 1000)
	if err != nil || len(other) != 0 {
		t.Fatalf("owner isolation: %v %v", other, err)
	}
}

func TestSkillLifecycleHistoryRollsBackMutation(t *testing.T) {
	for _, table := range []string{"events", "audit_entries"} {
		for _, action := range []string{"install", "replace", "remove"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				c, objects, project := skillCore(t)
				var before skillResponse
				if action != "install" {
					c.mustUpload("/api/v1/projects/"+project+"/skills?filename=a.tar.gz", "application/gzip", skillArchive(t, "deploy"), &before)
				}
				kind := "skill.installed"
				if action == "remove" {
					kind = "skill.removed"
				}
				column := "type"
				if table == "audit_entries" {
					column = "action"
				}
				lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT reject_lifecycle CHECK (%s <> '%s') NOT VALID", table, column, kind))
				subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
				defer unsubscribe()
				var status int
				if action == "remove" {
					status = c.do("DELETE", "/api/v1/skills/"+before.ID, nil, nil)
				} else {
					status = c.doRaw("POST", "/api/v1/projects/"+project+"/skills?filename=b.tar.gz", "application/gzip", skillArchive(t, "deploy"), nil)
				}
				if status < 400 {
					t.Fatalf("history failure accepted: %d", status)
				}
				noLifecyclePublication(t, subscription, "skill.")
				var listed struct{ Items []skillResponse }
				c.mustDo("GET", "/api/v1/projects/"+project+"/skills", nil, &listed, 200)
				want := 0
				if action != "install" {
					want = 1
				}
				if len(listed.Items) != want || objects.count() != want || len(lifecycleEvents(t, c, "skill.")) != want || len(lifecycleAudit(t, c, "skill.")) != want {
					t.Fatalf("rollback failed: %+v, objects %d", listed, objects.count())
				}
				if want == 1 && listed.Items[0].ArtifactID != before.ArtifactID {
					t.Fatal("old bundle was lost")
				}
			})
		}
	}
}

func TestSessionLifecycleAuditSurvivesDeletion(t *testing.T) {
	c, backend, project, dir := setup(t)
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	input := map[string]any{"projectId": project, "backendInstanceId": c.backendID, "workingDirectoryId": dir, "message": "Keep this title"}
	var first, retry struct{ Session struct{ ID string } }
	c.mustDo("POST", "/api/v1/sessions/start", input, &first, 201, [2]string{"Idempotency-Key", "lifecycle"}, [2]string{"X-Threavia-Channel", "vscode"})
	c.mustDo("POST", "/api/v1/sessions/start", input, &retry, 201, [2]string{"Idempotency-Key", "lifecycle"})
	if first.Session.ID != retry.Session.ID {
		t.Fatal("start was not idempotent")
	}
	start := receive(t, "first job", backend.starts)
	id := first.Session.ID
	c.mustDo("DELETE", "/api/v1/sessions/"+id, nil, nil, 409)
	c.asUser("mallory").mustDo("POST", "/api/v1/sessions/"+id+"/archive", nil, nil, 404)
	c.mustDo("POST", "/api/v1/sessions/"+id+"/archive", nil, nil, 200)
	c.mustDo("POST", "/api/v1/sessions/"+id+"/archive", nil, nil, 409)
	c.mustDo("POST", "/api/v1/sessions/"+id+"/restore", nil, nil, 200)
	completeDelegatedJob(t, c, backend, start)
	c.mustDo("DELETE", "/api/v1/sessions/"+id, nil, nil, 204)
	deleted := awaitFrame(t, web, "session.deleted")
	if other := awaitFrame(t, editor, "session.deleted"); deleted.id != other.id {
		t.Fatal("clients disagree on deletion")
	}
	replay, closeReplay := c.openStream(t, fmt.Sprint(deleted.event.Sequence-1))
	defer closeReplay()
	if got := awaitFrame(t, replay, "session.deleted"); got.id != deleted.id {
		t.Fatal("deletion notice cannot replay")
	}
	entries := lifecycleAudit(t, c, "session.")
	if len(entries) != 4 {
		t.Fatalf("audit duplicated or lost: %v", entries)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.ActorID != "thomas" || entry.ProjectID != project || entry.SessionID != id || entry.SubjectID != id || !strings.Contains(string(entry.Detail), "Keep this title") {
			t.Fatal(entry)
		}
		seen[entry.Action] = true
		if entry.Action == "session.created" && entry.Channel != "vscode" {
			t.Fatal(entry)
		}
	}
	if len(seen) != 4 {
		t.Fatal(seen)
	}
	persisted := lifecycleEvents(t, c, "session.")
	if len(persisted) != 1 || persisted[0].Type != events.TypeSessionDeleted || persisted[0].SessionID != nil || persisted[0].ProjectID == nil {
		t.Fatalf("deletion notice disappeared with history: %v", persisted)
	}
	var payload map[string]any
	if err := json.Unmarshal(persisted[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sessionId"] != id || payload["artifactsDeleted"] != false {
		t.Fatal(payload)
	}
	other, err := c.store.ListAudit(context.Background(), "mallory", "", 500)
	if err != nil || len(other) != 0 {
		t.Fatal("audit leaks to other owners", other, err)
	}
}

func TestSessionLifecycleHistoryRollsBackMutation(t *testing.T) {
	for _, table := range []string{"events", "audit_entries"} {
		for _, action := range []string{"created", "archived", "restored", "deleted"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				c, backend, project, dir := setup(t)
				objects := newMemoryObjects()
				c.svc.SetObjectStore(objects, 8<<20)
				id := ""
				wantStatus := domain.SessionActive
				var artifactID domain.ArtifactID
				if action != "created" {
					id = c.startSession(project, c.backendID, dir, "Preserve me")
					start := receive(t, "job", backend.starts)
					completeDelegatedJob(t, c, backend, start)
					if action == "restored" {
						c.mustDo("POST", "/api/v1/sessions/"+id+"/archive", nil, nil, 200)
						wantStatus = domain.SessionArchived
					}
					if action == "deleted" {
						artifact, err := c.svc.UploadArtifact(context.Background(), auth.Identity{UserID: "thomas"}, domain.ProjectID(project), "keep.txt", "text/plain", strings.NewReader("keep"), domain.Scope{SessionID: domain.SessionID(id)})
						if err != nil {
							t.Fatal(err)
						}
						artifactID = artifact.ID
					}
				}
				beforeEvents, beforeAudit := len(lifecycleEvents(t, c, "session.")), len(lifecycleAudit(t, c, "session."))
				column := "type"
				if table == "audit_entries" {
					column = "action"
				}
				lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT reject_lifecycle CHECK (%s <> 'session.%s') NOT VALID", table, column, action))
				subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
				defer unsubscribe()
				var status int
				switch action {
				case "created":
					status = c.do("POST", "/api/v1/sessions/start", map[string]any{"projectId": project, "backendInstanceId": c.backendID, "message": "discard"}, nil)
				case "archived":
					status = c.do("POST", "/api/v1/sessions/"+id+"/archive", nil, nil)
				case "restored":
					status = c.do("POST", "/api/v1/sessions/"+id+"/restore", nil, nil)
				case "deleted":
					status = c.do("DELETE", "/api/v1/sessions/"+id+"?artifacts=delete", nil, nil)
				}
				if status < 400 {
					t.Fatalf("history failure accepted: %d", status)
				}
				noLifecyclePublication(t, subscription, "session.")
				if len(lifecycleEvents(t, c, "session.")) != beforeEvents || len(lifecycleAudit(t, c, "session.")) != beforeAudit {
					t.Fatal("failed operation left history")
				}
				if id != "" {
					session, err := c.store.GetSession(context.Background(), "thomas", domain.SessionID(id))
					if err != nil || session.Status != wantStatus {
						t.Fatal("session mutation did not roll back", session, err)
					}
				} else {
					sessions, err := c.store.ListSessions(context.Background(), "thomas", domain.ProjectID(project), true)
					if err != nil || len(sessions) != 0 {
						t.Fatal("failed start left session", sessions, err)
					}
				}
				if artifactID != "" {
					if _, err := c.store.GetArtifact(context.Background(), "thomas", artifactID); err != nil || objects.count() != 1 {
						t.Fatal("failed deletion lost artifacts", err)
					}
				}
			})
		}
	}
}

func TestDelegatedSessionLifecycleAuditActor(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.startSession(project, c.backendID, dir, "Manager")
	manager := receive(t, "manager", backend.starts)
	id, child := delegatedSession(t, backend, manager, map[string]any{"message": "Child", "idempotencyKey": "audit-child"})
	completeDelegatedJob(t, c, backend, child)
	backend.callCoreTool(t, context.Background(), manager.GetRunId(), manager.GetJobId(), "session_archive", map[string]any{"sessionId": id, "archived": true})
	count := 0
	for _, entry := range lifecycleAudit(t, c, "session.") {
		if entry.SessionID != id {
			continue
		}
		if entry.ActorID != "agent" || entry.JobID != manager.GetJobId() || entry.Channel != "agent" {
			t.Fatal(entry)
		}
		count++
	}
	if count != 2 {
		t.Fatal(count)
	}
}

func TestConcurrentSkillInstallsAuditTheActualReplacement(t *testing.T) {
	c, objects, project := skillCore(t)
	archive := skillArchive(t, "deploy")
	gate := make(chan struct{})
	type result struct {
		skill domain.Skill
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-gate
			skill, err := c.svc.InstallSkill(context.Background(), auth.Identity{UserID: "thomas"}, domain.ProjectID(project),
				domain.SkillSource{Type: domain.SkillSourceUpload}, "", strings.NewReader(archive))
			results <- result{skill, err}
		}()
	}
	close(gate)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.skill.ID != second.skill.ID || objects.count() != 1 {
		t.Fatalf("concurrent install failed: %+v %+v, objects %d", first, second, objects.count())
	}
	entries := lifecycleEvents(t, c, "skill.")
	if len(entries) != 2 || len(lifecycleAudit(t, c, "skill.")) != 2 {
		t.Fatal(entries)
	}
	replacements := 0
	for _, event := range entries {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["replaced"] == true {
			replacements++
		}
	}
	if replacements != 1 {
		t.Fatal("replacement metadata did not follow serialized state", replacements)
	}
}

func TestConcurrentSessionArchivesAuditOneTransition(t *testing.T) {
	c, backend, project, dir := setup(t)
	id := c.startSession(project, c.backendID, dir, "Archive once")
	receive(t, "job", backend.starts)
	gate := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-gate
			_, err := c.svc.SetSessionStatus(context.Background(), auth.Identity{UserID: "thomas"}, domain.SessionID(id), domain.SessionArchived)
			results <- err
		}()
	}
	close(gate)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, service.ErrConflict)) || (second == nil && errors.Is(first, service.ErrConflict))) {
		t.Fatalf("expected one transition: %v %v", first, second)
	}
	if len(lifecycleEvents(t, c, "session.archived")) != 1 || len(lifecycleAudit(t, c, "session.archived")) != 1 {
		t.Fatal("duplicate archive history")
	}
}
