package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	sdktools "github.com/rclsilver/threavia/pkg/backend-sdk/tools"
)

func delegatedSession(t *testing.T, backend *fakeBackend, manager *backendv1.StartJob, input map[string]any) (string, *backendv1.StartJob) {
	t.Helper()
	created := backend.callCoreTool(t, context.Background(), manager.GetRunId(), manager.GetJobId(), "session_create", input)
	session, ok := created["session"].(map[string]any)
	if !ok {
		t.Fatalf("session_create = %v, want the created session", created)
	}
	id, _ := session["id"].(string)
	if id == "" {
		t.Fatalf("session_create = %v, want a session identifier", created)
	}
	start := receive(t, "the delegated job", backend.starts)
	if start.GetSessionId() != id {
		t.Fatalf("delegated start = %s, want session %s", start.GetSessionId(), id)
	}
	return id, start
}

func completeDelegatedJob(t *testing.T, c *core, backend *fakeBackend, start *backendv1.StartJob) {
	t.Helper()
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "done", nil)
	}))
	waitUntil(t, "the job to complete", func() bool {
		return c.jobStatus(start.GetSessionId(), start.GetJobId()) == "COMPLETED"
	})
}

// A delegated session keeps the manager's scope and permissions before its
// first dispatch, and the relation belongs to the durable Session, not a turn.
func TestSessionToolsCreateInheritAndResumeManager(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate the fix")
	manager := receive(t, "the manager job", backend.starts)
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+managerID+"/policy", map[string]any{
		"mode": "GUARDED", "allowFilesystemWrite": true, "allowGitCommit": false, "allowNetwork": false,
	}, nil, http.StatusOK)
	_ = receive(t, "the manager policy", backend.policies)

	input := map[string]any{"message": "Fix the parser", "title": "Parser fix", "idempotencyKey": "parser"}
	childID, child := delegatedSession(t, backend, manager, input)
	if child.GetProjectId() != projectID {
		t.Fatalf("child project = %s, want %s", child.GetProjectId(), projectID)
	}
	policy := child.GetExecutionPolicy()
	if policy.GetMode() != backendv1.ExecutionMode_EXECUTION_MODE_GUARDED || !policy.GetAllowFilesystemWrite() || policy.GetAllowGitCommit() || policy.GetAllowNetwork() {
		t.Fatalf("child was dispatched with policy %+v, want the manager's restrained policy", policy)
	}
	var snapshot struct {
		Session struct {
			ManagerSessionID   string `json:"managerSessionId"`
			WorkingDirectoryID string `json:"workingDirectoryId"`
			Title              string `json:"title"`
		} `json:"session"`
		Runs []struct {
			BackendInstanceID string `json:"backendInstanceId"`
		} `json:"runs"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+childID, nil, &snapshot, http.StatusOK)
	if snapshot.Session.ManagerSessionID != managerID || snapshot.Session.WorkingDirectoryID != dirID || snapshot.Session.Title != "Parser fix" || len(snapshot.Runs) != 1 || snapshot.Runs[0].BackendInstanceID != c.backendID {
		t.Fatalf("delegated session = %+v, want manager, directory and backend inherited", snapshot)
	}

	replayed := backend.callCoreTool(t, context.Background(), manager.GetRunId(), manager.GetJobId(), "session_create", input)
	if replayed["session"].(map[string]any)["id"] != childID {
		t.Fatalf("retry returned %v, want the same child %s", replayed, childID)
	}
	expectNothing(t, "a second delegated start on retry", backend.starts)

	completeDelegatedJob(t, c, backend, manager)
	backend.callCoreToolExpectingFailure(t, context.Background(), manager.GetRunId(), manager.GetJobId(), "session_create", map[string]any{"message": "Work from a finished turn"})
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+managerID+"/messages", map[string]any{"message": "Check your delegates"}, nil, http.StatusCreated)
	resumed := receive(t, "the manager's next job", backend.starts)
	listed := backend.callCoreTool(t, context.Background(), resumed.GetRunId(), resumed.GetJobId(), "session_list", map[string]any{})
	sessions, _ := listed["sessions"].([]any)
	if len(sessions) != 1 || sessions[0].(map[string]any)["id"] != childID {
		t.Fatalf("resumed manager's children = %v, want %s", listed, childID)
	}
	read := backend.callCoreTool(t, context.Background(), resumed.GetRunId(), resumed.GetJobId(), "session_read", map[string]any{"sessionId": childID})
	if read["session"].(map[string]any)["id"] != childID {
		t.Fatalf("resumed manager read = %v, want the durable child", read)
	}
}

// Sharing an owner or a project does not make arbitrary sessions delegates.
func TestSessionToolsRespectDirectDelegationScope(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Inspect the parser"})
	siblingID, _ := delegatedSession(t, backend, manager, map[string]any{"message": "Inspect the writer"})
	unrelated := c.startSession(projectID, c.backendID, dirID, "Independent work")
	_ = receive(t, "the independent job", backend.starts)
	foreign := c.startSession(c.createProject("other"), c.backendID, "", "Other project")
	_ = receive(t, "the other project's job", backend.starts)
	ctx := context.Background()

	for _, id := range []string{managerID, unrelated, foreign} {
		for _, tool := range []string{"session_read", "session_wait", "session_send", "session_archive"} {
			backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), tool,
				map[string]any{"sessionId": id, "message": "interfere", "archived": true, "timeoutSeconds": 0})
		}
	}
	for _, id := range []string{childID, siblingID, managerID} {
		backend.callCoreToolExpectingFailure(t, ctx, child.GetRunId(), child.GetJobId(), "session_read", map[string]any{"sessionId": id})
	}
	backend.callCoreToolExpectingFailure(t, ctx, child.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": childID})

	other := c.asUser("other-owner")
	otherProject := other.createProject("private")
	otherBackendID, credential := other.registerBackend("private backend")
	otherBackend := other.connectBackend(credential)
	otherID := other.startSession(otherProject, otherBackendID, "", "Private work")
	_ = receive(t, "the other owner's job", otherBackend.starts)
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": otherID})
}

// The manager may answer a delegate's information request, with agent
// provenance, but cannot turn that tool into an approval bypass.
func TestSessionToolsAnswerInformationAndKeepPermissionsHuman(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Deploy to staging"})
	siblingID, _ := delegatedSession(t, backend, manager, map[string]any{"message": "Check staging config"})
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, child.GetRunId(), child.GetJobId(), &backendv1.UserInputRequested{
			RequestId: "environment", Prompt: "Which environment?", Choices: []string{"staging", "production"},
		})
	}))
	waitUntil(t, "the child input", func() bool { return len(c.snapshot(childID).Attention.UserInputs) == 1 })
	requestID := c.snapshot(childID).Attention.UserInputs[0].ID
	answer := map[string]any{"sessionId": childID, "requestId": requestID, "value": "staging"}
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_answer", map[string]any{
		"sessionId": managerID, "requestId": requestID, "value": "staging",
	})
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_answer", map[string]any{
		"sessionId": siblingID, "requestId": requestID, "value": "staging",
	})
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_answer", map[string]any{
		"sessionId": childID, "requestId": requestID, "value": "not an offered choice",
	})
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_answer", answer)
	resolved := receive(t, "the delegated answer", backend.inputs)
	if resolved.GetJobId() != child.GetJobId() || resolved.GetRequestId() != "environment" || resolved.GetValue() != "staging" {
		t.Fatalf("input resolution = %+v, want the child's staging answer", resolved)
	}
	waitUntil(t, "the child to resume", func() bool { return c.jobStatus(childID, child.GetJobId()) == "RUNNING" })
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_answer", answer)
	var receipt map[string]any
	for _, event := range c.snapshot(childID).Events {
		if event.Type == "user_input.resolved" {
			if err := json.Unmarshal(event.Payload, &receipt); err != nil {
				t.Fatal(err)
			}
		}
	}
	if receipt["channel"] != "agent" || receipt["actorJobId"] != manager.GetJobId() {
		t.Fatalf("answer receipt = %v, want the manager Job and agent channel", receipt)
	}

	payload, _ := structpb.NewStruct(map[string]any{"tool": "git push"})
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, child.GetRunId(), child.GetJobId(), &backendv1.ValidationRequested{
			RequestId: "push", Title: "Push the fix", RequestPayload: payload,
		})
	}))
	waitUntil(t, "the child validation", func() bool { return len(c.snapshot(childID).Attention.Validations) == 1 })
	validationID := c.snapshot(childID).Attention.Validations[0].ID
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_answer", map[string]any{
		"sessionId": childID, "requestId": validationID, "value": "approved",
	})
	expectNothing(t, "a validation approved by the manager", backend.validations)
	if len(c.snapshot(childID).Attention.Validations) != 1 {
		t.Fatal("the permission request must remain pending for the human")
	}
}

func TestSessionToolsQueueCancelAndArchive(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Inspect the parser"})
	ctx := context.Background()
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_archive", map[string]any{"sessionId": childID, "archived": true})
	message := map[string]any{"sessionId": childID, "message": "Then inspect the writer", "idempotencyKey": "followup"}
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", message)
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", message)
	if len(c.snapshot(childID).Jobs) != 2 || c.countEvents(childID, "user.message") != 2 {
		t.Fatal("a retried followup must enqueue exactly one new child Job and message")
	}
	for _, delivery := range []string{"NEXT", "NOW"} {
		backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", map[string]any{
			"sessionId": childID, "message": "Interrupt", "delivery": delivery,
		})
	}
	completeDelegatedJob(t, c, backend, child)
	next := receive(t, "the queued child followup", backend.starts)
	if next.GetSessionId() != childID || next.GetRunId() != child.GetRunId() {
		t.Fatalf("followup = %+v, want the same child Run", next)
	}
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_cancel", map[string]any{
		"sessionId": childID, "jobId": manager.GetJobId(), "reason": "wrong job",
	})
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_cancel", map[string]any{
		"sessionId": childID, "jobId": next.GetJobId(), "reason": "No longer needed",
	})
	cancelled := receive(t, "the child cancellation", backend.cancels)
	if cancelled.GetJobId() != next.GetJobId() {
		t.Fatalf("cancel = %+v, want the child's Job", cancelled)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCancelled(ctx, next.GetRunId(), next.GetJobId())
	}))
	waitUntil(t, "the cancelled child", func() bool { return c.jobStatus(childID, next.GetJobId()) == "CANCELLED" })
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_archive", map[string]any{"sessionId": childID, "archived": true})
	listed := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_list", map[string]any{})
	if children, _ := listed["sessions"].([]any); len(children) != 0 {
		t.Fatalf("active children = %v, want none", listed)
	}
	listed = backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_list", map[string]any{"includeArchived": true})
	if children, _ := listed["sessions"].([]any); len(children) != 1 {
		t.Fatalf("all children = %v, want the archived one", listed)
	}
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", map[string]any{
		"sessionId": childID, "message": "Cannot reach an archived child",
	})
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_archive", map[string]any{"sessionId": childID, "archived": false})
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_send", map[string]any{"sessionId": childID, "message": "Resume"})
	_ = receive(t, "the restored child's Job", backend.starts)
}

// A wait is invoked over the same control stream as the child events. Keeping
// that stream free is essential: otherwise the awaited event never reaches Core.
func TestSessionToolsWaitDoesNotBlockItsBackendStream(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Inspect the parser"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	read := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": childID})
	cursor := read["cursor"]
	encoded, err := structpb.NewStruct(map[string]any{"sessionId": childID, "afterSequence": cursor, "timeoutSeconds": 3})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		value *structpb.Struct
		err   error
	}
	results := make(chan result, 1)
	go func() {
		value, err := backend.sdk.Invoke(ctx, sdktools.Call{RunID: manager.GetRunId(), JobID: manager.GetJobId(), Name: "session_wait", Input: encoded})
		results <- result{value, err}
	}()
	// Give the wait a chance to enter its pending state before emitting. The
	// received result must reflect the new event even if scheduling reverses it.
	time.Sleep(50 * time.Millisecond)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.AgentMessage(ctx, child.GetRunId(), child.GetJobId(), "Parser analysis ready")
	}))
	got := receive(t, "the wait result while the same backend remains active", results)
	if got.err != nil {
		t.Fatalf("session_wait: %v", got.err)
	}
	value := got.value.AsMap()
	if value["timedOut"] != false {
		t.Fatalf("wait = %v, want an event, not a timeout", value)
	}
	found := false
	for _, item := range value["events"].([]any) {
		event := item.(map[string]any)
		payload, _ := event["payload"].(map[string]any)
		if event["type"] == "agent.message" && payload["text"] == "Parser analysis ready" {
			found = true
		}
	}
	if !found {
		t.Fatalf("wait result = %v, want the new child message", value)
	}

	timed := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_wait", map[string]any{
		"sessionId": childID, "afterSequence": value["cursor"], "timeoutSeconds": 1,
	})
	if timed["timedOut"] != true {
		t.Fatalf("idle wait = %v, want timedOut", timed)
	}
}

func TestSessionToolsReadPaginatesOnlyTheChildHistory(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Inspect the parser"})
	ctx := context.Background()
	for _, message := range []string{"First finding", "Second finding", "Third finding"} {
		backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
			return backend.events.AgentMessage(ctx, child.GetRunId(), child.GetJobId(), message)
		}))
	}
	waitUntil(t, "the child findings", func() bool { return c.countEvents(childID, "agent.message") == 3 })
	childCursor := int64(0)
	for _, event := range c.snapshot(childID).Events {
		if event.Sequence > childCursor {
			childCursor = event.Sequence
		}
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.AgentMessage(ctx, manager.GetRunId(), manager.GetJobId(), "Manager-only message")
	}))
	waitUntil(t, "the manager message", func() bool { return c.countEvents(managerID, "agent.message") == 1 })
	first := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": childID, "limit": 2})
	page, _ := first["events"].([]any)
	if len(page) != 2 || first["cursor"] != float64(childCursor) {
		t.Fatalf("first page = %v, want two child events and child cursor %d", first, childCursor)
	}
	seen := map[any]bool{}
	for _, entry := range page {
		seen[entry.(map[string]any)["sequence"]] = true
	}
	before := first["beforeSequence"]
	if before == nil {
		t.Fatalf("first page = %v, want a backwards history cursor", first)
	}
	second := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": childID, "limit": 2, "before": before})
	older, _ := second["events"].([]any)
	if len(older) != 2 {
		t.Fatalf("older page = %v, want two earlier child events", second)
	}
	for _, entry := range older {
		event := entry.(map[string]any)
		if seen[event["sequence"]] {
			t.Fatalf("event %v appeared on both history pages", event)
		}
		if event["sessionId"] != childID {
			t.Fatalf("history included a foreign event: %v", event)
		}
	}
}

// The manager's permission relay is declared as a mandatory human validation,
// and the child's immutable action must survive that relay without alteration.
func TestSessionToolsPermissionRelayKeepsTheExactChildAction(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	mandatory := false
	for _, spec := range manager.GetProjectContext().GetTools() {
		if spec.GetName() == "session_resolve_validation" {
			mandatory = spec.GetRequiresValidation()
		}
	}
	if !mandatory {
		t.Fatal("session_resolve_validation must require a human validation in every execution mode")
	}
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Prepare the fix"})
	ctx := context.Background()
	payload, _ := structpb.NewStruct(map[string]any{"tool": "Bash", "input": map[string]any{"command": "git push origin fix-parser"}})
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, child.GetRunId(), child.GetJobId(), &backendv1.ValidationRequested{
			RequestId: "push-fix", Title: "Push the parser fix", RequestPayload: payload,
		})
	}))
	waitUntil(t, "the child's push request", func() bool { return len(c.snapshot(childID).Attention.Validations) == 1 })
	read := backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_read", map[string]any{"sessionId": childID})
	request := read["attention"].(map[string]any)["validations"].([]any)[0].(map[string]any)
	valid := map[string]any{
		"sessionId": childID, "requestId": request["id"], "approved": true,
		"payloadSha256": request["payloadSha256"], "requestPayload": request["requestPayload"],
		"note": "The person approved this exact push through the manager",
	}
	wrongHash := map[string]any{}
	for key, value := range valid {
		wrongHash[key] = value
	}
	wrongHash["payloadSha256"] = "mismatched action hash"
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", wrongHash)
	wrongAction := map[string]any{}
	for key, value := range valid {
		wrongAction[key] = value
	}
	wrongAction["requestPayload"] = map[string]any{"tool": "Bash", "input": map[string]any{"command": "git push --force origin main"}}
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", wrongAction)
	if len(c.snapshot(childID).Attention.Validations) != 1 {
		t.Fatal("a mismatched relay must leave the original child permission pending")
	}
	// Core requires a matching human approval receipt as well as the mandatory
	// provider gate, so even a direct backend call cannot approve the child.
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", valid)
	gatePayload, err := structpb.NewStruct(map[string]any{
		"tool": "mcp__threavia__session_resolve_validation", "input": valid,
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, manager.GetRunId(), manager.GetJobId(), &backendv1.ValidationRequested{
			RequestId: "relay-push", Title: "Approve the delegated parser push", RequestPayload: gatePayload,
		})
	}))
	waitUntil(t, "the human approval in the manager conversation", func() bool {
		return len(c.snapshot(manager.GetSessionId()).Attention.Validations) == 1
	})
	gateID := c.snapshot(manager.GetSessionId()).Attention.Validations[0].ID
	c.mustDo(http.MethodPost, "/api/v1/validations/"+gateID+"/resolve", map[string]any{
		"approved": true, "channel": "web",
	}, nil, http.StatusOK)
	humanResolution := receive(t, "the human approval to reach the manager", backend.validations)
	if humanResolution.GetJobId() != manager.GetJobId() || !humanResolution.GetApproved() {
		t.Fatalf("manager approval = %+v, want the person's relay approval", humanResolution)
	}
	changedDecision := map[string]any{}
	for key, value := range valid {
		changedDecision[key] = value
	}
	changedDecision["approved"] = false
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", changedDecision)
	backend.callCoreTool(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", valid)
	resolved := receive(t, "the exact child's push decision", backend.validations)
	if !resolved.GetApproved() || resolved.GetRequestId() != "push-fix" || resolved.GetJobId() != child.GetJobId() {
		t.Fatalf("relayed permission = %+v, want the exact approved child request", resolved)
	}
	waitUntil(t, "the child to resume after approval", func() bool { return c.jobStatus(childID, child.GetJobId()) == "RUNNING" })
	backend.callCoreToolExpectingFailure(t, ctx, manager.GetRunId(), manager.GetJobId(), "session_resolve_validation", valid)
}

// When the manager finished its turn, a delegated question brings it back so
// the person never has to open a worker conversation to answer routine input.
func TestSessionToolsChildQuestionWakesTheIdleManager(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Deploy to staging"})
	completeDelegatedJob(t, c, backend, manager)
	ctx := context.Background()
	event := backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, child.GetRunId(), child.GetJobId(), &backendv1.UserInputRequested{
			RequestId: "environment", Prompt: "Which environment?", Choices: []string{"staging", "production"},
		})
	})
	backend.emit(t, ctx, event)
	awakened := receive(t, "the manager woken by its child's question", backend.starts)
	if awakened.GetSessionId() != managerID || awakened.GetRunId() != manager.GetRunId() {
		t.Fatalf("woken Job = %+v, want the original manager conversation", awakened)
	}
	waitUntil(t, "the child's input to remain pending", func() bool { return len(c.snapshot(childID).Attention.UserInputs) == 1 })
	var childAttention struct {
		Attention struct {
			UserInputs []struct {
				Notify bool `json:"notify"`
			} `json:"userInputs"`
		} `json:"attention"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+childID, nil, &childAttention, http.StatusOK)
	if len(childAttention.Attention.UserInputs) != 1 || childAttention.Attention.UserInputs[0].Notify {
		t.Fatalf("child input relevance = %+v, want a question handled by the manager without notifying the human directly", childAttention)
	}
	pending := c.snapshot(childID).Attention.UserInputs[0].ID
	backend.callCoreTool(t, ctx, awakened.GetRunId(), awakened.GetJobId(), "session_answer", map[string]any{
		"sessionId": childID, "requestId": pending, "value": "staging",
	})
	_ = receive(t, "the manager answer to reach the child", backend.inputs)
	backend.emit(t, ctx, event)
	expectNothing(t, "a duplicate manager wake for the replayed input", backend.starts)
	if len(c.snapshot(managerID).Jobs) != 2 {
		t.Fatal("replaying a child question must not enqueue another manager turn")
	}
}

// An input received during the manager's last running step must not disappear
// into the gap between skipping an immediate wake and completing that turn.
func TestSessionToolsPendingChildQuestionWakesFinishingManager(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Deploy to staging"})
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, child.GetRunId(), child.GetJobId(), &backendv1.UserInputRequested{
			RequestId: "environment", Prompt: "Which environment?", Choices: []string{"staging", "production"},
		})
	}))
	waitUntil(t, "the pending child question while its manager still runs", func() bool {
		return len(c.snapshot(childID).Attention.UserInputs) == 1 && c.jobStatus(managerID, manager.GetJobId()) == "RUNNING"
	})
	waitUntil(t, "the manager callback to be queued until its current Job finishes", func() bool {
		jobs := c.snapshot(managerID).Jobs
		return len(jobs) == 2 && jobs[1].Status == "QUEUED"
	})
	expectNothing(t, "an unnecessary second turn for the active manager", backend.starts)
	completeDelegatedJob(t, c, backend, manager)
	awakened := receive(t, "the manager to pick up its already pending child question", backend.starts)
	if awakened.GetSessionId() != managerID || awakened.GetRunId() != manager.GetRunId() {
		t.Fatalf("woken Job = %+v, want the original manager conversation", awakened)
	}
	pending := c.snapshot(childID).Attention.UserInputs[0].ID
	backend.callCoreTool(t, ctx, awakened.GetRunId(), awakened.GetJobId(), "session_answer", map[string]any{
		"sessionId": childID, "requestId": pending, "value": "staging",
	})
	_ = receive(t, "the finishing manager's answer to reach its child", backend.inputs)
}

func TestSessionToolsChildCompletionQueuesOneManagerCallback(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	managerID := c.startSession(projectID, c.backendID, dirID, "Coordinate")
	manager := receive(t, "the manager", backend.starts)
	childID, child := delegatedSession(t, backend, manager, map[string]any{"message": "Inspect the parser"})
	completeDelegatedJob(t, c, backend, child)
	waitUntil(t, "a completed worker callback to queue behind the active manager", func() bool {
		jobs := c.snapshot(managerID).Jobs
		return len(jobs) == 2 && jobs[1].Status == "QUEUED"
	})
	expectNothing(t, "a second manager Job starting before the current one finishes", backend.starts)
	completeDelegatedJob(t, c, backend, manager)
	awakened := receive(t, "the queued worker result to resume its manager", backend.starts)
	if awakened.GetSessionId() != managerID || awakened.GetRunId() != manager.GetRunId() {
		t.Fatalf("woken Job = %+v, want the original manager conversation", awakened)
	}
	read := backend.callCoreTool(t, context.Background(), awakened.GetRunId(), awakened.GetJobId(), "session_read", map[string]any{"sessionId": childID})
	jobs := read["jobs"].([]any)
	if len(jobs) != 1 || jobs[0].(map[string]any)["status"] != "COMPLETED" {
		t.Fatalf("worker state = %v, want the completed assignment", read)
	}
	if len(c.snapshot(managerID).Jobs) != 2 {
		t.Fatal("one worker completion must enqueue exactly one manager callback")
	}
}
