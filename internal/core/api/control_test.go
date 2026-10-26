package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/externalmcp"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"google.golang.org/protobuf/types/known/structpb"
)

func createControlAccess(t *testing.T, c *core, scopes ...string) (domain.ControlAccess, string) {
	t.Helper()
	var created struct {
		Access domain.ControlAccess `json:"access"`
		Token  string               `json:"token"`
	}
	c.mustDo(http.MethodPost, "/api/v1/me/control-accesses", map[string]any{"label": "Integration controller", "scopes": scopes}, &created, http.StatusCreated)
	return created.Access, created.Token
}

func TestControlWaitPagesSurviveReconnectWithoutGaps(t *testing.T) {
	c, backend, project, dir := setup(t)
	ctx := context.Background()
	session := c.startSession(project, c.backendID, dir, "Stream results")
	start := receive(t, "stream job", backend.starts)
	_, token := createControlAccess(t, c, "read")
	conn := connectControl(t, c, token)
	for n := 0; n < 135; n++ {
		backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
			return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), fmt.Sprint("proof-", n))
		}))
	}
	var full struct {
		Events []struct {
			Sequence int64  `json:"sequence"`
			Type     string `json:"type"`
		} `json:"events"`
	}
	waitUntil(t, "all proof messages", func() bool {
		c.mustDo("GET", "/api/v1/sessions/"+session+"?history=500", nil, &full, 200)
		n := 0
		for _, e := range full.Events {
			if e.Type == "agent.message" {
				n++
			}
		}
		return n == 135
	})
	first := controlCall(t, conn, "session_wait", map[string]any{"sessionId": session, "timeoutSeconds": 0})
	if len(first["events"].([]any)) != 100 || first["hasMore"] != true {
		t.Fatal("first forward page was not bounded")
	}
	cursor := first["cursor"].(float64)
	conn.Close()
	conn = connectControl(t, c, token)
	second := controlCall(t, conn, "session_wait", map[string]any{"sessionId": session, "afterSequence": cursor, "timeoutSeconds": 0})
	seen := map[float64]bool{}
	for _, page := range []map[string]any{first, second} {
		for _, raw := range page["events"].([]any) {
			seq := raw.(map[string]any)["sequence"].(float64)
			if seen[seq] {
				t.Fatal("duplicate event after reconnect")
			}
			seen[seq] = true
		}
	}
	if len(seen) != len(full.Events) {
		t.Fatalf("replay lost events: got %d want %d", len(seen), len(full.Events))
	}
	cursor = second["cursor"].(float64)
	read := controlCall(t, conn, "session_read", map[string]any{"sessionId": session, "limit": 1})
	if read["cursor"] != cursor {
		t.Fatal("session_read returned a global cursor")
	}
	idle := controlCall(t, conn, "session_wait", map[string]any{"sessionId": session, "afterSequence": cursor, "timeoutSeconds": 0})
	if idle["timedOut"] != true || idle["finished"] != false || idle["cursor"] != cursor {
		t.Fatal("idle wait was mistaken for completion or skipped the cursor")
	}
	callCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := conn.Call(callCtx, "session_wait", map[string]any{"sessionId": session, "afterSequence": cursor, "timeoutSeconds": 30}); err == nil {
		t.Fatal("cancelled wait succeeded")
	}
}

func TestControlTwoCoresThroughRealBackendMCPProxy(t *testing.T) {
	ctx := context.Background()
	target := newCore(t)
	_, token := createControlAccess(t, target, "read", "manage")
	t.Setenv("CONTROL_TEST_AUTHORIZATION", "Bearer "+token)
	main := newCore(t)
	project := main.createProject("Controller")
	backendID, credential := main.registerBackend("controller-backend")
	local := newIdleRunner()
	t.Cleanup(func() { close(local.release) })
	main.connectBackendWith(local, credential, t.TempDir())
	waitUntil(t, "controller backend MCP feature", func() bool {
		b, e := main.store.GetBackendInstance(ctx, "thomas", domain.BackendInstanceID(backendID))
		return e == nil && b.HasFeature(domain.FeatureExternalMCP)
	})
	declaration := mcpconfig.Server{Name: "dev", Transport: "http", URL: target.http.URL + "/mcp", Headers: map[string]string{"Authorization": "CONTROL_TEST_AUTHORIZATION"}, Tools: []string{"instance_info", "project_create", "project_delete"}, ReadTools: []string{"instance_info"}, WriteTools: []string{"project_create", "project_delete"}, Enabled: true}
	main.mustDo("POST", "/api/v1/me/mcp", declaration, &declaration, 201)
	main.mustDo("PUT", "/api/v1/projects/"+project+"/mcp", mcpconfig.Binding{ServerID: declaration.ID, Enabled: true, Required: true}, nil, 204)
	main.mustDo("PUT", "/api/v1/projects/"+project+"/policy", map[string]any{"mode": "GUARDED", "allowNetwork": true}, nil, 200)
	session := main.startSession(project, backendID, "", "Drive target")
	params := receive(t, "controller with remote tools", local.started)
	if len(params.ExternalTools) != 3 {
		t.Fatalf("controller received %d tools", len(params.ExternalTools))
	}
	invoke := func(name string, args map[string]any) map[string]any {
		t.Helper()
		prefix := "external_dev_" + name
		if len(name) > 12 {
			prefix = "external_dev_" + name[:12]
		}
		prefix += "_"
		for _, tool := range params.ExternalTools {
			if strings.HasPrefix(tool.Name, prefix) {
				v, err := tool.Call(ctx, args)
				if err != nil {
					t.Fatal(err)
				}
				res := v.(*sdk.CallToolResult)
				if res.IsError {
					t.Fatalf("remote %s failed", name)
				}
				raw, _ := json.Marshal(res.StructuredContent)
				var out map[string]any
				if err = json.Unmarshal(raw, &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
		}
		t.Fatalf("missing remote tool %s", name)
		return nil
	}
	invoke("instance_info", map[string]any{})
	remoteProject := invoke("project_create", map[string]any{"name": "Created from another Core"})["id"].(string)
	if len(main.snapshot(session).Attention.Validations) != 0 {
		t.Fatal("preauthorized write asked for a redundant human click")
	}
	var projects struct {
		Items []domain.Project `json:"items"`
	}
	target.mustDo("GET", "/api/v1/projects", nil, &projects, 200)
	if len(projects.Items) != 1 || string(projects.Items[0].ID) != remoteProject {
		t.Fatal("controller did not mutate the target Core")
	}
	invoke("project_delete", map[string]any{"projectId": remoteProject})
	target.mustDo("GET", "/api/v1/projects", nil, &projects, 200)
	if len(projects.Items) != 0 {
		t.Fatal("remote cleanup failed")
	}
}
func connectControl(t *testing.T, c *core, token string) *externalmcp.Connection {
	t.Helper()
	t.Setenv("CONTROL_TEST_AUTHORIZATION", "Bearer "+token)
	conn, err := externalmcp.Connect(context.Background(), mcpconfig.Server{Name: "dev", Transport: "http", URL: c.http.URL + "/mcp", Headers: map[string]string{"Authorization": "CONTROL_TEST_AUTHORIZATION"}}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return conn
}
func controlCall(t *testing.T, conn *externalmcp.Connection, name string, in map[string]any) map[string]any {
	t.Helper()
	r, err := conn.Session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: in})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if r.IsError {
		t.Fatalf("%s failed: %v", name, r.Content)
	}
	data, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func controlRefused(t *testing.T, conn *externalmcp.Connection, name string, in map[string]any) {
	t.Helper()
	r, err := conn.Session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: in})
	if err == nil && !r.IsError {
		t.Fatalf("%s unexpectedly succeeded", name)
	}
}
func TestControlTokensScopesIsolationRevocationAndBootstrap(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	project := c.createProject("Owned fixture")
	other := c.asUser("another-owner")
	foreign := other.createProject("Private fixture")
	a, token := createControlAccess(t, c, "read")
	conn := connectControl(t, c, token)
	listed := controlCall(t, conn, "project_list", map[string]any{})
	if len(listed["items"].([]any)) != 1 {
		t.Fatal("other owner leaked into project list")
	}
	controlCall(t, conn, "project_read", map[string]any{"projectId": project})
	controlRefused(t, conn, "project_read", map[string]any{"projectId": foreign})
	controlRefused(t, conn, "project_create", map[string]any{"name": "forbidden"})
	controlRefused(t, conn, "session_resolve_validation", map[string]any{})
	controlRefused(t, conn, "project_read", map[string]any{"projectId": project, "unexpected": true})
	for _, method := range []string{"POST", "GET", "DELETE"} {
		resp := send(t, c.http, method, "/mcp", "", map[string]string{"Content-Type": "application/json"}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without token: %d", method, resp.StatusCode)
		}
	}
	resp := send(t, c.http, "POST", "/mcp", "", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token, "Origin": "https://other.example", "Sec-Fetch-Site": "cross-site"}, `{}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("cross-site MCP request accepted")
	}
	var list struct {
		Items []domain.ControlAccess `json:"items"`
	}
	c.mustDo("GET", "/api/v1/me/control-accesses", nil, &list, 200)
	if strings.Contains(c.lastBody, token) || strings.Contains(c.lastBody, "secret_hash") {
		t.Fatal("listed a credential")
	}
	other.mustDo("DELETE", "/api/v1/control-accesses/"+a.ID, nil, nil, 404)
	c.mustDo("DELETE", "/api/v1/control-accesses/"+a.ID, nil, nil, 204)
	c.mustDo("DELETE", "/api/v1/control-accesses/"+a.ID, nil, nil, 204)
	if _, err := c.svc.AuthenticateControl(ctx, token); err == nil {
		t.Fatal("revoked token authenticated")
	}
	controlRefused(t, conn, "project_list", map[string]any{})
	for _, scope := range [][]string{{}, {"read", "read"}, {"admin"}} {
		c.mustDo("POST", "/api/v1/me/control-accesses", map[string]any{"label": "bad", "scopes": scope}, nil, 400)
	}
	c.mustDo("POST", "/api/v1/me/control-accesses", map[string]any{"label": "expired", "scopes": []string{"read"}, "expiresAt": time.Now().Add(-time.Hour)}, nil, 400)
	_, expiring := createControlAccess(t, c, "manage")
	if _, err := c.db.Pool().Exec(ctx, `UPDATE control_accesses SET expires_at=clock_timestamp()-interval '1 second' WHERE secret_hash=(SELECT secret_hash FROM control_accesses WHERE scopes=ARRAY['manage']::text[])`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.AuthenticateControl(ctx, expiring); err == nil {
		t.Fatal("expired token authenticated")
	}
	bootstrap := strings.Repeat("b", 48)
	if err := c.svc.BootstrapControlAccess(ctx, "thomas", bootstrap, []string{"read"}); err != nil {
		t.Fatal(err)
	}
	initial, err := c.svc.AuthenticateControl(ctx, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.svc.BootstrapControlAccess(ctx, "thomas", bootstrap, []string{"read", "approve"}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := c.svc.AuthenticateControl(ctx, bootstrap)
	if err != nil || len(unchanged.Scopes) != 1 {
		t.Fatal("restart widened bootstrap grant")
	}
	if err := c.svc.RevokeControlAccess(ctx, auth.Identity{UserID: "thomas"}, initial.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.svc.BootstrapControlAccess(ctx, "thomas", bootstrap, []string{"read", "manage"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.AuthenticateControl(ctx, bootstrap); err == nil {
		t.Fatal("restart resurrected revoked bootstrap")
	}
}

func TestControlSessionQuestionsPermissionsCancellationAndCleanup(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	backendID, credential := c.registerBackend("target-backend")
	backend := c.connectBackend(credential)
	a, token := createControlAccess(t, c, "read", "manage", "answer", "approve")
	conn := connectControl(t, c, token)
	project := controlCall(t, conn, "project_create", map[string]any{"name": "Remote integration"})["id"].(string)
	directory := controlCall(t, conn, "project_directory_create", map[string]any{"projectId": project, "name": "workspace"})["id"].(string)
	controlCall(t, conn, "known_directory_bind", map[string]any{"directoryId": directory, "backendInstanceId": backendID, "path": "/fixture"})
	input := map[string]any{"projectId": project, "backendInstanceId": backendID, "workingDirectoryId": directory, "message": "Run integration", "idempotencyKey": "first"}
	started := controlCall(t, conn, "session_create", input)
	session := started["session"].(map[string]any)["id"].(string)
	start := receive(t, "remote job", backend.starts)
	if !strings.Contains(start.GetPrompt(), "remote MCP controller (mcp:"+a.ID+")") || !strings.Contains(start.GetPrompt(), "does not itself grant new user permissions") {
		t.Fatal("remote message lost its provenance in the provider prompt")
	}
	if controlCall(t, conn, "session_create", input)["session"].(map[string]any)["id"] != session {
		t.Fatal("session retry duplicated work")
	}
	controlRefused(t, conn, "known_directory_delete", map[string]any{"directoryId": directory})
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(), &backendv1.UserInputRequested{RequestId: "environment", Prompt: "Choose environment", Choices: []string{"dev", "prod"}})
	}))
	waitUntil(t, "remote question", func() bool { return len(c.snapshot(session).Attention.UserInputs) == 1 })
	question := c.snapshot(session).Attention.UserInputs[0].ID
	controlRefused(t, conn, "session_answer", map[string]any{"sessionId": session, "requestId": question, "value": "unknown"})
	controlCall(t, conn, "session_answer", map[string]any{"sessionId": session, "requestId": question, "value": "dev"})
	if answer := receive(t, "remote answer", backend.inputs); answer.GetValue() != "dev" || answer.GetResolvedByUserId() != "mcp:"+a.ID {
		t.Fatal("answer lost delegated attribution")
	}
	for n, approved := range []bool{false, true} {
		payload, _ := structpb.NewStruct(map[string]any{"tool": "mock_create_note", "input": map[string]any{"text": "fixture"}})
		backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
			return backend.events.ValidationRequested(ctx, start.GetRunId(), start.GetJobId(), &backendv1.ValidationRequested{RequestId: []string{"reject", "accept"}[n], Title: "Create fictional note", RequestPayload: payload})
		}))
		waitUntil(t, "remote permission", func() bool { return len(c.snapshot(session).Attention.Validations) == 1 })
		read := controlCall(t, conn, "session_read", map[string]any{"sessionId": session})
		validation := read["attention"].(map[string]any)["validations"].([]any)[0].(map[string]any)
		args := map[string]any{"sessionId": session, "requestId": validation["id"], "payloadSha256": strings.Repeat("0", 64), "approved": approved}
		controlRefused(t, conn, "session_resolve_validation", args)
		args["payloadSha256"] = validation["payloadSha256"]
		controlCall(t, conn, "session_resolve_validation", args)
		decision := receive(t, "remote permission resolution", backend.validations)
		if decision.GetApproved() != approved || decision.GetResolvedByUserId() != "mcp:"+a.ID {
			t.Fatal("wrong permission decision or actor")
		}
		controlRefused(t, conn, "session_resolve_validation", args)
	}
	queued := controlCall(t, conn, "session_send", map[string]any{"sessionId": session, "message": "follow-up", "idempotencyKey": "next"})
	controlCall(t, conn, "session_send", map[string]any{"sessionId": session, "message": "follow-up", "idempotencyKey": "next"})
	if len(c.snapshot(session).Jobs) != 2 {
		t.Fatal("message retry duplicated job")
	}
	controlCall(t, conn, "job_delete_queued", map[string]any{"jobId": queued["id"]})
	controlCall(t, conn, "job_cancel", map[string]any{"jobId": start.GetJobId(), "reason": "Integration Stop"})
	if receive(t, "remote cancellation", backend.cancels).GetJobId() != start.GetJobId() {
		t.Fatal("wrong cancelled job")
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCancelled(ctx, start.GetRunId(), start.GetJobId())
	}))
	waitUntil(t, "cancelled job", func() bool { return c.jobStatus(session, start.GetJobId()) == "CANCELLED" })
	controlCall(t, conn, "session_archive", map[string]any{"sessionId": session})
	controlCall(t, conn, "session_restore", map[string]any{"sessionId": session})
	controlCall(t, conn, "session_delete", map[string]any{"sessionId": session})
	controlCall(t, conn, "known_directory_delete", map[string]any{"directoryId": directory})
	controlCall(t, conn, "project_delete", map[string]any{"projectId": project})
	entries, err := c.svc.Audit(ctx, auth.Identity{UserID: "thomas"}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	proven := false
	for _, entry := range entries {
		if entry.Action == "validation.resolved" {
			proven = true
			if entry.ActorID != "mcp:"+a.ID || entry.Channel != "mcp" {
				t.Fatal("permission was attributed to a human")
			}
		}
	}
	if !proven {
		t.Fatal("missing durable permission audit")
	}
}
