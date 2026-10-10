package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/backends/shared/policy"
)

func TestApprovalMapping(t *testing.T) {
	for _, approved := range []bool{false, true} {
		c, _, _ := provider(t, "success")
		asker := &testAsker{approved: approved}
		c.SetAsker(asker)
		live := &execution{policy: policy.Policy{AllowFilesystemWrite: true}, items: map[string]map[string]any{
			"edit": {"changes": []any{map[string]any{"path": "a.go"}, map[string]any{"path": "b.go"}}},
			"cmd":  {"command": "touch a.go"},
		}}
		for _, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval"} {
			item := "cmd"
			if method == "item/fileChange/requestApproval" {
				item = "edit"
			}
			result, err := c.answer(context.Background(), live, "job", method, map[string]any{"itemId": item})
			if err != nil {
				t.Fatal(err)
			}
			decision := result.(map[string]any)["decision"]
			want := "decline"
			if approved {
				want = "accept"
			}
			if decision != want {
				t.Fatalf("%s: got %v, want %s", method, decision, want)
			}
		}
		if approved && len(asker.calls) != 3 {
			t.Fatalf("each changed path must be checked, got %v", asker.calls)
		}
	}
}
func TestBroadPermissionsAreNotGranted(t *testing.T) {
	c, _, _ := provider(t, "success")
	live := &execution{policy: policy.Policy{AllowFilesystemWrite: true}}
	result, err := c.answer(context.Background(), live, "job", "item/permissions/requestApproval", map[string]any{"permissions": map[string]any{"network": map[string]any{"enabled": true}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != "{\"permissions\":{},\"scope\":\"turn\"}" {
		t.Fatalf("granted permissions: %s", encoded)
	}
	result, err = c.answer(context.Background(), live, "job", "item/commandExecution/requestApproval", map[string]any{"command": "echo hello", "additionalPermissions": map[string]any{"network": map[string]any{"enabled": true}}})
	if err != nil || result.(map[string]any)["decision"] != "decline" {
		t.Fatalf("broad command overlay was granted: %v, %v", result, err)
	}
}
func TestReadOnlyPolicyRejectsSandboxEscape(t *testing.T) {
	c, _, _ := provider(t, "success")
	result, err := c.answer(context.Background(), &execution{}, "job", "item/commandExecution/requestApproval", map[string]any{"command": "touch a.go"})
	if err != nil || result.(map[string]any)["decision"] != "decline" {
		t.Fatalf("read-only escape was accepted: %v, %v", result, err)
	}
}
func TestUnknownRequestFailsClosed(t *testing.T) {
	c, _, _ := provider(t, "success")
	if _, err := c.answer(context.Background(), &execution{}, "job", "unknown/request", nil); err == nil {
		t.Fatal("unknown request was accepted")
	}
}

func TestOnlyControlledMCPElicitationIsAccepted(t *testing.T) {
	c, _, _ := provider(t, "success")
	for _, server := range []string{"threavia", "unrelated"} {
		for _, mode := range []string{"form", "url", "openai/userVerification"} {
			result, err := c.answer(context.Background(), &execution{}, "job", "mcpServer/elicitation/request", map[string]any{"serverName": server, "mode": mode})
			if err != nil {
				t.Fatal(err)
			}
			if (result.(map[string]any)["action"] == "accept") != (server == "threavia" && mode == "form") {
				t.Fatal("incorrect MCP consent", server, mode, result)
			}
		}
	}
}
func TestUserQuestions(t *testing.T) {
	c, _, _ := provider(t, "success")
	result, err := c.answer(context.Background(), &execution{}, "job", "item/tool/requestUserInput", map[string]any{"questions": []any{
		map[string]any{"id": "first", "question": "Which branch?"}, map[string]any{"id": "second", "question": "Which directory?"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != "{\"answers\":{\"first\":{\"answers\":[\"answer\"]},\"second\":{\"answers\":[\"answer\"]}}}" {
		t.Fatalf("unexpected answers: %s", encoded)
	}
}

// Opt-in integration smoke test: only local RPCs, no inference and no token
// values in the output. Run with THREAVIA_CODEX_SMOKE=1.
func TestInstalledCodexHandshake(t *testing.T) {
	if os.Getenv("THREAVIA_CODEX_SMOKE") != "1" {
		t.Skip("opt-in local Codex protocol smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	diagnostics := &tailWriter{}
	cmd.Stderr = diagnostics
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = input.Close(); _ = cmd.Wait() }()
	r := newRPC(ctx, input, output)
	if err := r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "threavia", "version": "dev"}, "capabilities": map[string]any{"experimentalApi": true}}, nil); err != nil {
		if strings.Contains(diagnostics.String(), "Read-only file system") {
			t.Fatal("Codex cannot start: its state directory is read-only in this environment")
		}
		t.Fatal(err)
	}
	if err := r.send(map[string]any{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := r.call(ctx, "config/read", map[string]any{"includeLayers": true}, &response); err != nil {
		t.Fatal(err)
	}
	if response["config"] == nil {
		t.Fatal("configuration response missing config")
	}
	if err := r.call(ctx, "account/read", map[string]any{"refreshToken": false}, &response); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeRulesCannotWidenThePolicy(t *testing.T) {
	dir := t.TempDir()
	if found, err := hasNativeRules(dir); err != nil || found {
		t.Fatalf("empty native config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "default.rules"), []byte("prefix_rule(pattern=[\"git\",\"push\"],decision=\"allow\")"), 0600); err != nil {
		t.Fatal(err)
	}
	if found, err := hasNativeRules(dir); err != nil || !found {
		t.Fatalf("native allow rule not detected: %v", err)
	}
}
