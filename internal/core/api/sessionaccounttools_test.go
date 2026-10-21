package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestSessionToolsListProjectsAndPaginateSessions(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	localID := c.startSession(projectID, c.backendID, dirID, "Independent local work")
	_ = receive(t, "local work", backend.starts)
	otherProject := c.createProject("another project")
	otherID := c.startSession(otherProject, c.backendID, "", "Independent distant work")
	_ = receive(t, "distant work", backend.starts)
	emptyProject := c.createProject("empty project")
	archivedProject := c.createProject("archived project")
	c.mustDo(http.MethodPost, "/api/v1/projects/"+archivedProject+"/archive", nil, nil, http.StatusOK)
	ctx := context.Background()
	list := func(input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_list", input)
	}
	local := list(map[string]any{})
	if len(local["sessions"].([]any)) != 2 || len(local["projects"].([]any)) != 1 {
		t.Fatalf("default list must include every session in the current project: %v", local)
	}
	remote := list(map[string]any{"projectId": otherProject})
	if items := remote["sessions"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != otherID {
		t.Fatalf("project filter returned wrong sessions: %v", remote)
	}
	managed := list(map[string]any{"scope": "managed"})
	if len(managed["sessions"].([]any)) != 0 {
		t.Fatalf("independent sessions must not become delegates: %v", managed)
	}
	first := list(map[string]any{"scope": "all", "limit": 1})
	seenProjects := map[any]bool{}
	for _, entry := range first["projects"].([]any) {
		project := entry.(map[string]any)
		seenProjects[project["id"]] = true
		if project["name"] == "" || project["instructions"] != nil {
			t.Fatalf("project directory should include names, not instructions: %v", project)
		}
	}
	if !seenProjects[emptyProject] || seenProjects[archivedProject] {
		t.Fatalf("empty projects should be discoverable and archived projects hidden: %v", first)
	}
	seen := map[any]bool{}
	page := first
	for {
		items := page["sessions"].([]any)
		if len(items) != 1 {
			t.Fatalf("limit was ignored: %v", page)
		}
		id := items[0].(map[string]any)["id"]
		if seen[id] {
			t.Fatalf("session appeared on multiple pages: %s", id)
		}
		seen[id] = true
		next, more := page["nextOffset"]
		if !more {
			break
		}
		// Activity on an older session must not move it across page offsets.
		if len(seen) == 1 {
			c.mustDo(http.MethodPost, "/api/v1/sessions/"+managerID+"/messages", map[string]any{"message": "Still working"}, nil, http.StatusCreated)
		}
		page = list(map[string]any{"scope": "all", "limit": 1, "offset": next})
	}
	if len(seen) != 3 || !seen[managerID] || !seen[localID] || !seen[otherID] {
		t.Fatalf("pagination lost sessions: %v", seen)
	}
	archived := list(map[string]any{"scope": "all", "includeArchived": true})
	foundArchived := false
	for _, entry := range archived["projects"].([]any) {
		foundArchived = foundArchived || entry.(map[string]any)["id"] == archivedProject
	}
	if !foundArchived {
		t.Fatal("includeArchived did not include the archived project")
	}
	for _, invalid := range []map[string]any{
		{"scope": "invalid"}, {"scope": true}, {"scope": "all", "projectId": projectID},
		{"projectId": ""}, {"projectId": false}, {"limit": 0}, {"limit": 101},
		{"offset": -1}, {"offset": 0.5}, {"includeArchived": "yes"},
	} {
		backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_list", invalid)
	}
}

func TestSessionToolsSendAndReadAcrossProjects(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	targetProject := c.createProject("message destination")
	targetID := c.startSession(targetProject, c.backendID, "", "Original target assignment")
	target := receive(t, "the target", backend.starts)
	ctx := context.Background()
	input := map[string]any{"sessionId": targetID, "message": "Inspect this finding", "idempotencyKey": "finding"}
	for range 2 {
		backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", input)
	}
	if len(c.snapshot(targetID).Jobs) != 2 || c.countEvents(targetID, "user.message") != 2 {
		t.Fatal("retry must create exactly one follow-up in the target session")
	}
	read := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": targetID})
	found := false
	for _, entry := range read["events"].([]any) {
		event := entry.(map[string]any)
		payload, _ := event["payload"].(map[string]any)
		if event["type"] == "user.message" && payload["text"] == "Inspect this finding" {
			found = payload["actorJobId"] == manager.GetJobId() && event["projectId"] == targetProject
		}
	}
	if !found {
		t.Fatalf("cross-project message lost its origin or target: %v", read)
	}
	completeDelegatedJob(t, c, backend, target)
	followup := receive(t, "the target followup", backend.starts)
	if followup.GetSessionId() != targetID || followup.GetRunId() != target.GetRunId() || followup.GetProjectId() != targetProject {
		t.Fatalf("message started work in the wrong scope: %+v", followup)
	}
	if !strings.Contains(followup.GetPrompt(), "Message from an agent") || followup.GetExecutionPolicy().String() != target.GetExecutionPolicy().String() {
		t.Fatalf("message lost agent provenance or changed the recipient policy: %+v", followup)
	}
	var session domain.Session
	session, err := c.store.GetSession(ctx, "thomas", domain.SessionID(targetID))
	if err != nil || session.ManagerSessionID != nil {
		t.Fatalf("messaging should not adopt an independent session: %+v (%v)", session, err)
	}
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", map[string]any{"sessionId": targetID, "message": "   "})
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", map[string]any{"sessionId": targetID, "message": "Hello", "delivery": "invalid"})
}

func TestSessionToolsCreateAcrossProjects(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	targetProject := c.createProject("creation destination")
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+targetProject, map[string]any{"instructions": "Use target project instructions."}, nil, http.StatusOK)
	var directory idOnly
	c.mustDo(http.MethodPost, "/api/v1/projects/"+targetProject+"/directories", map[string]any{"name": "target checkout"}, &directory, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/directories/"+directory.ID+"/bindings", map[string]any{"backendInstanceId": c.backendID, "path": "/target/checkout"}, nil, http.StatusOK)
	ctx := context.Background()
	for _, invalid := range []map[string]any{
		{"projectId": targetProject, "workingDirectoryId": dirID, "message": "Wrong project directory"},
		{"projectId": false, "message": "Invalid target"},
		{"projectId": targetProject, "workingDirectoryId": true, "message": "Invalid directory"},
	} {
		backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_create", invalid)
	}
	input := map[string]any{"projectId": targetProject, "workingDirectoryId": directory.ID, "message": "Inspect the destination", "title": "Destination task", "idempotencyKey": "target"}
	childID, child := delegatedSession(t, backend, manager, input)
	if child.GetProjectId() != targetProject || child.GetProjectContext().GetWorkingDirectoryPath() != "/target/checkout" ||
		!strings.Contains(child.GetProjectContext().GetProjectInstructions(), "Use target project instructions.") ||
		child.GetExecutionPolicy().String() != manager.GetExecutionPolicy().String() {
		t.Fatalf("target context or inherited execution policy is wrong: %+v", child)
	}
	replayed := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_create", input)
	if replayed["session"].(map[string]any)["id"] != childID {
		t.Fatal("creation retry duplicated the cross-project session")
	}
	expectNothing(t, "duplicate creation dispatch", backend.starts)
	noDirectory, _ := delegatedSession(t, backend, manager, map[string]any{"projectId": targetProject, "message": "No inherited directory"})
	session, err := c.store.GetSession(ctx, "thomas", domain.SessionID(noDirectory))
	if err != nil || session.WorkingDirectoryID != nil {
		t.Fatalf("cross-project creation inherited the source checkout: %+v (%v)", session, err)
	}
	managed := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_list", map[string]any{"scope": "managed", "projectId": targetProject})
	if len(managed["sessions"].([]any)) != 2 {
		t.Fatalf("cross-project delegates were not discoverable: %v", managed)
	}
	// The same creation key in the source project must be a distinct operation.
	localID, _ := delegatedSession(t, backend, manager, map[string]any{"message": "Local assignment", "idempotencyKey": "target"})
	if localID == childID {
		t.Fatal("creation keys collided across target projects")
	}
	completeDelegatedJob(t, c, backend, child)
	waitUntil(t, "cross-project completion callback", func() bool { return len(c.snapshot(manager.GetSessionId()).Jobs) == 2 })
	state := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_wait", map[string]any{"sessionId": childID, "timeoutSeconds": 0})
	encoded, _ := json.Marshal(state["jobs"])
	if !strings.Contains(string(encoded), "COMPLETED") {
		t.Fatalf("cross-project completion not observable: %v", state)
	}
}
