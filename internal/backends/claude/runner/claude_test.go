package runner_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
	"github.com/rclsilver/threavia/internal/backends/claude/policy"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
)

// recordingSink captures everything the runner normalises out of provider
// output.
type recordingSink struct {
	mu       sync.Mutex
	events   []string
	messages []string
	tools    []string
	native   string
	summary  string
	failure  string
}

func (s *recordingSink) record(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, fmt.Sprintf(format, args...))
}

func (s *recordingSink) JobStarted(context.Context, string, string) error {
	s.record("job.started")
	return nil
}

func (s *recordingSink) NativeSessionBound(_ context.Context, _, _, nativeSessionID string) error {
	s.mu.Lock()
	s.native = nativeSessionID
	s.mu.Unlock()
	s.record("native.bound")
	return nil
}

func (s *recordingSink) AgentMessage(_ context.Context, _, _, text string) error {
	s.mu.Lock()
	s.messages = append(s.messages, text)
	s.mu.Unlock()
	s.record("agent.message")
	return nil
}

func (s *recordingSink) ToolStarted(_ context.Context, _, _, callID, name string, input map[string]any) error {
	s.mu.Lock()
	s.tools = append(s.tools, "started:"+name+":"+fmt.Sprint(input["file_path"]))
	s.mu.Unlock()
	s.record("tool.started")
	return nil
}

func (s *recordingSink) ToolCompleted(_ context.Context, _, _, callID, name string, output map[string]any) error {
	s.mu.Lock()
	s.tools = append(s.tools, "completed:"+name+":"+fmt.Sprint(output["output"]))
	s.mu.Unlock()
	s.record("tool.completed")
	return nil
}

func (s *recordingSink) ToolFailed(_ context.Context, _, _, callID, name, message string) error {
	s.mu.Lock()
	s.tools = append(s.tools, "failed:"+name+":"+message)
	s.mu.Unlock()
	s.record("tool.failed")
	return nil
}

func (s *recordingSink) JobCompleted(_ context.Context, _, _, summary string) error {
	s.mu.Lock()
	s.summary = summary
	s.mu.Unlock()
	s.record("job.completed")
	return nil
}

func (s *recordingSink) JobFailed(_ context.Context, _, _, code, message string) error {
	s.mu.Lock()
	s.failure = code + ": " + message
	s.mu.Unlock()
	s.record("job.failed")
	return nil
}

func (s *recordingSink) JobCancelled(context.Context, string, string) error {
	s.record("job.cancelled")
	return nil
}

func (s *recordingSink) Progress(context.Context, string, string, string, string) {}

func (s *recordingSink) timeline() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// fakeClaude writes a stand-in for the Claude Code CLI that records its
// arguments and prints canned stream-json, so the parsing and the process
// lifecycle are tested without spending a real provider session.
func fakeClaude(t *testing.T, script string) (binary, argsFile string) {
	t.Helper()

	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	binary = filepath.Join(dir, "claude")

	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > /dev/null\n" + script
	if err := os.WriteFile(binary, []byte(content), 0o700); err != nil {
		t.Fatalf("writing the fake provider: %v", err)
	}
	return binary, argsFile
}

func newRunner(t *testing.T, binary string) *runner.Claude {
	t.Helper()

	tools := mcp.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	tools.SetAsker(nil)
	if err := tools.Start(); err != nil {
		t.Fatalf("starting the tool endpoint: %v", err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })

	return runner.NewClaude(binary, tools, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the provider arguments: %v", err)
	}
	return strings.Split(strings.TrimRight(string(content), "\n"), "\n")
}

func hasFlag(args []string, flag, value string) bool {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

// TestRunNormalisesProviderOutput pins the translation from Claude Code
// stream-json into the provider-independent Threavia vocabulary.
func TestRunNormalisesProviderOutput(t *testing.T) {
	t.Parallel()

	script := `
cat <<'OUT'
{"type":"system","subtype":"init","session_id":"ignored","tools":[]}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Je regarde le projet."}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"Read","input":{"file_path":"/tmp/foo"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"contenu"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"termine","session_id":"ignored"}
OUT
`
	binary, argsFile := fakeClaude(t, script)
	sink := &recordingSink{}

	err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", Prompt: "Analyse ce projet",
		WorkingDirectory: t.TempDir(), ProjectName: "homelab",
	}, sink)
	if err != nil {
		t.Fatalf("running the job: %v", err)
	}

	want := []string{"job.started", "native.bound", "agent.message", "tool.started", "tool.completed", "job.completed"}
	got := sink.timeline()
	if len(got) != len(want) {
		t.Fatalf("timeline = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("timeline = %v, want %v", got, want)
		}
	}

	if len(sink.messages) != 1 || sink.messages[0] != "Je regarde le projet." {
		t.Errorf("agent messages = %v", sink.messages)
	}
	if len(sink.tools) != 2 || sink.tools[0] != "started:Read:/tmp/foo" || sink.tools[1] != "completed:Read:contenu" {
		t.Errorf("tool events = %v", sink.tools)
	}
	if sink.summary != "termine" {
		t.Errorf("summary = %q, want %q", sink.summary, "termine")
	}

	// A fresh Run gets a provider session id minted by the backend, so the Run
	// is resumable from its very first message.
	args := readArgs(t, argsFile)
	if !hasFlag(args, "--session-id", sink.native) {
		t.Errorf("args %v must create the session %q", args, sink.native)
	}
	for _, flag := range []string{"--print", "--verbose", "--strict-mcp-config"} {
		if !contains(args, flag) {
			t.Errorf("args %v must contain %s", args, flag)
		}
	}
	if !hasFlag(args, "--permission-prompt-tool", mcp.PermissionTool) {
		t.Errorf("args %v must route permission prompts through threavia", args)
	}
	if !hasFlag(args, "--output-format", "stream-json") {
		t.Errorf("args %v must request stream-json", args)
	}
}

// TestRunResumesTheNativeSession pins that a second Job continues the same
// provider session rather than starting a new one.
func TestRunResumesTheNativeSession(t *testing.T) {
	t.Parallel()

	binary, argsFile := fakeClaude(t, `echo '{"type":"result","subtype":"success","is_error":false,"result":"ok"}'`)
	sink := &recordingSink{}

	err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-2", NativeSessionID: "native-42", Prompt: "Et maintenant",
		WorkingDirectory: t.TempDir(),
	}, sink)
	if err != nil {
		t.Fatalf("running the job: %v", err)
	}

	args := readArgs(t, argsFile)
	if !hasFlag(args, "--resume", "native-42") {
		t.Fatalf("args %v must resume the existing provider session", args)
	}
	if contains(args, "--session-id") {
		t.Fatalf("args %v must not create a session while resuming", args)
	}
	if sink.native != "native-42" {
		t.Errorf("bound native session = %q, want native-42", sink.native)
	}
}

// TestProviderErrorBecomesAFailedJob pins that a provider error is reported as
// a failure rather than as a completion.
func TestProviderErrorBecomesAFailedJob(t *testing.T) {
	t.Parallel()

	binary, _ := fakeClaude(t, `echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}'`)
	sink := &recordingSink{}

	if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
	}, sink); err != nil {
		t.Fatalf("running the job: %v", err)
	}
	if !strings.Contains(sink.failure, "boom") {
		t.Fatalf("failure = %q, want the provider error", sink.failure)
	}
}

// TestProviderCrashIsReported pins that a process dying without a result is a
// failure, not a silent success.
func TestProviderCrashIsReported(t *testing.T) {
	t.Parallel()

	binary, _ := fakeClaude(t, `echo "something went very wrong" >&2; exit 3`)
	sink := &recordingSink{}

	if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
	}, sink); err != nil {
		t.Fatalf("running the job: %v", err)
	}
	if !strings.Contains(sink.failure, "something went very wrong") {
		t.Fatalf("failure = %q, want the provider diagnostics", sink.failure)
	}
}

// TestCancelStopsTheProcess covers the backend half of acceptance criterion 11:
// cancelling actually stops the work, and only then is CANCELLED reported.
func TestCancelStopsTheProcess(t *testing.T) {
	t.Parallel()

	// A provider that would run forever, and a child that would outlive a naive
	// kill of the direct process only.
	binary, _ := fakeClaude(t, `sleep 300 & sleep 300`)
	sink := &recordingSink{}
	claude := newRunner(t, binary)

	done := make(chan error, 1)
	go func() {
		done <- claude.Run(context.Background(), runner.StartParams{
			RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
		}, sink)
	}()

	waitFor(t, "the job to start", func() bool {
		return contains(sink.timeline(), "job.started")
	})

	if err := claude.Cancel("job-1"); err != nil {
		t.Fatalf("cancelling the job: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the cancelled run returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled process did not stop")
	}

	timeline := sink.timeline()
	if !contains(timeline, "job.cancelled") {
		t.Fatalf("timeline = %v, want a cancellation", timeline)
	}
	if contains(timeline, "job.completed") || contains(timeline, "job.failed") {
		t.Fatalf("timeline = %v, a cancelled job must not also complete or fail", timeline)
	}
}

// TestCancelUnknownJob pins that cancelling something this backend never ran is
// reported, not silently swallowed: reconciliation depends on the distinction.
func TestCancelUnknownJob(t *testing.T) {
	t.Parallel()

	binary, _ := fakeClaude(t, `true`)
	if err := newRunner(t, binary).Cancel("job-unknown"); err != runner.ErrUnknownJob {
		t.Fatalf("got %v, want ErrUnknownJob", err)
	}
}

// TestMissingProviderIsAFailedJob pins that an absent CLI fails the Job with an
// explanation rather than hanging.
func TestMissingProviderIsAFailedJob(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	claude := newRunner(t, filepath.Join(t.TempDir(), "does-not-exist"))

	if err := claude.Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
	}, sink); err != nil {
		t.Fatalf("running the job: %v", err)
	}
	if !strings.Contains(sink.failure, "PROVIDER_UNAVAILABLE") {
		t.Fatalf("failure = %q, want PROVIDER_UNAVAILABLE", sink.failure)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestActionLimitStopsTheRun pins the half of a policy no permission gate can
// enforce: a Run that asks for nothing can still take too many steps.
func TestActionLimitStopsTheRun(t *testing.T) {
	t.Parallel()

	// Three tool calls, where the policy allows two.
	script := `
cat <<'OUT'
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Read","input":{}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Read","input":{}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"c3","name":"Read","input":{}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"done"}
OUT
`
	binary, _ := fakeClaude(t, script)
	sink := &recordingSink{}

	err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
		Policy: policy.Policy{
			Mode:       backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
			MaxActions: 2,
		},
	}, sink)
	if err != nil {
		t.Fatalf("running the job: %v", err)
	}

	if !strings.Contains(sink.failure, "POLICY_LIMIT") {
		t.Fatalf("failure = %q, want a policy limit", sink.failure)
	}
	if !strings.Contains(sink.failure, "2 actions") {
		t.Errorf("failure = %q, want it to name the limit", sink.failure)
	}
	// It stopped at the limit rather than running to completion.
	if contains(sink.timeline(), "job.completed") {
		t.Error("a run stopped by its policy must not also complete")
	}
	if count := countEntries(sink.timeline(), "tool.started"); count != 2 {
		t.Errorf("%d actions were taken, want the 2 the policy allowed", count)
	}
}

// TestDurationLimitStopsTheRun pins the other limit: wall-clock time.
func TestDurationLimitStopsTheRun(t *testing.T) {
	t.Parallel()

	binary, _ := fakeClaude(t, `sleep 300`)
	sink := &recordingSink{}

	err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
		Policy: policy.Policy{
			Mode:               backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
			MaxDurationSeconds: 1,
		},
	}, sink)
	if err != nil {
		t.Fatalf("running the job: %v", err)
	}
	if !strings.Contains(sink.failure, "POLICY_LIMIT") {
		t.Fatalf("failure = %q, want a policy limit", sink.failure)
	}
}

func countEntries(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}
