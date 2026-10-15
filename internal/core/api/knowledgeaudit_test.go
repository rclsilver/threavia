package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func auditState(t *testing.T, c *core) string {
	t.Helper()
	var state string
	err := c.db.Pool().QueryRow(context.Background(), `SELECT jsonb_build_object(
 'projects',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM projects p),
 'tasks',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tasks t),
 'decisions',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM decisions d),
 'dependencies',(SELECT jsonb_agg(to_jsonb(d) ORDER BY task_id,depends_on_task_id) FROM task_dependencies d),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM audit_entries a),
 'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY global_sequence) FROM events e))::text`).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestProjectAuditEventsAndDeletionAreDurable(t *testing.T) {
	c := newCore(t)
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	header := [2]string{"X-Threavia-Channel", "web"}
	var project domain.Project
	c.mustDo("POST", "/api/v1/projects", map[string]any{"name": "Audit original", "description": "private description"}, &project, 201, header)
	created := awaitFrame(t, web, "project.created")
	if awaitFrame(t, editor, "project.created").id != created.id {
		t.Fatal("clients disagree")
	}
	path := "/api/v1/projects/" + string(project.ID)
	c.mustDo("PATCH", path, map[string]any{"name": "Audit renamed", "instructions": "private instructions"}, nil, 200, header)
	awaitFrame(t, web, "project.updated")
	c.mustDo("POST", path+"/archive", nil, nil, 200, header)
	awaitFrame(t, web, "project.archived")
	c.mustDo("POST", path+"/archive", nil, nil, 409, header)
	c.mustDo("POST", path+"/restore", nil, nil, 200, header)
	awaitFrame(t, web, "project.restored")
	c.asUser("mallory").mustDo("DELETE", path, nil, nil, 404)
	c.mustDo("DELETE", path, nil, nil, 204, header)
	removed := awaitFrame(t, web, "project.deleted")
	if awaitFrame(t, editor, "project.deleted").id != removed.id {
		t.Fatal("deletion notice is not owner scoped")
	}
	remaining := lifecycleEvents(t, c, "project.")
	if len(remaining) != 1 || remaining[0].ProjectID != nil {
		t.Fatal("deletion notice did not survive cascade", remaining)
	}
	replay, closeReplay := c.openStream(t, fmt.Sprint(removed.event.Sequence-1))
	defer closeReplay()
	if awaitFrame(t, replay, "project.deleted").id != removed.id {
		t.Fatal("deletion cannot replay")
	}
	entries := lifecycleAudit(t, c, "project.")
	if len(entries) != 5 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if entry.ProjectID != string(project.ID) || entry.SubjectID != string(project.ID) || entry.ActorID != "thomas" || entry.Channel != "web" {
			t.Fatal(entry)
		}
		if strings.Contains(string(entry.Detail), "private") {
			t.Fatal("audit copied project content")
		}
	}
	var detail map[string]any
	_ = json.Unmarshal(entries[0].Detail, &detail)
	if detail["name"] != "Audit renamed" {
		t.Fatal("deleted name lost", detail)
	}
	other, err := c.store.ListAudit(context.Background(), "mallory", "", 100)
	if err != nil || len(other) != 0 {
		t.Fatal("audit leaked", other, err)
	}
}

func TestProjectHistoryFailureRollsBackEveryMutation(t *testing.T) {
	for _, table := range []string{"audit_entries", "events"} {
		for _, action := range []string{"created", "updated", "archived", "restored", "deleted"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				c := newCore(t)
				project := c.createProject("Keep project")
				path := "/api/v1/projects/" + project
				if action == "restored" {
					c.mustDo("POST", path+"/archive", nil, nil, 200)
				}
				before := auditState(t, c)
				column := "action"
				if table == "events" {
					column = "type"
				}
				lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT reject_project_audit CHECK (%s <> 'project.%s') NOT VALID", table, column, action))
				subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
				defer unsubscribe()
				method, target, body := "POST", path+"/"+strings.TrimSuffix(action, "d"), any(nil)
				switch action {
				case "created":
					target = "/api/v1/projects"
					body = map[string]any{"name": "Must roll back"}
				case "updated":
					method = "PATCH"
					target = path
					body = map[string]any{"name": "Must roll back", "instructions": "do not lose old content"}
				case "archived":
					target = path + "/archive"
				case "restored":
					target = path + "/restore"
				case "deleted":
					method = "DELETE"
					target = path
				}
				if code := c.do(method, target, body, nil); code < 400 {
					t.Fatalf("accepted failed history: %d %s", code, c.lastBody)
				}
				if after := auditState(t, c); after != before {
					t.Fatal("project mutation escaped failed history")
				}
				noLifecyclePublication(t, subscription, "project.")
			})
		}
	}
}

func TestProjectDeletionDoesNotCleanArtifactBytesBeforeAuditCommits(t *testing.T) {
	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	project := domain.ProjectID(c.createProject("Keep bytes"))
	ctx, who := context.Background(), auth.Identity{UserID: "thomas"}
	artifact, err := c.svc.UploadArtifact(ctx, who, project, "kept.txt", "text/plain", strings.NewReader("preserved bytes"), domain.Scope{ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleSQL(t, c, `ALTER TABLE audit_entries ADD CONSTRAINT reject_project_deletion CHECK (action <> 'project.deleted') NOT VALID`)
	if err := c.svc.DeleteProject(ctx, who, project); err == nil {
		t.Fatal("failed audit accepted")
	}
	if objects.count() != 1 {
		t.Fatal("rolled-back deletion removed bytes")
	}
	var count int
	if err := c.db.Pool().QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE id=$1`, artifact.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	lifecycleSQL(t, c, `ALTER TABLE audit_entries DROP CONSTRAINT reject_project_deletion`)
	if err := c.svc.DeleteProject(ctx, who, project); err != nil {
		t.Fatal(err)
	}
	if objects.count() != 0 {
		t.Fatal("committed deletion did not clean bytes")
	}
}

func TestAuditedKnowledgeEventsReachBothClientsAndReplayPrivately(t *testing.T) {
	c := newCore(t)
	project := domain.ProjectID(c.createProject("Both clients"))
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	ctx, who := context.Background(), auth.Identity{UserID: "thomas"}
	task, err := c.svc.CreateTask(ctx, who, project, "Shared task", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := awaitFrame(t, web, "task.created")
	if awaitFrame(t, editor, "task.created").id != first.id || len(lifecycleAudit(t, c, "task.")) != 1 {
		t.Fatal("clients observed uncommitted or different history")
	}
	global, err := c.svc.CreateDecision(ctx, who, "", "Private global memory", "body omitted", domain.DecisionNormal, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	created := awaitFrame(t, web, "decision.created")
	if awaitFrame(t, editor, "decision.created").id != created.id {
		t.Fatal("global event differs across clients")
	}
	replay, closeReplay := c.openStream(t, first.id)
	defer closeReplay()
	if awaitFrame(t, replay, "decision.created").id != created.id {
		t.Fatal("global fact cannot replay")
	}
	if _, err := c.svc.UpdateTask(ctx, auth.Identity{UserID: "mallory"}, task.ID, "foreign", "", domain.TaskDone); err == nil {
		t.Fatal("foreign task mutated")
	}
	if err := c.svc.DeleteDecision(ctx, auth.Identity{UserID: "mallory"}, global.ID); err == nil {
		t.Fatal("foreign global memory deleted")
	}
	events, err := c.store.EventsAfter(ctx, "mallory", 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("events leaked", events, err)
	}
	audit, err := c.store.ListAudit(ctx, "mallory", "", 100)
	if err != nil || len(audit) != 0 {
		t.Fatal("audit leaked", audit, err)
	}
}

func TestKnowledgeAuditRecordsMutationsAndRevivalWithoutContent(t *testing.T) {
	c := newCore(t)
	project := c.createProject("Knowledge audit")
	ctx := domain.WithChannel(context.Background(), domain.Channel("vscode"))
	who := auth.Identity{UserID: "thomas"}
	pid := domain.ProjectID(project)
	a, err := c.svc.CreateTask(ctx, who, pid, "First task", "private task description", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.svc.CreateTask(ctx, who, pid, "Second task", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.UpdateTask(ctx, who, a.ID, "Renamed task", "private replacement", domain.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.AddTaskDependency(ctx, who, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.AddTaskDependency(ctx, who, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.RemoveTaskDependency(ctx, who, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.RemoveTaskDependency(ctx, who, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.svc.DeleteTask(ctx, who, a.ID); err != nil {
		t.Fatal(err)
	}
	tasks := lifecycleAudit(t, c, "task.")
	if len(tasks) != 6 {
		t.Fatal(tasks)
	}
	for _, entry := range tasks {
		if entry.Channel != "vscode" || entry.ActorID != "thomas" || strings.Contains(string(entry.Detail), "private") {
			t.Fatal(entry)
		}
	}
	old, err := c.svc.CreateDecision(ctx, who, pid, "Old decision", "private decision", domain.DecisionNormal, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.svc.CreateDecision(ctx, who, pid, "New decision", "private revised decision", domain.DecisionNormal, &old.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.SetDecisionImportance(ctx, who, next.ID, domain.DecisionImportant); err != nil {
		t.Fatal(err)
	}
	if _, err = c.svc.SetDecisionImportance(ctx, who, next.ID, domain.DecisionImportant); err != nil {
		t.Fatal(err)
	}
	if err = c.svc.DeleteDecision(ctx, who, next.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := c.store.GetDecision(ctx, who.UserID, old.ID)
	if err != nil || restored.Status != domain.DecisionActive {
		t.Fatal(restored, err)
	}
	entries := lifecycleAudit(t, c, "decision.")
	if len(entries) != 6 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if strings.Contains(string(entry.Detail), "private") || entry.Channel != "vscode" {
			t.Fatal(entry)
		}
	}
	var restoredEvent bool
	for _, event := range lifecycleEvents(t, c, "decision.") {
		var p service.DecisionPayload
		_ = json.Unmarshal(event.Payload, &p)
		if p.Restored && p.DecisionID == string(old.ID) && p.Status == "ACTIVE" {
			restoredEvent = true
		}
	}
	if !restoredEvent {
		t.Fatal("revival has no durable notification")
	}
	before := len(entries)
	promoted, err := c.svc.MakeDecisionGlobal(ctx, who, old.ID, nil)
	if err != nil || promoted.ProjectID != "" {
		t.Fatal(promoted, err)
	}
	if _, err = c.svc.MakeDecisionGlobal(ctx, who, promoted.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err = c.svc.DeleteDecision(ctx, who, promoted.ID); err != nil {
		t.Fatal(err)
	}
	if len(lifecycleAudit(t, c, "decision.")) != before+3 {
		t.Fatal("promotion audit duplicated or missing")
	}
	predecessor, _ := c.store.GetDecision(ctx, who.UserID, old.ID)
	if predecessor.Status != domain.DecisionSuperseded {
		t.Fatal("deleted global fact revived project fact")
	}
}

func TestKnowledgeHistoryFailureRollsBackMutationsAndLineage(t *testing.T) {
	for _, table := range []string{"audit_entries", "events"} {
		for _, action := range []string{"task.create", "task.update", "task.delete", "dependency.add", "dependency.remove", "decision.create", "decision.supersede", "decision.pin", "decision.delete", "decision.revive", "decision.promote"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				c := newCore(t)
				project := c.createProject("Rollback knowledge")
				pid := domain.ProjectID(project)
				ctx := context.Background()
				who := auth.Identity{UserID: "thomas"}
				a, err := c.svc.CreateTask(ctx, who, pid, "A", "original task", nil)
				if err != nil {
					t.Fatal(err)
				}
				b, err := c.svc.CreateTask(ctx, who, pid, "B", "", nil)
				if err != nil {
					t.Fatal(err)
				}
				old, err := c.svc.CreateDecision(ctx, who, pid, "Old", "original memory", domain.DecisionNormal, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				var next domain.Decision
				if action == "dependency.remove" {
					if _, err = c.svc.AddTaskDependency(ctx, who, a.ID, b.ID); err != nil {
						t.Fatal(err)
					}
				}
				if action == "decision.revive" {
					next, err = c.svc.CreateDecision(ctx, who, pid, "Next", "replacement", domain.DecisionNormal, &old.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				kind := "task.updated"
				switch action {
				case "task.create":
					kind = "task.created"
				case "task.delete":
					kind = "task.deleted"
				case "decision.create", "decision.promote":
					kind = "decision.created"
				case "decision.supersede":
					kind = "decision.superseded"
				case "decision.pin", "decision.revive":
					kind = "decision.updated"
				case "decision.delete":
					kind = "decision.deleted"
				}
				before := auditState(t, c)
				column := "action"
				if table == "events" {
					column = "type"
				}
				lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT reject_knowledge_audit CHECK (%s <> '%s') NOT VALID", table, column, kind))
				subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
				defer unsubscribe()
				switch action {
				case "task.create":
					_, err = c.svc.CreateTask(ctx, who, pid, "New", "", []domain.TaskID{a.ID})
				case "task.update":
					_, err = c.svc.UpdateTask(ctx, who, a.ID, "Changed", "", domain.TaskDone)
				case "task.delete":
					err = c.svc.DeleteTask(ctx, who, a.ID)
				case "dependency.add":
					_, err = c.svc.AddTaskDependency(ctx, who, a.ID, b.ID)
				case "dependency.remove":
					_, err = c.svc.RemoveTaskDependency(ctx, who, a.ID, b.ID)
				case "decision.create":
					_, err = c.svc.CreateDecision(ctx, who, pid, "New", "", domain.DecisionNormal, nil, nil)
				case "decision.supersede":
					_, err = c.svc.CreateDecision(ctx, who, pid, "Next", "", domain.DecisionNormal, &old.ID, nil)
				case "decision.pin":
					_, err = c.svc.SetDecisionImportance(ctx, who, old.ID, domain.DecisionImportant)
				case "decision.delete":
					err = c.svc.DeleteDecision(ctx, who, old.ID)
				case "decision.revive":
					err = c.svc.DeleteDecision(ctx, who, next.ID)
				case "decision.promote":
					_, err = c.svc.MakeDecisionGlobal(ctx, who, old.ID, nil)
				}
				if err == nil {
					t.Fatal("failed history accepted")
				}
				if after := auditState(t, c); after != before {
					t.Fatal("mutation or history escaped rollback")
				}
				noLifecyclePublication(t, subscription, "")
			})
		}
	}
}

func TestAgentKnowledgeAuditUsesAuthenticatedCallingJob(t *testing.T) {
	c, backend, project, dir := setup(t)
	session := c.startSession(project, c.backendID, dir, "File knowledge")
	start := receive(t, "start", backend.starts)
	ctx := context.Background()
	backend.callCoreTool(t, ctx, start.RunId, start.JobId, "task_create", map[string]any{"title": "Agent task"})
	backend.callCoreTool(t, ctx, start.RunId, start.JobId, "decision_create", map[string]any{"title": "Agent fact", "content": "private content", "importance": "NORMAL"})
	for _, prefix := range []string{"task.", "decision."} {
		entries := lifecycleAudit(t, c, prefix)
		if len(entries) != 1 || entries[0].ActorID != "agent" || entries[0].JobID != start.JobId || entries[0].Channel != "agent" || entries[0].ProjectID != project {
			t.Fatal(session, entries)
		}
	}
}

func TestConcurrentDependencyMutationsCannotCommitACycle(t *testing.T) {
	c := newCore(t)
	project := c.createProject("Graph")
	ctx := context.Background()
	who := auth.Identity{UserID: "thomas"}
	a, err := c.svc.CreateTask(ctx, who, domain.ProjectID(project), "A", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.svc.CreateTask(ctx, who, domain.ProjectID(project), "B", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, pair := range [][2]domain.TaskID{{a.ID, b.ID}, {b.ID, a.ID}} {
		wg.Add(1)
		go func(pair [2]domain.TaskID) {
			defer wg.Done()
			_, err := c.svc.AddTaskDependency(ctx, who, pair[0], pair[1])
			results <- err
		}(pair)
	}
	wg.Wait()
	close(results)
	var success int
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("committed a dependency cycle or lost both changes", success)
	}
	var updates int
	for _, entry := range lifecycleAudit(t, c, "task.") {
		if entry.Action == "task.updated" {
			updates++
		}
	}
	if updates != 1 {
		t.Fatal("audit disagrees with graph", updates)
	}
}
