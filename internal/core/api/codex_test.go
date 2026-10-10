package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	codex "github.com/rclsilver/threavia/internal/backends/codex/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/core/domain"
	sdkclient "github.com/rclsilver/threavia/pkg/backend-sdk/client"
	sdkstate "github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

const codexTestThread = "11111111-1111-4111-8111-111111111111"

// TestCodexBackendResumesAfterValidation drives Core -> SDK -> shared adapter
// -> real provider subprocess -> durable events -> client snapshots.
func TestCodexBackendResumesAfterValidation(t *testing.T) {
	c := newCore(t)
	t.Setenv("THREAVIA_CODEX_E2E_PROCESS", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	providerPath := filepath.Join(t.TempDir(), "codex")
	quoted := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	if err := os.WriteFile(providerPath, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestCodexE2EProcess$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tools := mcp.New(logger)
	if err := tools.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })
	local := codex.New(providerPath, tools, codex.Options{Scratch: t.TempDir()}, logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = local.Close(ctx)
	})
	backendID, credential := c.registerBackend("codex-laptop")
	cfg := adapter.Default()
	cfg.Client.BackendName = "codex"
	cfg.Provider.DefaultWorkingDirectory = t.TempDir()
	cfg.Provider.SkillCachePath = t.TempDir()
	cfg.Provider.ScratchPath = t.TempDir()
	store := sdkstate.NewMemoryStore()
	handler := adapter.New(cfg, local, store, logger)
	tools.SetAsker(handler)
	local.SetAsker(handler)
	clientCfg := cfg.Client
	clientCfg.CoreAddress = "passthrough:///bufnet"
	clientCfg.Token = credential
	clientCfg.BackendName = "codex"
	clientCfg.InstanceName = "codex-laptop"
	clientCfg.TLS.Enabled = false
	clientCfg.HeartbeatInterval = 200 * time.Millisecond
	clientCfg.DialOptions = []grpc.DialOption{c.dialer}
	sdk, err := sdkclient.New(clientCfg, handler, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler.Bind(sdk)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = sdk.Run(ctx) }()
	waitUntil(t, "Codex backend connected", sdk.Connected)
	project := c.createProject("codex-project")
	projectPolicy := guarded()
	projectPolicy["rules"] = []map[string]any{
		{"effect": "ALLOW", "capability": "SHELL", "match": "go test *"},
		{"effect": "ASK", "capability": "SHELL", "match": "touch *"},
		{"effect": "DENY", "capability": "FILE_READ", "match": "secrets/**"},
	}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/policy", projectPolicy, nil, http.StatusOK)
	session := c.startSession(project, backendID, "", "create a file")
	for turn := 0; turn < 2; turn++ {
		waitUntil(t, "Codex approval visible in the client", func() bool { return len(c.snapshot(session).Attention.Validations) == 1 })
		pending := c.snapshot(session)
		validation := pending.Attention.Validations[0]
		c.mustDo(http.MethodPost, "/api/v1/validations/"+validation.ID+"/resolve", map[string]any{"approved": true, "channel": "web"}, nil, http.StatusOK)
		waitUntil(t, "Codex Job completed", func() bool {
			completed := 0
			for _, job := range c.snapshot(session).Jobs {
				if job.Status == "COMPLETED" {
					completed++
				}
			}
			return completed == turn+1
		})
		snapshot := c.snapshot(session)
		if len(snapshot.Runs) != 1 {
			t.Fatalf("resumption created another Run: %v", snapshot.Runs)
		}
		run, err := c.store.RunByID(context.Background(), domain.RunID(snapshot.Runs[0].ID))
		if err != nil {
			t.Fatal(err)
		}
		if run.NativeSessionID == nil || *run.NativeSessionID != codexTestThread {
			t.Fatalf("wrong Codex thread: %v", run.NativeSessionID)
		}
		if turn == 0 {
			c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "continue the same conversation"}, nil, http.StatusCreated)
		}
	}
	if got := c.countEvents(session, "agent.message"); got != 2 {
		t.Fatalf("got %d messages, expected one per turn", got)
	}
}

func TestCodexE2EProcess(t *testing.T) {
	if os.Getenv("THREAVIA_CODEX_E2E_PROCESS") != "1" {
		return
	}
	var hookCommand string
	var policyEndpoint string
	for _, arg := range os.Args {
		if value, ok := strings.CutPrefix(arg, "hooks="); ok {
			_, value, _ = strings.Cut(value, "command=")
			_ = json.NewDecoder(strings.NewReader(value)).Decode(&hookCommand)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if encoder.Encode(v) != nil {
			os.Exit(5)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result map[string]any  `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(6)
		}
		response := func(v any) { send(map[string]any{"id": msg.ID, "result": v}) }
		switch msg.Method {
		case "initialize", "config/read", "skills/extraRoots/set":
			response(map[string]any{})
		case "initialized":
		case "skills/list":
			send(map[string]any{"id": msg.ID, "result": map[string]any{"data": []any{map[string]any{"skills": []any{}}}}})
		case "account/read":
			response(map[string]any{"account": map[string]any{"type": "apiKey"}, "requiresOpenaiAuth": true})
		case "hooks/list":
			var hooks []any
			for _, event := range []string{"preToolUse", "sessionStart"} {
				hooks = append(hooks, map[string]any{"command": hookCommand, "eventName": event, "enabled": true, "handlerType": "command", "key": event, "currentHash": "test-hash"})
			}
			response(map[string]any{"data": []any{map[string]any{"hooks": hooks}}})
		case "thread/start", "thread/resume":
			if msg.Method == "thread/resume" && msg.Params["threadId"] != codexTestThread {
				os.Exit(7)
			}
			config := msg.Params["config"].(map[string]any)
			servers := config["mcp_servers"].(map[string]any)
			endpoint := servers["threavia"].(map[string]any)["url"].(string)
			policyEndpoint = strings.Replace(endpoint, "/mcp/", "/policy/", 1)
			resp, err := http.Post(policyEndpoint, "application/json", strings.NewReader(`{"hook_event_name":"SessionStart"}`))
			if err != nil || resp.StatusCode != http.StatusOK {
				os.Exit(9)
			}
			_ = resp.Body.Close()
			response(map[string]any{"thread": map[string]any{"id": codexTestThread}})
		case "turn/start":
			response(map[string]any{"turn": map[string]any{"id": "turn-1"}})
			for _, action := range []struct {
				command string
				blocked bool
			}{{"go test ./...", false}, {"cat secrets/key", true}, {"touch result.txt", false}} {
				body, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": action.command}})
				resp, err := http.Post(policyEndpoint, "application/json", strings.NewReader(string(body)))
				if err != nil || resp.StatusCode != http.StatusOK {
					os.Exit(10)
				}
				var result map[string]any
				decodeErr := json.NewDecoder(resp.Body).Decode(&result)
				_ = resp.Body.Close()
				if decodeErr != nil || (result["decision"] == "block") != action.blocked {
					os.Exit(11)
				}
			}
			send(map[string]any{"method": "item/started", "params": map[string]any{"threadId": codexTestThread, "item": map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "touch result.txt", "status": "inProgress"}}})
			send(map[string]any{"id": 42, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": codexTestThread, "turnId": "turn-1", "itemId": "cmd-1", "command": "touch result.txt"}})
		default:
			if string(msg.ID) != "42" {
				continue
			}
			if msg.Result["decision"] != "accept" {
				os.Exit(8)
			}
			send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": codexTestThread, "item": map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "touch result.txt", "status": "completed"}}})
			send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": codexTestThread, "item": map[string]any{"id": "reply", "type": "agentMessage", "text": "done"}}})
			send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": codexTestThread, "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
		}
	}
	os.Exit(0)
}
