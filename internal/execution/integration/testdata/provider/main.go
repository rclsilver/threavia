// A deterministic CLI fixture. It implements the provider transports used by
// the real runners; it never contacts an inference service or reads credentials.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--network-probe" {
		conn, err := net.DialTimeout("tcp", os.Args[2], 2*time.Second)
		if conn != nil {
			conn.Close()
		}
		if (err == nil) != (os.Args[3] == "allow") {
			fmt.Fprintln(os.Stderr, "network policy probe mismatch:", err)
			os.Exit(8)
		}
		fmt.Println("network policy probe:", os.Args[3])
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--spawn-detached-heartbeat" {
		cmd := exec.Command(os.Args[0], "--heartbeat", os.Args[2])
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		fmt.Println("detached child started")
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--heartbeat" {
		for {
			if err := os.WriteFile(os.Args[2], []byte(time.Now().Format(time.RFC3339Nano)), 0600); err != nil {
				panic(err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "--native-codex-smoke" {
		nativeSmoke()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--echo-server" {
		if err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "reachable") })); err != nil {
			os.Exit(6)
		}
		return
	}
	if strings.Contains(filepath.Base(os.Args[0]), "claude") {
		claude()
		return
	}
	codex()
}

// Exercise the actual provider's command execution protocol, without a model,
// API key or inference request. Runtime mounts supply the external boundary.
func nativeSmoke() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	input, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		panic(err)
	}
	defer func() { input.Close(); cmd.Process.Kill(); cmd.Wait() }()
	encoder := json.NewEncoder(input)
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	call := func(id int, method string, params any) map[string]any {
		if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			panic(err)
		}
		for scanner.Scan() {
			var response map[string]any
			if json.Unmarshal(scanner.Bytes(), &response) != nil {
				panic("invalid native response")
			}
			if response["id"] == float64(id) {
				if response["error"] != nil {
					panic(fmt.Sprint(response["error"]))
				}
				result, _ := response["result"].(map[string]any)
				return result
			}
		}
		panic("native provider exited without response")
	}
	call(1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "threavia-runtime-smoke", "version": "test"}})
	_ = encoder.Encode(map[string]any{"method": "initialized", "params": map[string]any{}})
	result := call(2, "command/exec", map[string]any{"command": []string{"bash", "-c", "test -r /etc/os-release && if touch /workspace/forbidden; then exit 11; fi; if touch /forbidden; then exit 12; fi; printf external-sandbox-ok"}, "cwd": "/workspace", "sandboxPolicy": map[string]any{"type": "externalSandbox", "networkAccess": "enabled"}})
	if result["exitCode"] != float64(0) || !strings.Contains(fmt.Sprint(result["stdout"]), "external-sandbox-ok") {
		panic(fmt.Sprintf("external sandbox failed: %+v", result))
	}
	fmt.Println("Codex native externalSandbox: read succeeded, writes denied by container mounts")
}
func send(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		os.Exit(2)
	}
}
func check(writable bool) string {
	for _, key := range []string{"THREAVIA_BACKEND_TOKEN", "THREAVIA_EXECUTION_TOKEN"} {
		if os.Getenv(key) != "" {
			return "private control environment leaked"
		}
	}
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		return "service account token leaked"
	}
	if _, err := os.Stat("/private-control/backend.db"); err == nil {
		return "backend private state leaked"
	}
	for _, path := range []string{"/state/executor.json", "/executor-clients/codex/token", "/executor-clients/claude/token"} {
		if _, err := os.Stat(path); err == nil {
			return "private control file leaked"
		}
	}
	if err := os.WriteFile("/workspace/fixture-write", []byte("write"), 0600); (err == nil) != writable {
		return "workspace mount permissions do not match policy"
	}
	if err := os.WriteFile("/fixture-root-write", []byte("no"), 0600); err == nil {
		return "container root filesystem is writable"
	}
	manifests, _ := filepath.Glob("/shared/skills/jobs/*/skills/fixture/SKILL.md")
	if len(manifests) != 1 {
		return "per-Job skills mount missing or exposes other Jobs"
	}
	if _, err := os.ReadFile(manifests[0]); err != nil {
		return "skill bundle symlink is not readable"
	}
	if err := os.WriteFile(manifests[0], []byte("tampered"), 0644); err == nil {
		return "skill bundle is writable"
	}
	dirs, err := os.ReadDir("/shared/scratch")
	if err != nil || len(dirs) != 1 {
		return "per-Run scratch mount missing or exposes other Runs"
	}
	if err := os.WriteFile(filepath.Join("/shared/scratch", dirs[0].Name(), "fixture-scratch"), []byte("private run scratch"), 0600); err != nil {
		return "Run scratch is not writable: " + err.Error()
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "fixture-session"), []byte("native session"), 0600); err != nil {
		return "provider home not writable: " + err.Error()
	}
	return "fixture completed"
}
func claude() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("--effort <effort> low medium high")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		send(map[string]any{"loggedIn": true})
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	send(map[string]any{"type": "system", "subtype": "init", "session_id": "fixture", "tools": []any{}})
	text := check(strings.Contains(scanner.Text(), "EXPECT_WORKSPACE_WRITE"))
	send(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}})
	send(map[string]any{"type": "result", "subtype": "success", "is_error": text != "fixture completed", "result": text, "session_id": "fixture"})
}
func codex() {
	hook := ""
	var skillRoots []string
	re := regexp.MustCompile(`command=("(?:[^"\\]|\\.)*")`)
	for _, arg := range os.Args {
		if m := re.FindStringSubmatch(arg); len(m) == 2 {
			_ = json.Unmarshal([]byte(m[1]), &hook)
		}
	}
	notify := func(method string, params any) { send(map[string]any{"method": method, "params": params}) }
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 64<<20)
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage
			Method string
			Params map[string]any
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(3)
		}
		if len(msg.ID) == 0 {
			continue
		}
		response := func(v any) { send(map[string]any{"id": msg.ID, "result": v}) }
		switch msg.Method {
		case "initialize":
			response(map[string]any{"userAgent": "fixture"})
		case "account/read":
			response(map[string]any{"account": map[string]any{"type": "apiKey"}, "requiresOpenaiAuth": false})
		case "config/read":
			response(map[string]any{"config": map[string]any{"model": "fixture-model"}})
		case "model/list":
			response(map[string]any{"data": []any{map[string]any{"model": "fixture-model", "displayName": "Fixture", "isDefault": true}}})
		case "skills/list":
			list := []any{}
			for _, root := range skillRoots {
				manifest := filepath.Join(root, "SKILL.md")
				if _, err := os.ReadFile(manifest); err != nil {
					panic(err)
				}
				list = append(list, map[string]any{"name": "fixture", "path": manifest, "enabled": true})
			}
			response(map[string]any{"data": []any{map[string]any{"skills": list}}})
		case "skills/extraRoots/set":
			skillRoots = nil
			for _, root := range msg.Params["extraRoots"].([]any) {
				skillRoots = append(skillRoots, root.(string))
			}
			response(map[string]any{})
		case "hooks/list":
			list := []any{}
			for _, event := range []string{"preToolUse", "sessionStart"} {
				list = append(list, map[string]any{"command": hook, "eventName": event, "enabled": true, "handlerType": "command", "key": event, "currentHash": "fixture"})
			}
			response(map[string]any{"data": []any{map[string]any{"hooks": list}}})
		case "thread/start", "thread/resume":
			if msg.Method == "thread/resume" {
				if _, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "fixture-session")); err != nil {
					send(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32000, "message": "native session was lost"}})
					continue
				}
			}
			response(map[string]any{"thread": map[string]any{"id": "fixture-thread"}})
		case "turn/start":
			if sandbox, _ := msg.Params["sandboxPolicy"].(map[string]any); sandbox["type"] != "externalSandbox" {
				panic("Kubernetes worker did not select externalSandbox")
			}
			endpoint := os.Getenv("THREAVIA_CODEX_POLICY_ENDPOINT")
			res, err := http.Post(endpoint, "application/json", bytes.NewBufferString(`{"hook_event_name":"SessionStart"}`))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(4)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != 200 {
				os.Exit(5)
			}
			response(map[string]any{"turn": map[string]any{"id": "fixture-turn", "status": "inProgress"}})
			raw, _ := json.Marshal(msg.Params)
			text := check(strings.Contains(string(raw), "EXPECT_WORKSPACE_WRITE"))
			notify("item/completed", map[string]any{"threadId": "fixture-thread", "item": map[string]any{"type": "agentMessage", "id": "fixture-message", "text": text}})
			notify("turn/completed", map[string]any{"threadId": "fixture-thread", "turn": map[string]any{"id": "fixture-turn", "status": "completed"}})
		default:
			response(map[string]any{})
		}
	}
}
