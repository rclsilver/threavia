package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
)

type testEndpoint struct{}

func (testEndpoint) RegisterLocalTools(string, []mcp.LocalTool) {}

func (testEndpoint) RegisterPolicyGate(_ string, gate func(context.Context, map[string]any) (any, error)) {
	_, _ = gate(context.Background(), map[string]any{"hook_event_name": "SessionStart"})
}

func (testEndpoint) Register(string, string, []mcp.CoreTool) string {
	return "http://127.0.0.1:12345/mcp/secret-job-token"
}
func (testEndpoint) Unregister(string) {}

type recordingSink struct {
	mu                    sync.Mutex
	events                []string
	native, code, summary string
	usage                 *backendv1.Usage
	started               chan struct{}
	payloads              map[string]map[string]any
}

func (s *recordingSink) keep(event string, payload map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.payloads == nil {
		s.payloads = make(map[string]map[string]any)
	}
	s.payloads[event] = payload
}

func (s *recordingSink) append(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, value)
}
func (s *recordingSink) JobStarted(context.Context, string, string) error {
	s.append("started")
	if s.started != nil {
		close(s.started)
	}
	return nil
}
func (s *recordingSink) NativeSessionBound(_ context.Context, _, _, id string) error {
	s.native = id
	s.append("bound")
	return nil
}
func (s *recordingSink) AgentMessage(_ context.Context, _, _, text string) error {
	s.append("message:" + text)
	return nil
}
func (s *recordingSink) ToolStarted(_ context.Context, _, _, id, name string, input map[string]any) error {
	s.keep("tool:"+name+":"+id, input)
	s.append("tool:" + name + ":" + id)
	return nil
}
func (s *recordingSink) ToolCompleted(_ context.Context, _, _, id, name string, output map[string]any) error {
	s.keep("result:"+name+":"+id, output)
	s.append("result:" + name + ":" + id)
	return nil
}
func (s *recordingSink) ToolFailed(_ context.Context, _, _, id, name, detail string) error {
	s.append("tool-failed:" + name + ":" + id)
	return nil
}
func (s *recordingSink) WorkspaceChanged(context.Context, string, string, workspace.Summary) error {
	s.append("workspace")
	return nil
}
func (s *recordingSink) JobCompleted(_ context.Context, _, _, summary string, usage *backendv1.Usage) error {
	s.summary = summary
	s.usage = usage
	s.append("completed")
	return nil
}
func (s *recordingSink) JobFailed(_ context.Context, _, _, code, detail string, usage *backendv1.Usage) error {
	s.code = code
	s.summary = detail
	s.usage = usage
	s.append("failed")
	return nil
}
func (s *recordingSink) JobCancelled(context.Context, string, string) error {
	s.append("cancelled")
	return nil
}
func (s *recordingSink) Progress(_ context.Context, _, _, kind, detail string) {
	s.append("progress:" + detail)
}

type testAsker struct {
	approved bool
	called   chan struct{}
	wait     bool
	release  <-chan struct{}
	calls    []string
	mu       sync.Mutex
}

func (a *testAsker) AskPermission(ctx context.Context, _, name string, input map[string]any) (mcp.Decision, error) {
	a.mu.Lock()
	a.calls = append(a.calls, name)
	if a.called != nil {
		select {
		case a.called <- struct{}{}:
		default:
		}
	}
	a.mu.Unlock()
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return mcp.Decision{}, ctx.Err()
		}
	}
	if a.wait {
		<-ctx.Done()
		return mcp.Decision{}, ctx.Err()
	}
	return mcp.Decision{Approved: a.approved}, nil
}
func (a *testAsker) AskValidation(ctx context.Context, job, name string, input map[string]any) (mcp.Decision, error) {
	return a.AskPermission(ctx, job, name, input)
}
func (a *testAsker) AskUser(context.Context, string, string, []string, bool) (string, error) {
	return "answer", nil
}
func (a *testAsker) CallCoreTool(context.Context, string, string, map[string]any, string) (map[string]any, error) {
	return nil, errors.New("unexpected call")
}

func provider(t *testing.T, scenario string) (*Codex, contract.StartParams, *recordingSink) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary, nil, Options{Model: "test-model", ReasoningEffort: "medium", Scratch: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.tools = testEndpoint{}
	c.command = func(ctx context.Context, hookCommand string) *exec.Cmd {
		if strings.Contains(hookCommand, "secret-job-token") {
			t.Fatal("policy credential leaked into Codex argv")
		}
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestProviderProcess$", "--", scenario)
		cmd.Env = append(os.Environ(), "THREAVIA_CODEX_TEST_PROCESS=1", "THREAVIA_CODEX_TEST_HOOK_COMMAND="+hookCommand)
		return cmd
	}
	c.SetAsker(&testAsker{approved: true})
	p := contract.StartParams{RunID: "run", JobID: "job", WorkingDirectory: t.TempDir(), Prompt: "hello", Policy: policy.Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_GUARDED, AllowFilesystemWrite: true, AllowGitCommit: true, AllowNetwork: true}}
	return c, p, &recordingSink{}
}

// A real subprocess exercises framing, process exit, cancellation and RPC
// dispatch without a network listener, credentials or billable inference.
func TestProviderProcess(t *testing.T) {
	if os.Getenv("THREAVIA_CODEX_TEST_PROCESS") != "1" {
		return
	}
	if os.Getenv("THREAVIA_CODEX_POLICY_ENDPOINT") != "http://127.0.0.1:12345/policy/secret-job-token" {
		os.Exit(16)
	}
	scenario := os.Args[len(os.Args)-1]
	encoder := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if err := encoder.Encode(v); err != nil {
			os.Exit(8)
		}
	}
	notify := func(method string, p any) { send(map[string]any{"method": method, "params": p}) }
	complete := func(turn string) {
		notify("item/agentMessage/delta", map[string]any{"threadId": "thread-1", "delta": "done"})
		notify("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"type": "agentMessage", "id": "message-1", "text": "done"}})
		notify("thread/tokenUsage/updated", map[string]any{"threadId": "thread-1", "turnId": turn, "tokenUsage": map[string]any{
			"total": map[string]any{"inputTokens": 10000, "outputTokens": 9000, "cachedInputTokens": 5000},
			"last":  map[string]any{"inputTokens": 100, "outputTokens": 20, "cachedInputTokens": 60}}})
		status := "completed"
		var failure any
		if scenario == "failed" {
			status = "failed"
			failure = map[string]any{"message": "model refused the request"}
		}
		notify("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": turn, "status": status, "error": failure}})
	}
	turn := "turn-1"
	loggedIn := false
	var roots []string
	helperCount := 0
	helperKinds := map[string]bool{}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 8<<20)
	for scanner.Scan() {
		var msg message
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(9)
		}
		var p map[string]any
		_ = json.Unmarshal(msg.Params, &p)
		response := func(result any) { send(map[string]any{"id": msg.ID, "result": result}) }
		switch msg.Method {
		case "initialize":
			response(map[string]any{"userAgent": "fake-codex"})
		case "initialized":
		case "account/read":
			var account any = map[string]any{"type": "apiKey"}
			if scenario == "no-auth" {
				account = nil
			}
			response(map[string]any{"account": account, "requiresOpenaiAuth": true})
		case "account/login/start":
			if p["type"] != "apiKey" || p["apiKey"] != "test-private-key" {
				os.Exit(13)
			}
			loggedIn = true
			response(map[string]any{"type": "apiKey"})
		case "config/read":
			response(map[string]any{"config": map[string]any{"mcp_servers": map[string]any{"private-server": map[string]any{"url": "http://example.invalid"}}}})
		case "skills/extraRoots/set":
			if entries, ok := p["extraRoots"].([]any); ok {
				for _, entry := range entries {
					roots = append(roots, entry.(string))
				}
			}
			response(map[string]any{})
		case "skills/list":
			nativeSkills := []any{map[string]any{"name": "unrelated-machine-skill", "path": "/unrelated/SKILL.md", "enabled": true}}
			for _, root := range roots {
				nativeSkills = append(nativeSkills, map[string]any{"name": "review-code", "path": filepath.Join(root, "SKILL.md"), "enabled": true})
			}
			response(map[string]any{"data": []any{map[string]any{"skills": nativeSkills}}})
		case "thread/read":
			parent := map[string]string{"child-1": "thread-1", "foreign-1": "elsewhere"}[str(p, "threadId")]
			response(map[string]any{"thread": map[string]any{"turns": []any{}, "parentThreadId": parent}})
		case "hooks/list":
			var hooks []any
			for _, event := range []string{"preToolUse", "sessionStart"} {
				hooks = append(hooks, map[string]any{"command": os.Getenv("THREAVIA_CODEX_TEST_HOOK_COMMAND"), "eventName": event, "enabled": true, "handlerType": "command", "key": event, "currentHash": "test-hash"})
			}
			if scenario == "missing-hooks" {
				hooks = nil
			}
			if scenario == "local-hooks" {
				hooks = append(hooks, map[string]any{"command": "unrelated-hook", "eventName": "preToolUse", "enabled": true, "handlerType": "command", "key": "local-hook", "currentHash": "local-hash"})
			}
			response(map[string]any{"data": []any{map[string]any{"hooks": hooks}}})
		case "thread/start", "thread/resume":
			if p["ephemeral"] == true {
				config, _ := p["config"].(map[string]any)
				if p["approvalPolicy"] != "never" || config["mcp_servers.threavia.enabled"] != false || config["features.shell_tool"] != false || config["features.multi_agent"] != false || config["features.unified_exec"] != false {
					os.Exit(19)
				}
				settings, _ := config["skills.config"].([]any)
				for _, setting := range settings {
					entry, _ := setting.(map[string]any)
					if entry["enabled"] != false {
						os.Exit(24)
					}
				}
				helperCount++
				id := fmt.Sprintf("helper-%d", helperCount)
				helperKinds[id] = config["web_search"] == "live"
				response(map[string]any{"thread": map[string]any{"id": id}})
				continue
			}
			if scenario == "apikey" && !loggedIn {
				os.Exit(14)
			}
			// Assert isolation and the actual native wire spellings.
			config, _ := p["config"].(map[string]any)
			settings, _ := config["skills.config"].([]any)
			disabled := false
			for _, setting := range settings {
				entry, _ := setting.(map[string]any)
				if entry["path"] == "/unrelated/SKILL.md" {
					disabled = entry["enabled"] == false
				}
			}
			if !disabled {
				os.Exit(23)
			}
			approval, reviewer := "untrusted", "user"
			if strings.HasPrefix(scenario, "supervised") {
				approval, reviewer = "on-request", "auto_review"
			}
			if scenario == "supervised-readonly" {
				approval = "never"
			}
			if p["approvalPolicy"] != approval || p["approvalsReviewer"] != reviewer || p["sandbox"] != "read-only" || config["mcp_servers.private-server.enabled"] != false {
				os.Exit(10)
			}
			trust, _ := config["hooks.state"].(map[string]any)
			if (scenario != "local-hooks" && len(trust) != 2) || config["features.hooks"] != true || config["features.unified_exec"] != true || config["features.unified_exec_tty"] != false || config["features.multi_agent"] != true {
				os.Exit(15)
			}
			if scenario == "local-hooks" {
				local, _ := trust["local-hook"].(map[string]any)
				if local["enabled"] != false {
					os.Exit(17)
				}
			}
			if scenario == "resume" && p["threadId"] != "thread-1" {
				os.Exit(11)
			}
			if scenario == "resume-error" {
				send(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32600, "message": "thread not found"}})
				continue
			}
			response(map[string]any{"thread": map[string]any{"id": "thread-1"}})
		case "turn/start":
			if id := str(p, "threadId"); strings.HasPrefix(id, "helper-") {
				response(map[string]any{"turn": map[string]any{"id": "helper-turn"}})
				text := `{"verdict":"allow","reason":"Within the user's request."}`
				if strings.Contains(scenario, "deny") {
					text = `{"verdict":"deny","reason":"Outside the user's request."}`
				}
				if strings.Contains(scenario, "ask") {
					text = `{"verdict":"ask","reason":"Uncertain authorization."}`
				}
				if helperKinds[id] {
					if scenario != "helper-no-search" {
						notify("item/completed", map[string]any{"threadId": id, "item": map[string]any{"id": "search", "type": "webSearch", "query": "test"}})
					}
					text = `{"results":[{"title":"Example","url":"https://example.com/docs","snippet":"An excerpt."},{"title":"Denied","url":"https://denied.example/private","snippet":"Hidden."}]}`
				}
				if scenario == "helper-malformed" {
					text = "not valid JSON"
				}
				notify("item/completed", map[string]any{"threadId": id, "item": map[string]any{"id": "helper-message", "type": "agentMessage", "text": text}})
				notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "helper-turn", "status": "completed"}})
				continue
			}
			if strings.HasPrefix(scenario, "supervised") {
				input, _ := p["input"].([]any)
				message, _ := input[0].(map[string]any)
				if !strings.Contains(str(message, "text"), "Standing instructions") {
					os.Exit(20)
				}
			}
			if scenario == "skills" {
				input, _ := p["input"].([]any)
				if len(input) != 2 {
					os.Exit(21)
				}
				skill, _ := input[1].(map[string]any)
				if skill["type"] != "skill" || skill["name"] != "review-code" || !filepath.IsAbs(str(skill, "path")) {
					os.Exit(22)
				}
			}
			response(map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress"}})
			if scenario == "crash" {
				os.Exit(12)
			}
			if scenario == "malformed" {
				_, _ = os.Stdout.WriteString("invalid JSON\n")
				continue
			}
			if scenario == "hang" || strings.HasPrefix(scenario, "helper-") || strings.HasPrefix(scenario, "supervised") {
				continue
			}
			if scenario == "approval" || scenario == "interrupt" || scenario == "queue" {
				item := map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "touch example.txt", "cwd": p["cwd"], "status": "inProgress"}
				notify("item/started", map[string]any{"threadId": "thread-1", "item": item})
				send(map[string]any{"id": 7, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-1", "turnId": turn, "itemId": "cmd-1", "command": "touch example.txt"}})
				continue
			}
			if scenario == "subagents" {
				// Usage restored from an earlier Job's turn is not this Job's.
				notify("thread/tokenUsage/updated", map[string]any{"threadId": "thread-1", "turnId": "old-turn", "tokenUsage": map[string]any{"last": map[string]any{"inputTokens": 9999}}})
				notify("thread/tokenUsage/updated", map[string]any{"threadId": "thread-1", "turnId": turn, "tokenUsage": map[string]any{"last": map[string]any{"inputTokens": 10, "outputTokens": 1}}})
				notify("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"type": "subAgentActivity", "id": "spawn-1", "kind": "started", "agentThreadId": "child-1", "agentPath": "/root/alpha"}})
				notify("turn/started", map[string]any{"threadId": "child-1", "turn": map[string]any{"id": "child-turn"}})
				notify("item/started", map[string]any{"threadId": "child-1", "item": map[string]any{"id": "child-cmd", "type": "commandExecution", "command": "touch alpha.txt"}})
				send(map[string]any{"id": 8, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "child-1", "turnId": "child-turn", "itemId": "child-cmd", "command": "touch alpha.txt"}})
				send(map[string]any{"id": 9, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "foreign-1", "turnId": "x", "itemId": "x", "command": "touch foreign.txt"}})
				continue
			}
			if scenario == "mcp-failure" {
				item := map[string]any{"id": "web-1", "type": "mcpToolCall", "server": "threavia", "tool": "web_fetch", "status": "inProgress"}
				notify("item/started", map[string]any{"threadId": "thread-1", "item": item})
				item["status"] = "failed"
				item["error"] = map[string]any{"message": "policy refused this page"}
				notify("item/completed", map[string]any{"threadId": "thread-1", "item": item})
				complete(turn)
				continue
			}
			notify("item/started", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "pwd"}})
			notify("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "pwd", "status": "completed", "aggregatedOutput": "/work"}})
			complete(turn)
		case "turn/interrupt":
			response(map[string]any{})
			notify("serverRequest/resolved", map[string]any{"threadId": "thread-1", "requestId": 7})
			notify("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": turn, "status": "interrupted"}})
			turn = "turn-2"
			scenario = "success"
		case "turn/steer":
			if p["expectedTurnId"] != turn {
				os.Exit(18)
			}
			response(map[string]any{"turnId": turn})
			if scenario == "queue" {
				notify("item/started", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "cmd-2", "type": "commandExecution", "command": "pwd"}})
				notify("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "cmd-2", "type": "commandExecution", "command": "pwd", "status": "completed"}})
			}
		case "thread/unsubscribe", "thread/backgroundTerminals/clean":
			if record := os.Getenv("THREAVIA_CODEX_TEST_RECORD"); record != "" && msg.Method != "thread/unsubscribe" {
				file, _ := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				_, _ = fmt.Fprintf(file, "%s %s\n", msg.Method, str(p, "threadId"))
				_ = file.Close()
			}
			response(map[string]any{})
		default:
			if string(msg.ID) == "9" {
				// Another thread's request is refused, never answered for it.
				if msg.Error == nil {
					os.Exit(25)
				}
				continue
			}
			if string(msg.ID) == "8" {
				var result map[string]any
				_ = json.Unmarshal(msg.Result, &result)
				if result["decision"] != "accept" {
					os.Exit(26)
				}
				notify("item/completed", map[string]any{"threadId": "child-1", "item": map[string]any{"id": "child-cmd", "type": "commandExecution", "command": "touch alpha.txt", "status": "completed"}})
				notify("item/completed", map[string]any{"threadId": "child-1", "item": map[string]any{"id": "child-message", "type": "agentMessage", "text": "alpha written"}})
				notify("thread/tokenUsage/updated", map[string]any{"threadId": "child-1", "turnId": "child-turn", "tokenUsage": map[string]any{"last": map[string]any{"inputTokens": 50, "outputTokens": 5}}})
				notify("turn/completed", map[string]any{"threadId": "child-1", "turn": map[string]any{"id": "child-turn", "status": "completed"}})
				notify("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"type": "subAgentActivity", "id": "subagent-completed-child-turn", "kind": "completed", "agentThreadId": "child-1", "agentPath": "/root/alpha"}})
				notify("turn/plan/updated", map[string]any{"threadId": "thread-1", "turnId": turn, "explanation": "Two steps.", "plan": []any{
					map[string]any{"step": "Spawn alpha", "status": "completed"}, map[string]any{"step": "Report", "status": "inProgress"}}})
				complete(turn)
				continue
			}
			if string(msg.ID) == "7" {
				var result map[string]any
				_ = json.Unmarshal(msg.Result, &result)
				if scenario == "interrupt" {
					continue
				}
				if scenario == "queue" {
					scenario = "success"
				}
				complete(turn)
				turn = "turn-2"
			}
		}
	}
	os.Exit(0)
}

func TestRunAndResume(t *testing.T) {
	for _, scenario := range []string{"success", "resume", "failed", "crash", "malformed", "resume-error"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, scenario)
			if strings.HasPrefix(scenario, "resume") {
				p.NativeSessionID = "thread-1"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Run(ctx, p, sink); err != nil {
				t.Fatal(err)
			}
			if scenario == "success" || scenario == "resume" {
				if sink.native != "thread-1" || sink.summary != "done" || sink.code != "" {
					t.Fatalf("unexpected outcome: %+v", sink)
				}
				if sink.usage == nil || sink.usage.InputTokens != 40 || sink.usage.OutputTokens != 20 || sink.usage.CacheReadTokens != 60 {
					t.Fatalf("usage must only account for this turn: %v", sink.usage)
				}
				joined := strings.Join(sink.events, ",")
				if !strings.Contains(joined, "tool:Bash:cmd-1,result:Bash:cmd-1") {
					t.Fatalf("tool events: %s", joined)
				}
			} else if sink.code != "PROVIDER_ERROR" {
				t.Fatalf("expected failure, got %+v", sink)
			}
			terminal := 0
			for _, e := range sink.events {
				if e == "failed" || e == "completed" || e == "cancelled" {
					terminal++
				}
			}
			if terminal != 1 {
				t.Fatalf("got %d terminal events", terminal)
			}
		})
	}
}
func TestCancellationAndDeadline(t *testing.T) {
	for _, scenario := range []string{"cancel", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, "hang")
			sink.started = make(chan struct{})
			if scenario == "deadline" {
				p.Policy.MaxDurationSeconds = 1
			}
			done := make(chan error, 1)
			go func() { done <- c.Run(context.Background(), p, sink) }()
			select {
			case <-sink.started:
			case <-time.After(5 * time.Second):
				t.Fatal("did not start")
			}
			if scenario == "cancel" {
				if err := c.Cancel(p.JobID); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("did not stop")
			}
			if scenario == "deadline" {
				if sink.code != "POLICY_LIMIT" {
					t.Fatalf("got %s", sink.code)
				}
			} else if sink.events[len(sink.events)-1] != "cancelled" {
				t.Fatalf("events: %v", sink.events)
			}
			if !errors.Is(c.Cancel(p.JobID), contract.ErrUnknownJob) {
				t.Fatal("finished job still registered")
			}
		})
	}
}
func TestApprovalDoesNotBlockInterrupt(t *testing.T) {
	c, p, sink := provider(t, "interrupt")
	asker := &testAsker{wait: true, called: make(chan struct{}, 1)}
	c.SetAsker(asker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, p, sink) }()
	select {
	case <-asker.called:
	case <-ctx.Done():
		t.Fatal("approval not received")
	}
	if err := c.Inject(p.JobID, "new instruction", true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("interrupt blocked by approval")
	}
	if sink.code != "" || sink.events[len(sink.events)-1] != "completed" {
		t.Fatalf("unexpected outcome: %+v", sink)
	}
}
func TestActionLimit(t *testing.T) {
	c, p, sink := provider(t, "success")
	p.Policy.MaxActions = 1
	// Normal run has one action and must remain allowed.
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "" {
		t.Fatalf("unexpected limit: %s", sink.code)
	}
}
func TestUnsupportedPolicyFailsBeforeSpawning(t *testing.T) {
	c, p, sink := provider(t, "success")
	p.Policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability(99)}}
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "POLICY_UNSUPPORTED" || sink.native != "" {
		t.Fatalf("unsafe policy accepted: %+v", sink)
	}
}

func TestNextMessageAndActionLimit(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			c, p, sink := provider(t, "queue")
			p.Policy.MaxActions = limit
			release := make(chan struct{})
			asker := &testAsker{approved: true, called: make(chan struct{}, 1), release: release}
			c.SetAsker(asker)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Run(ctx, p, sink) }()
			select {
			case <-asker.called:
			case <-ctx.Done():
				t.Fatal("approval not received")
			}
			if err := c.Inject(p.JobID, "next instruction", false); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("queued turn did not end")
			}
			if limit == 1 {
				if sink.code != "POLICY_LIMIT" {
					t.Fatalf("expected limit, got %+v", sink)
				}
			} else {
				if sink.code != "" || sink.usage == nil || sink.usage.InputTokens != 40 || sink.usage.OutputTokens != 20 {
					t.Fatalf("steered turn must only be accounted for once: %+v", sink)
				}
			}
		})
	}
}

func TestPolicyUpdateStopsAnUnsupportedJob(t *testing.T) {
	c, p, sink := provider(t, "hang")
	sink.started = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background(), p, sink) }()
	select {
	case <-sink.started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	p.Policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability(99)}}
	c.UpdatePolicy(p.JobID, p.Policy)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unsupported policy left the provider running")
	}
	if sink.code != "POLICY_UNSUPPORTED" {
		t.Fatalf("got %s", sink.code)
	}
}

func TestPolicyUpdatedBeforeProviderStarts(t *testing.T) {
	c, p, sink := provider(t, "success")
	updated := p.Policy
	updated.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, Capability: backendv1.PermissionCapability(99)}}
	c.UpdatePolicy(p.JobID, updated)
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "POLICY_UNSUPPORTED" || sink.native != "" {
		t.Fatalf("the runner ignored a policy updated during directory resolution: %+v", sink)
	}
}

func TestCloseStopsActiveWork(t *testing.T) {
	c, p, sink := provider(t, "hang")
	sink.started = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background(), p, sink) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-sink.started:
	case <-ctx.Done():
		t.Fatal("job did not start")
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sink.events[len(sink.events)-1] != "cancelled" {
		t.Fatalf("shutdown did not stop work: %v", sink.events)
	}
}

func TestProviderAuthentication(t *testing.T) {
	for _, scenario := range []string{"apikey", "no-auth"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, scenario)
			if scenario == "apikey" {
				c.options.APIKey = "test-private-key"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Run(ctx, p, sink); err != nil {
				t.Fatal(err)
			}
			if scenario == "no-auth" {
				if sink.code != "AUTHENTICATION_REQUIRED" || sink.native != "" {
					t.Fatalf("unauthenticated provider ran: %+v", sink)
				}
			} else if sink.code != "" {
				t.Fatalf("API-key login failed: %+v", sink)
			}
		})
	}
}
