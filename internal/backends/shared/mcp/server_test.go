package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
)

// stubAsker stands in for the Threavia user.
type stubAsker struct {
	decision   mcp.Decision
	answer     string
	toolResult map[string]any
	err        error

	jobID    string
	toolName string
	input    map[string]any
	prompt   string
	choices  []string
	freeText bool
	file     string
	// validated records that the request went past the execution policy.
	validated bool
}

func (s *stubAsker) AskPermission(_ context.Context, jobID, toolName string, input map[string]any) (mcp.Decision, error) {
	s.jobID, s.toolName, s.input = jobID, toolName, input
	return s.decision, s.err
}

func (s *stubAsker) AskValidation(_ context.Context, jobID, toolName string, input map[string]any) (mcp.Decision, error) {
	s.jobID, s.toolName, s.input, s.validated = jobID, toolName, input, true
	return s.decision, s.err
}

func (s *stubAsker) AskUser(_ context.Context, jobID, prompt string, choices []string, freeText bool) (string, error) {
	s.jobID, s.prompt, s.choices, s.freeText = jobID, prompt, choices, freeText
	return s.answer, s.err
}

func (s *stubAsker) CallCoreTool(_ context.Context, jobID, name string, input map[string]any, fileInput string) (map[string]any, error) {
	s.jobID, s.toolName, s.input, s.file = jobID, name, input, fileInput
	return s.toolResult, s.err
}

func newServer(t *testing.T, asker mcp.Asker, coreTools ...mcp.CoreTool) (*mcp.Server, string) {
	t.Helper()

	server := mcp.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.SetAsker(asker)
	if err := server.Start(); err != nil {
		t.Fatalf("starting the tool endpoint: %v", err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })

	return server, server.Register("job-1", "token-1", coreTools)
}

// rpc performs one JSON-RPC call and returns the decoded result.
func rpc(t *testing.T, endpoint, method string, params any) (map[string]any, map[string]any) {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("calling %s: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decoding the response of %s: %v", method, err)
	}
	return decoded.Result, decoded.Error
}

// toolText extracts the single text content block a tool returns.
func toolText(t *testing.T, result map[string]any) string {
	t.Helper()

	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result %v carries no content", result)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content block %v is malformed", content[0])
	}
	text, _ := block["text"].(string)
	return text
}

// TestHandshakeAndTools pins what Claude Code needs to discover the endpoint.
func TestHandshakeAndTools(t *testing.T) {
	t.Parallel()

	_, endpoint := newServer(t, &stubAsker{})

	result, rpcErr := rpc(t, endpoint, "initialize", map[string]any{"protocolVersion": "2025-11-25"})
	if rpcErr != nil {
		t.Fatalf("initialize failed: %v", rpcErr)
	}
	// Echoing the client version is what keeps this compatible across protocol
	// revisions.
	if result["protocolVersion"] != "2025-11-25" {
		t.Errorf("protocolVersion = %v, want the one the client offered", result["protocolVersion"])
	}

	result, rpcErr = rpc(t, endpoint, "tools/list", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("tools/list failed: %v", rpcErr)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("%d tools exposed, want 2", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		name, _ := entry["name"].(string)
		names[name] = true
	}
	if !names[mcp.ToolApprovalPrompt] || !names[mcp.ToolAskUser] {
		t.Fatalf("tools = %v, want the approval prompt and the question tool", names)
	}
}

// TestApprovalIsAllowed pins the shape Claude Code expects when a permission is
// granted: the tool proceeds with its input.
func TestApprovalIsAllowed(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{decision: mcp.Decision{Approved: true}}
	_, endpoint := newServer(t, asker)

	result, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name": mcp.ToolApprovalPrompt,
		"arguments": map[string]any{
			"tool_name": "Write",
			"input":     map[string]any{"file_path": "/tmp/foo", "content": "bonjour"},
		},
	})
	if rpcErr != nil {
		t.Fatalf("the tool call failed: %v", rpcErr)
	}

	var payload struct {
		Behavior     string         `json:"behavior"`
		UpdatedInput map[string]any `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(toolText(t, result)), &payload); err != nil {
		t.Fatalf("decoding the decision: %v", err)
	}
	if payload.Behavior != "allow" {
		t.Fatalf("behavior = %q, want allow", payload.Behavior)
	}
	if payload.UpdatedInput["file_path"] != "/tmp/foo" {
		t.Fatalf("updatedInput = %v, want the original input", payload.UpdatedInput)
	}

	if asker.jobID != "job-1" || asker.toolName != "Write" {
		t.Fatalf("the request reached the user as %s/%s", asker.jobID, asker.toolName)
	}
}

// TestDenialCarriesTheReason pins that a refusal reaches the agent with an
// explanation.
func TestDenialCarriesTheReason(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{decision: mcp.Decision{Approved: false, Reason: "pas en production"}}
	_, endpoint := newServer(t, asker)

	result, _ := rpc(t, endpoint, "tools/call", map[string]any{
		"name":      mcp.ToolApprovalPrompt,
		"arguments": map[string]any{"tool_name": "Bash", "input": map[string]any{"command": "rm -rf /"}},
	})

	var payload struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(toolText(t, result)), &payload); err != nil {
		t.Fatalf("decoding the decision: %v", err)
	}
	if payload.Behavior != "deny" || payload.Message != "pas en production" {
		t.Fatalf("decision = %+v, want a denial carrying the reason", payload)
	}
}

// TestUnansweredPermissionFailsClosed pins the safety rule: a permission
// request that cannot be answered is never an approval.
func TestUnansweredPermissionFailsClosed(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{err: errors.New("core unreachable")}
	_, endpoint := newServer(t, asker)

	result, _ := rpc(t, endpoint, "tools/call", map[string]any{
		"name":      mcp.ToolApprovalPrompt,
		"arguments": map[string]any{"tool_name": "Write", "input": map[string]any{}},
	})

	var payload struct {
		Behavior string `json:"behavior"`
	}
	if err := json.Unmarshal([]byte(toolText(t, result)), &payload); err != nil {
		t.Fatalf("decoding the decision: %v", err)
	}
	if payload.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny when no decision could be obtained", payload.Behavior)
	}
}

// TestAskUserReachesTheUser pins the question path.
func TestAskUserReachesTheUser(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{answer: "staging"}
	_, endpoint := newServer(t, asker)

	result, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name": mcp.ToolAskUser,
		"arguments": map[string]any{
			"prompt":  "Quel environnement ?",
			"choices": []any{"staging", "production"},
		},
	})
	if rpcErr != nil {
		t.Fatalf("the tool call failed: %v", rpcErr)
	}
	if got := toolText(t, result); got != "staging" {
		t.Fatalf("answer = %q, want staging", got)
	}
	if asker.prompt != "Quel environnement ?" || len(asker.choices) != 2 {
		t.Fatalf("the question reached the user as %q %v", asker.prompt, asker.choices)
	}
	// The tool documents `choices` as a closed list, so offering one is the
	// agent saying it wants nothing else.
	if asker.freeText {
		t.Error("a question with a closed list of choices must not invite free text")
	}
}

// TestAskUserWithoutChoicesInvitesFreeText pins the other half of that contract:
// a question with no list has to be answerable at all.
func TestAskUserWithoutChoicesInvitesFreeText(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{answer: "/srv/app"}
	_, endpoint := newServer(t, asker)

	if _, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name":      mcp.ToolAskUser,
		"arguments": map[string]any{"prompt": "Where should I work?"},
	}); rpcErr != nil {
		t.Fatalf("the tool call failed: %v", rpcErr)
	}
	if !asker.freeText {
		t.Error("a question with no choices must invite free text")
	}
}

// TestEndpointIsScopedToItsJob pins the only thing standing between a local
// process and another Job's prompts: an unguessable per-Job token.
func TestEndpointIsScopedToItsJob(t *testing.T) {
	t.Parallel()

	server, endpoint := newServer(t, &stubAsker{decision: mcp.Decision{Approved: true}})

	body := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	resp, err := http.Post(endpoint+"-wrong", "application/json", body)
	if err != nil {
		t.Fatalf("calling an unknown endpoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown token returned %d, want 404", resp.StatusCode)
	}

	// Once the Job is gone, so is its endpoint: a late call from a lingering
	// process reaches nothing.
	server.Unregister("token-1")
	resp, err = http.Post(endpoint, "application/json",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	if err != nil {
		t.Fatalf("calling an unregistered endpoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unregistered endpoint returned %d, want 404", resp.StatusCode)
	}
}

// TestConfigPointsAtTheEndpoint pins the --mcp-config value handed to Claude
// Code.
func TestConfigPointsAtTheEndpoint(t *testing.T) {
	t.Parallel()

	encoded, err := mcp.Config("http://127.0.0.1:1234/mcp/token")
	if err != nil {
		t.Fatalf("rendering the config: %v", err)
	}

	var decoded struct {
		MCPServers map[string]struct {
			Type    string `json:"type"`
			URL     string `json:"url"`
			Timeout int64  `json:"timeout"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decoding the config: %v", err)
	}
	server, ok := decoded.MCPServers[mcp.ServerName]
	if !ok {
		t.Fatalf("config %s must declare the %s server", encoded, mcp.ServerName)
	}
	if server.Type != "http" || server.URL != "http://127.0.0.1:1234/mcp/token" {
		t.Fatalf("server = %+v, want the loopback endpoint over http", server)
	}
	// Regression: without it Claude Code gives up on a permission prompt after
	// a minute, and a user who answers later finds the tool already failed.
	// The ceiling is the largest delay a JavaScript timer holds.
	if server.Timeout < 24*time.Hour.Milliseconds() || server.Timeout > 1<<31-1 {
		t.Fatalf("timeout = %dms, want days, within a JavaScript timer", server.Timeout)
	}
}

// TestCoreToolsAreForwarded pins that a backend hardcodes no tool name: what the
// agent can call is exactly what Core declared for this Job, forwarded
// unchanged.
func TestCoreToolsAreForwarded(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{toolResult: map[string]any{"taskId": "task-1", "status": "TODO"}}
	_, endpoint := newServer(t, asker, mcp.CoreTool{
		Name:        "task_create",
		Description: "File a task.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"title": map[string]any{"type": "string"}},
			"required":   []any{"title"},
		},
	})

	result, rpcErr := rpc(t, endpoint, "tools/list", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("tools/list failed: %v", rpcErr)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("%d tools exposed, want the two built-ins plus the declared one", len(tools))
	}

	var declared map[string]any
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		if entry["name"] == "task_create" {
			declared = entry
		}
	}
	if declared == nil {
		t.Fatal("the declared core tool must be exposed to the agent")
	}
	if declared["description"] != "File a task." {
		t.Errorf("description = %v, want the one Core declared", declared["description"])
	}
	if declared["inputSchema"] == nil {
		t.Error("the schema Core declared must reach the agent")
	}

	// Calling it reaches Core with the arguments untouched.
	result, rpcErr = rpc(t, endpoint, "tools/call", map[string]any{
		"name":      "task_create",
		"arguments": map[string]any{"title": "Rewire the puppet manifest"},
	})
	if rpcErr != nil {
		t.Fatalf("the tool call failed: %v", rpcErr)
	}
	if asker.toolName != "task_create" || asker.input["title"] != "Rewire the puppet manifest" {
		t.Fatalf("Core received %q with %v", asker.toolName, asker.input)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(toolText(t, result)), &payload); err != nil {
		t.Fatalf("decoding the tool result: %v", err)
	}
	if payload["taskId"] != "task-1" {
		t.Fatalf("result = %v, want what Core returned", payload)
	}
}

// TestUndeclaredToolsAreRefused pins that the endpoint is not a general proxy
// into Core: only what this Job was told about can be called.
func TestUndeclaredToolsAreRefused(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{toolResult: map[string]any{}}
	_, endpoint := newServer(t, asker, mcp.CoreTool{Name: "task_create"})

	if _, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name":      "decision_create",
		"arguments": map[string]any{"title": "sneaky"},
	}); rpcErr == nil {
		t.Fatal("a tool this job was not told about must be refused")
	}
	if asker.toolName != "" {
		t.Fatalf("an undeclared tool reached Core as %q", asker.toolName)
	}
}

// TestThreaviaToolsNeedNoPermission is a regression test.
//
// Claude Code asks the permission prompt about every tool it is not already
// allowed, Threavia's own included. A Core Tool reads or writes the project
// memory through Core — no filesystem, no network, no git — so the prompt said
// nothing a user could act on, and the agent sat waiting for an answer until
// its call timed out.
func TestThreaviaToolsNeedNoPermission(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{}
	_, endpoint := newServer(t, asker,
		mcp.CoreTool{Name: "project_history_search", Description: "Search the project history."})

	for _, tool := range []string{
		"mcp__threavia__project_history_search",
		"project_history_search",
		"mcp__threavia__ask_user",
	} {
		result, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
			"name": mcp.ToolApprovalPrompt,
			"arguments": map[string]any{
				"tool_name": tool,
				"input":     map[string]any{"query": "helm"},
			},
		})
		if rpcErr != nil {
			t.Fatalf("the approval prompt failed for %s: %v", tool, rpcErr)
		}
		if got := toolText(t, result); !strings.Contains(got, `"behavior":"allow"`) {
			t.Errorf("%s was not allowed outright: %s", tool, got)
		}
	}

	// Nothing reached the user, which is the point: no card to click, and no
	// agent waiting on one.
	if asker.toolName != "" {
		t.Fatalf("a threavia tool reached the user as %q", asker.toolName)
	}
}

// TestProviderToolsStillNeedPermission pins the other side: the gate is still
// there for what actually touches the machine.
func TestProviderToolsStillNeedPermission(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{decision: mcp.Decision{Approved: false, Reason: "no"}}
	_, endpoint := newServer(t, asker,
		mcp.CoreTool{Name: "project_history_search", Description: "Search the project history."})

	result, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name": mcp.ToolApprovalPrompt,
		"arguments": map[string]any{
			"tool_name": "Bash",
			"input":     map[string]any{"command": "rm -rf /"},
		},
	})
	if rpcErr != nil {
		t.Fatalf("the approval prompt failed: %v", rpcErr)
	}
	if asker.toolName != "Bash" {
		t.Fatalf("the provider tool did not reach the user, got %q", asker.toolName)
	}
	if got := toolText(t, result); !strings.Contains(got, `"behavior":"deny"`) {
		t.Fatalf("the refusal did not reach the provider: %s", got)
	}
}

// TestAToolThatWidensTheScopeIsValidated pins the exception to the rule above:
// a Core Tool whose spec requires validation is asked of the user, past the
// policy, and the answer is what reaches the provider.
func TestAToolThatWidensTheScopeIsValidated(t *testing.T) {
	t.Parallel()

	for _, approved := range []bool{false, true} {
		asker := &stubAsker{decision: mcp.Decision{Approved: approved}}
		_, endpoint := newServer(t, asker,
			mcp.CoreTool{Name: "task_create"},
			mcp.CoreTool{Name: "working_directory_set", RequiresValidation: true})

		result, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
			"name": mcp.ToolApprovalPrompt,
			"arguments": map[string]any{
				"tool_name": "mcp__threavia__working_directory_set",
				"input":     map[string]any{"knownDirectoryId": "kd-home"},
			},
		})
		if rpcErr != nil {
			t.Fatalf("the approval prompt failed: %v", rpcErr)
		}
		if !asker.validated || asker.toolName != "working_directory_set" || asker.input["knownDirectoryId"] != "kd-home" {
			t.Fatalf("the request reached the user as validated=%t %q %v, want a validation of working_directory_set",
				asker.validated, asker.toolName, asker.input)
		}

		want := `"behavior":"deny"`
		if approved {
			want = `"behavior":"allow"`
		}
		if got := toolText(t, result); !strings.Contains(got, want) {
			t.Fatalf("approved=%t answered %s, want %s", approved, got, want)
		}
	}
}

// TestTaskCreateNeedsNoValidation keeps the exception narrow: a Core Tool that
// only writes project metadata is still allowed without asking anyone, next
// to one that is asked.
func TestTaskCreateNeedsNoValidation(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{decision: mcp.Decision{Approved: false}}
	_, endpoint := newServer(t, asker,
		mcp.CoreTool{Name: "task_create"},
		mcp.CoreTool{Name: "working_directory_set", RequiresValidation: true})

	result, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name": mcp.ToolApprovalPrompt,
		"arguments": map[string]any{
			"tool_name": "mcp__threavia__task_create",
			"input":     map[string]any{"title": "Écrire la doc"},
		},
	})
	if rpcErr != nil {
		t.Fatalf("the approval prompt failed: %v", rpcErr)
	}
	if got := toolText(t, result); !strings.Contains(got, `"behavior":"allow"`) {
		t.Fatalf("task_create was not allowed outright: %s", got)
	}
	if asker.toolName != "" {
		t.Fatalf("task_create reached the user as %q", asker.toolName)
	}
}

// TestAFileToolNamesItsFile keeps the backend from guessing which input is a
// path to read: Core says so in the tool's spec, and the endpoint passes it on.
func TestAFileToolNamesItsFile(t *testing.T) {
	t.Parallel()

	asker := &stubAsker{toolResult: map[string]any{"artifactId": "a-1"}}
	_, endpoint := newServer(t, asker, mcp.CoreTool{Name: "artifact_publish", FileInput: "path"})

	if _, rpcErr := rpc(t, endpoint, "tools/call", map[string]any{
		"name":      "artifact_publish",
		"arguments": map[string]any{"path": "report.html"},
	}); rpcErr != nil {
		t.Fatalf("tools/call failed: %v", rpcErr)
	}
	if asker.toolName != "artifact_publish" || asker.file != "path" {
		t.Fatalf("forwarded %q with file input %q, want artifact_publish with path", asker.toolName, asker.file)
	}
}
