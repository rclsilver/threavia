package api_test

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	sdktools "github.com/rclsilver/threavia/pkg/backend-sdk/tools"
)

// approveCall plays the backend gate and the person for one validated Core
// Tool call: the backend asks in the calling Job, the person approves on the
// web, and Core then accepts that exact call.
func approveCall(t *testing.T, c *core, backend *fakeBackend, start *backendv1.StartJob, requestID, tool string, input map[string]any) {
	t.Helper()
	ctx := context.Background()
	payload, err := structpb.NewStruct(map[string]any{"tool": "mcp__threavia__" + tool, "input": input})
	if err != nil {
		t.Fatal(err)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, start.GetRunId(), start.GetJobId(), &backendv1.ValidationRequested{
			RequestId: requestID, Title: "Approve " + tool, RequestPayload: payload,
		})
	}))
	var validationID string
	waitUntil(t, "the validation of "+tool, func() bool {
		for _, v := range c.snapshot(start.GetSessionId()).Attention.Validations {
			validationID = v.ID
			return true
		}
		return false
	})
	c.mustDo(http.MethodPost, "/api/v1/validations/"+validationID+"/resolve", map[string]any{"approved": true, "channel": "web"}, nil, http.StatusOK)
	_ = receive(t, "the approval of "+tool, backend.validations)
}

// Validated tools are asked about in every mode, and a call without the
// person's receipt for that exact input is refused by Core itself.
func TestManageToolsDeclareAndEnforceValidation(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Tidy up")
	start := receive(t, "the job", backend.starts)
	validated := map[string]bool{}
	for _, spec := range start.GetProjectContext().GetTools() {
		validated[spec.GetName()] = spec.GetRequiresValidation()
	}
	for _, name := range []string{"decision_delete", "task_delete", "artifact_delete", "session_control", "session_delete", "job_delete", "project_archive", "project_delete"} {
		if !validated[name] {
			t.Errorf("%s must require validation", name)
		}
	}
	for _, name := range []string{"decision_list", "task_list", "artifact_list", "project_list", "audit_read", "session_pin"} {
		if _, ok := validated[name]; !ok {
			t.Errorf("%s is not offered to the job", name)
		} else if validated[name] {
			t.Errorf("%s must not require validation", name)
		}
	}

	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	task := call("task_create", map[string]any{"title": "Mistake"})["taskId"].(string)
	input := map[string]any{"taskId": task}
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "task_delete", input)
	// A receipt for another input does not cover this one.
	approveCall(t, c, backend, start, "other-task", "task_delete", map[string]any{"taskId": "00000000-0000-0000-0000-000000000000"})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "task_delete", input)
	approveCall(t, c, backend, start, "this-task", "task_delete", input)
	call("task_delete", input)
	if got := call("task_list", map[string]any{"includeDone": true}); len(got["tasks"].([]any)) != 0 {
		t.Fatalf("task was not deleted: %v", got)
	}
}

func TestManageToolsTasksAndDecisions(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Plan")
	start := receive(t, "the job", backend.starts)
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}

	first := call("task_create", map[string]any{"title": "Design", "description": "Sketch it"})["taskId"].(string)
	second := call("task_create", map[string]any{"title": "Build"})["taskId"].(string)
	call("task_dependency_add", map[string]any{"taskId": second, "dependsOn": first})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "task_dependency_add", map[string]any{"taskId": first, "dependsOn": second})
	read := call("task_read", map[string]any{"taskId": first})
	if read["description"] != "Sketch it" || len(read["dependents"].([]any)) != 1 {
		t.Fatalf("task_read = %v, want its description and the task waiting on it", read)
	}
	call("task_complete", map[string]any{"taskId": first})
	if got := call("task_list", map[string]any{}); len(got["tasks"].([]any)) != 1 {
		t.Fatalf("task_list without done = %v", got)
	}
	if got := call("task_list", map[string]any{"status": "DONE"}); len(got["tasks"].([]any)) != 1 || got["tasks"].([]any)[0].(map[string]any)["taskId"] != first {
		t.Fatalf("task_list DONE = %v", got)
	}
	call("task_dependency_remove", map[string]any{"taskId": second, "dependsOn": first})
	if deps := call("task_read", map[string]any{"taskId": second})["dependsOn"].([]any); len(deps) != 0 {
		t.Fatalf("dependency was not removed: %v", deps)
	}

	original := call("decision_create", map[string]any{"title": "Use PostgreSQL", "content": "It is there"})["decisionId"].(string)
	replacement := call("decision_create", map[string]any{"title": "Use PostgreSQL 17", "content": "Newer", "supersedes": original})["decisionId"].(string)
	if got := call("decision_list", map[string]any{}); len(got["decisions"].([]any)) != 1 {
		t.Fatalf("active decisions = %v", got)
	}
	if got := call("decision_list", map[string]any{"includeSuperseded": true}); len(got["decisions"].([]any)) != 2 {
		t.Fatalf("decision history = %v", got)
	}
	if got := call("decision_read", map[string]any{"decisionId": original}); got["supersededBy"] != replacement || got["content"] != "It is there" {
		t.Fatalf("decision_read original = %v", got)
	}
	if got := call("decision_read", map[string]any{"decisionId": replacement}); len(got["replaced"].([]any)) != 1 {
		t.Fatalf("decision_read replacement = %v", got)
	}
	if got := call("decision_set_importance", map[string]any{"decisionId": replacement, "importance": "IMPORTANT"}); got["importance"] != "IMPORTANT" {
		t.Fatalf("importance = %v", got)
	}
	input := map[string]any{"decisionId": replacement}
	approveCall(t, c, backend, start, "delete-decision", "decision_delete", input)
	call("decision_delete", input)
	active := call("decision_list", map[string]any{})["decisions"].([]any)
	if len(active) != 1 || active[0].(map[string]any)["decisionId"] != original {
		t.Fatalf("deleting the replacement must revive the original: %v", active)
	}
	call("decision_create", map[string]any{"title": "Shared", "scope": "global"})
	if got := call("decision_list", map[string]any{"scope": "global"}); len(got["decisions"].([]any)) != 1 {
		t.Fatalf("global decisions = %v", got)
	}

	other := c.asUser("someone-else")
	foreignProject := other.createProject("private")
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "task_list", map[string]any{"projectId": foreignProject})
	backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), "decision_list", map[string]any{"projectId": foreignProject})
}

func TestManageToolsArtifacts(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.startSession(projectID, c.backendID, dirID, "Report")
	manager := receive(t, "the manager", backend.starts)
	childID, _ := delegatedSession(t, backend, manager, map[string]any{"message": "Help"})
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), name, input)
	}
	publish := func(input map[string]any, file []byte) string {
		encoded, _ := structpb.NewStruct(input)
		result, err := backend.sdk.Invoke(ctx, sdktools.Call{RunID: manager.GetRunId(), JobID: manager.GetJobId(), Name: "artifact_publish", Input: encoded, File: file})
		if err != nil {
			t.Fatalf("artifact_publish: %v", err)
		}
		return result.AsMap()["artifactId"].(string)
	}
	own := publish(map[string]any{"path": "notes.md"}, []byte("# Notes\nhello"))
	shared := publish(map[string]any{"path": "brief.txt", "sessionId": childID, "title": "Brief"}, []byte("for the worker"))
	binary := publish(map[string]any{"path": "shot.png"}, []byte("\x89PNG\r\n\x1a\n\x00\x00"))
	// Writing into a conversation it does not manage is refused.
	manualID := c.startSession(projectID, c.backendID, dirID, "Manual")
	_ = receive(t, "the manual job", backend.starts)
	encoded, _ := structpb.NewStruct(map[string]any{"path": "intrusion.txt", "sessionId": manualID})
	if _, err := backend.sdk.Invoke(ctx, sdktools.Call{RunID: manager.GetRunId(), JobID: manager.GetJobId(), Name: "artifact_publish", Input: encoded, File: []byte("x")}); err == nil {
		t.Fatal("publishing into an unmanaged session must be refused")
	}

	if got := call("artifact_list", map[string]any{}); len(got["artifacts"].([]any)) != 3 {
		t.Fatalf("project artifacts = %v", got)
	}
	inChild := call("artifact_list", map[string]any{"sessionId": childID})["artifacts"].([]any)
	if len(inChild) != 1 || inChild[0].(map[string]any)["artifactId"] != shared {
		t.Fatalf("artifacts of the delegate = %v", inChild)
	}
	if c.countEvents(childID, "artifact.created") != 1 {
		t.Fatal("an artifact published into another session must be announced there")
	}
	if got := call("artifact_read", map[string]any{"artifactId": own}); got["content"] != "# Notes\nhello" {
		t.Fatalf("artifact_read text = %v", got)
	}
	if got := call("artifact_read", map[string]any{"artifactId": binary}); got["content"] != nil {
		t.Fatalf("artifact_read binary = %v, want no content", got)
	}
	if got := call("artifact_rename", map[string]any{"artifactId": own, "filename": "../renamed.md"}); got["filename"] != "renamed.md" {
		t.Fatalf("artifact_rename = %v", got)
	}
	input := map[string]any{"artifactId": binary}
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "artifact_delete", input)
	approveCall(t, c, backend, manager, "delete-artifact", "artifact_delete", input)
	call("artifact_delete", input)
	if got := call("artifact_list", map[string]any{}); len(got["artifacts"].([]any)) != 2 {
		t.Fatalf("artifact was not deleted: %v", got)
	}
}

func TestManageToolsSessionsBeyondDelegates(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	manualID := c.startSession(projectID, c.backendID, dirID, "Manual work")
	manual := receive(t, "the manual job", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Delegated"})
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), name, input)
	}
	fail := func(name string, input map[string]any) {
		backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), name, input)
	}

	// A delegate is renamed without validation; a manual session is not.
	if got := call("session_update", map[string]any{"sessionId": childID, "title": "Renamed delegate"}); got["session"].(map[string]any)["title"] != "Renamed delegate" {
		t.Fatalf("session_update title = %v", got)
	}
	fail("session_update", map[string]any{"sessionId": manualID, "title": "Nope"})
	fail("session_cancel", map[string]any{"sessionId": manualID, "jobId": manual.GetJobId()})

	rename := map[string]any{"sessionId": manualID, "action": "update", "title": "Renamed manual"}
	fail("session_control", rename)
	approveCall(t, c, backend, manager, "rename-manual", "session_control", rename)
	if got := call("session_control", rename); got["session"].(map[string]any)["title"] != "Renamed manual" {
		t.Fatalf("session_control update = %v", got)
	}
	cancel := map[string]any{"sessionId": manualID, "action": "cancel", "jobId": manual.GetJobId(), "reason": "obsolete"}
	approveCall(t, c, backend, manager, "cancel-manual", "session_control", cancel)
	call("session_control", cancel)
	_ = receive(t, "the cancellation", backend.cancels)

	// Interface state of this conversation and its delegates needs no
	// validation; another session's goes through session_control.
	call("session_pin", map[string]any{"sessionId": childID, "pinned": true})
	call("session_snooze", map[string]any{"sessionId": childID, "untilChange": true})
	call("session_snooze", map[string]any{"sessionId": childID, "clear": true})
	call("session_mark_read", map[string]any{"sessionId": manager.GetSessionId()})
	for _, tool := range []string{"session_pin", "session_snooze", "session_mark_read"} {
		fail(tool, map[string]any{"sessionId": manualID, "pinned": true, "untilChange": true})
	}
	fail("session_repository", map[string]any{"sessionId": manualID, "fetch": true})
	pin := map[string]any{"sessionId": manualID, "action": "pin", "pinned": true}
	approveCall(t, c, backend, manager, "pin-manual", "session_control", pin)
	if got := call("session_control", pin); got["session"].(map[string]any)["pinnedAt"] == nil {
		t.Fatalf("session_control pin = %v", got)
	}
	call("attention_list", map[string]any{})

	// Adopting is checked by Core too, not only by the backend gate.
	adopt := map[string]any{"sessionId": manualID}
	fail("session_adopt", adopt)
	approveCall(t, c, backend, manager, "adopt-manual", "session_adopt", adopt)
	call("session_adopt", adopt)
	call("session_detach", adopt)

	// A queued message can be dropped once the person approves.
	var queued struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+childID+"/messages", map[string]any{"message": "Later"}, &queued, http.StatusCreated)
	drop := map[string]any{"jobId": queued.ID}
	fail("job_delete", drop)
	approveCall(t, c, backend, manager, "drop-queued", "job_delete", drop)
	call("job_delete", drop)
	if status := c.jobStatus(childID, queued.ID); status != "CANCELLED" {
		t.Fatalf("withdrawn job is %s, want CANCELLED so the timeline says so", status)
	}

	self := map[string]any{"sessionId": manager.GetSessionId()}
	approveCall(t, c, backend, manager, "delete-self", "session_delete", self)
	fail("session_delete", self)

	completeDelegatedJob(t, c, backend, child)
	remove := map[string]any{"sessionId": childID, "deleteArtifacts": true}
	fail("session_delete", remove)
	approveCall(t, c, backend, manager, "delete-child", "session_delete", remove)
	call("session_delete", remove)
	if status := c.do(http.MethodGet, "/api/v1/sessions/"+childID, nil, nil); status != http.StatusNotFound {
		t.Fatalf("deleted session answers %d", status)
	}
}

func TestManageToolsProjectsAndAudit(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Organise")
	start := receive(t, "the job", backend.starts)
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	fail := func(name string, input map[string]any) {
		backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}

	created := call("project_create", map[string]any{"name": "Side", "description": "A side project"})
	sideID := created["projectId"].(string)
	// Instructions reach every later job of a project: changing them waits for
	// the person, and one approval covers one call.
	instruct := map[string]any{"projectId": sideID, "instructions": "Be brief"}
	fail("project_update", instruct)
	approveCall(t, c, backend, start, "instruct-side", "project_update", instruct)
	call("project_update", instruct)
	fail("project_update", instruct)
	if got := call("project_read", map[string]any{"projectId": sideID}); got["instructions"] != "Be brief" || got["description"] != "A side project" {
		t.Fatalf("project_read = %v", got)
	}
	cleared := map[string]any{"projectId": sideID, "description": ""}
	approveCall(t, c, backend, start, "clear-side", "project_update", cleared)
	if got := call("project_update", cleared); got["description"] != "" || got["instructions"] != "Be brief" {
		t.Fatalf("clearing the description = %v", got)
	}
	if got := call("project_list", map[string]any{}); len(got["projects"].([]any)) != 2 {
		t.Fatalf("project_list = %v", got)
	}

	own := map[string]any{"projectId": projectID, "archived": true}
	approveCall(t, c, backend, start, "archive-own", "project_archive", own)
	fail("project_archive", own)
	archive := map[string]any{"projectId": sideID, "archived": true}
	fail("project_archive", archive)
	approveCall(t, c, backend, start, "archive-side", "project_archive", archive)
	if got := call("project_archive", archive); got["status"] != "ARCHIVED" {
		t.Fatalf("project_archive = %v", got)
	}
	remove := map[string]any{"projectId": sideID}
	approveCall(t, c, backend, start, "delete-side", "project_delete", remove)
	call("project_delete", remove)
	if got := call("project_list", map[string]any{"includeArchived": true}); len(got["projects"].([]any)) != 1 {
		t.Fatalf("project was not deleted: %v", got)
	}

	entries := call("audit_read", map[string]any{"limit": 50})["entries"].([]any)
	found := false
	for _, raw := range entries {
		found = found || raw.(map[string]any)["action"] == "project.deleted"
	}
	if !found {
		t.Fatalf("audit_read = %v, want the deletion", entries)
	}
}

// A delegate's validated call goes through: the person approves the manager's
// relay, which resolves the delegate's own request, and Core accepts it.
func TestManageToolsDelegateValidatedThroughRelay(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Clean the backlog"})
	ctx := context.Background()
	task := backend.callCoreTool(t, ctx, child.GetRunId(), child.GetJobId(), "task_create", map[string]any{"title": "Obsolete"})["taskId"].(string)
	input := map[string]any{"taskId": task}

	payload, err := structpb.NewStruct(map[string]any{"tool": "mcp__threavia__task_delete", "input": input})
	if err != nil {
		t.Fatal(err)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, child.GetRunId(), child.GetJobId(), &backendv1.ValidationRequested{
			RequestId: "child-delete", Title: "Delete the task", RequestPayload: payload,
		})
	}))
	waitUntil(t, "the delegate's request", func() bool { return len(c.snapshot(childID).Attention.Validations) == 1 })
	read := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": childID})
	request := read["attention"].(map[string]any)["validations"].([]any)[0].(map[string]any)
	relay := map[string]any{
		"sessionId": childID, "requestId": request["id"], "approved": true,
		"payloadSha256": request["payloadSha256"], "requestPayload": request["requestPayload"],
	}
	approveCall(t, c, backend, manager, "relay-delete", "session_resolve_validation", relay)
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", relay)
	_ = receive(t, "the delegate's approval", backend.validations)

	backend.callCoreTool(t, ctx, child.GetRunId(), child.GetJobId(), "task_delete", input)
	if got := backend.callCoreTool(t, ctx, child.GetRunId(), child.GetJobId(), "task_list", map[string]any{"includeDone": true}); len(got["tasks"].([]any)) != 0 {
		t.Fatalf("task_list = %v, want the task deleted", got)
	}
}
