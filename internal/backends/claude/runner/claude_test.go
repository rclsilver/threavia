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
	"github.com/rclsilver/threavia/internal/backends/claude/workspace"
)

// recordingSink captures everything the runner normalises out of provider
// output.
type recordingSink struct {
	mu        sync.Mutex
	events    []string
	messages  []string
	tools     []string
	native    string
	workspace workspace.Summary
	usage     *backendv1.Usage
	summary   string
	failure   string
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

func (s *recordingSink) WorkspaceChanged(_ context.Context, _, _ string, summary workspace.Summary) error {
	s.mu.Lock()
	s.workspace = summary
	s.mu.Unlock()
	s.record("workspace.changed files=%d +%d -%d", len(summary.Files), summary.Additions, summary.Deletions)
	return nil
}

func (s *recordingSink) JobCompleted(_ context.Context, _, _, summary string, usage *backendv1.Usage) error {
	s.mu.Lock()
	s.summary = summary
	s.usage = usage
	s.mu.Unlock()
	s.record("job.completed")
	return nil
}

func (s *recordingSink) JobFailed(_ context.Context, _, _, code, message string, usage *backendv1.Usage) error {
	s.mu.Lock()
	s.failure = code + ": " + message
	s.usage = usage
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

	// The first message only: stdin stays open while the Job runs, and reading
	// to its end would wait for a result this script has not printed yet.
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nIFS= read -r first\n" + script
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

	return runner.NewClaude(binary, tools, runner.Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

// TestRunReportsWhatTheJobConsumed pins the accounting of a Job: Core shows
// what a turn cost, and it can only do that if the runner normalises what the
// provider reported on its result line.
func TestRunReportsWhatTheJobConsumed(t *testing.T) {
	t.Parallel()

	script := `
cat <<'OUT'
{"type":"result","subtype":"success","is_error":false,"result":"fait","total_cost_usd":0.0736,"usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":10811,"cache_creation_input_tokens":8920}}
OUT
`
	binary, _ := fakeClaude(t, script)
	sink := &recordingSink{}

	if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", Prompt: "Analyse", WorkingDirectory: t.TempDir(),
	}, sink); err != nil {
		t.Fatalf("running the job: %v", err)
	}

	usage := sink.usage
	if usage == nil {
		t.Fatal("the job reported no usage although the provider did")
	}
	if usage.GetInputTokens() != 2 || usage.GetOutputTokens() != 4 {
		t.Errorf("tokens = %d in, %d out; want 2 and 4", usage.GetInputTokens(), usage.GetOutputTokens())
	}
	// The cache halves are kept apart: in a long Session they dwarf fresh input,
	// and reading them as input would make every turn look enormous.
	if usage.GetCacheReadTokens() != 10811 || usage.GetCacheWriteTokens() != 8920 {
		t.Errorf("cache = %d read, %d written; want 10811 and 8920",
			usage.GetCacheReadTokens(), usage.GetCacheWriteTokens())
	}
	if usage.GetCostUsd() != 0.0736 {
		t.Errorf("cost = %v, want 0.0736", usage.GetCostUsd())
	}
}

// TestAJobWithoutAccountingReportsNone pins the distinction that matters: a
// provider that says nothing must not produce a Job that looks free.
func TestAJobWithoutAccountingReportsNone(t *testing.T) {
	t.Parallel()

	binary, _ := fakeClaude(t,
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"fait"}'`)
	sink := &recordingSink{}

	if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
		RunID: "run-1", JobID: "job-1", Prompt: "Analyse", WorkingDirectory: t.TempDir(),
	}, sink); err != nil {
		t.Fatalf("running the job: %v", err)
	}
	if sink.usage != nil {
		t.Fatalf("usage = %v, want none reported", sink.usage)
	}
}

// TestAMessageReachesTheTurnUnderWay pins the "next" delivery: a message sent
// while the Job runs is written to the same process, the Job ends with the
// turn that read it, and the process is then told to finish.
func TestAMessageReachesTheTurnUnderWay(t *testing.T) {
	t.Parallel()

	received := filepath.Join(t.TempDir(), "received")
	script := `
echo '{"type":"user","isReplay":true,"message":{"role":"user","content":"first"}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}'
IFS= read -r second
printf '%s\n' "$second" > ` + received + `
echo '{"type":"user","isReplay":true,"message":{"role":"user","content":"second"}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"both read"}'
# The runner closes stdin once every message was read: anything else here
# would hang the test rather than pass it.
if IFS= read -r more; then echo '{"type":"result","subtype":"success","is_error":false,"result":"stdin left open"}'; fi
`
	binary, argsFile := fakeClaude(t, script)
	sink := &recordingSink{}
	claude := newRunner(t, binary)

	done := make(chan error, 1)
	go func() {
		done <- claude.Run(context.Background(), runner.StartParams{
			RunID: "run-1", JobID: "job-1", Prompt: "first", WorkingDirectory: t.TempDir(),
		}, sink)
	}()
	waitFor(t, "the agent to speak", func() bool { return contains(sink.timeline(), "agent.message") })

	if err := claude.Inject("job-1", "also this", false); err != nil {
		t.Fatalf("injecting a message: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("running the job: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the job did not finish once its last message was answered")
	}

	if sink.summary != "both read" {
		t.Errorf("summary = %q, want the turn that read the second message", sink.summary)
	}
	line, err := os.ReadFile(received)
	if err != nil {
		t.Fatalf("reading what the provider received: %v", err)
	}
	if !strings.Contains(string(line), `"type":"user"`) || !strings.Contains(string(line), "also this") {
		t.Errorf("the provider received %s, want a user message", line)
	}
	args := readArgs(t, argsFile)
	if !hasFlag(args, "--input-format", "stream-json") || !contains(args, "--replay-user-messages") {
		t.Errorf("args %v must keep stdin open as stream-json and echo what was read", args)
	}

	if err := claude.Inject("job-1", "too late", false); err != runner.ErrUnknownJob {
		t.Errorf("injecting into a finished job: got %v, want ErrUnknownJob", err)
	}
}

// TestAMessageCanInterruptTheTurn pins the "now" delivery: the turn under way
// is interrupted, the message starts the next one in the same process, and
// the Job is judged on that last turn rather than on the interruption.
func TestAMessageCanInterruptTheTurn(t *testing.T) {
	t.Parallel()

	received := filepath.Join(t.TempDir(), "received")
	script := `
echo '{"type":"user","isReplay":true,"message":{"role":"user","content":"first"}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}'
IFS= read -r control
IFS= read -r message
printf '%s\n%s\n' "$control" "$message" > ` + received + `
echo '{"type":"result","subtype":"error_during_execution","is_error":true,"total_cost_usd":0.01,"usage":{"input_tokens":10,"output_tokens":5}}'
echo '{"type":"user","isReplay":true,"message":{"role":"user","content":"instead"}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"redirected","total_cost_usd":0.03,"usage":{"input_tokens":4,"output_tokens":2}}'
if IFS= read -r more; then echo '{"type":"result","subtype":"success","is_error":false,"result":"stdin left open"}'; fi
`
	binary, _ := fakeClaude(t, script)
	sink := &recordingSink{}
	claude := newRunner(t, binary)

	done := make(chan error, 1)
	go func() {
		done <- claude.Run(context.Background(), runner.StartParams{
			RunID: "run-1", JobID: "job-1", Prompt: "first", WorkingDirectory: t.TempDir(),
		}, sink)
	}()
	waitFor(t, "the agent to speak", func() bool { return contains(sink.timeline(), "agent.message") })

	if err := claude.Inject("job-1", "do this instead", true); err != nil {
		t.Fatalf("interrupting with a message: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("running the job: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the job did not finish once its last message was answered")
	}

	lines, err := os.ReadFile(received)
	if err != nil {
		t.Fatalf("reading what the provider received: %v", err)
	}
	sent := strings.Split(strings.TrimSpace(string(lines)), "\n")
	if len(sent) != 2 || !strings.Contains(sent[0], `"subtype":"interrupt"`) || !strings.Contains(sent[1], "do this instead") {
		t.Fatalf("the provider received %q, want an interrupt then the message", sent)
	}

	if !contains(sink.timeline(), "job.completed") || sink.summary != "redirected" {
		t.Errorf("timeline = %v, summary = %q: an interrupted turn followed by an answer is a completed job",
			sink.timeline(), sink.summary)
	}
	if sink.usage == nil || sink.usage.InputTokens != 14 || sink.usage.CostUsd != 0.03 {
		t.Errorf("usage = %v, want the tokens of both turns and the process cost so far", sink.usage)
	}
}

// TestAToolThatNeedsValidationIsAskedInEveryMode pins that a Core Tool which
// widens where future runs execute reaches the permission tool even in
// SUPERVISED, where the provider's own reviewer would otherwise settle the
// call: an ask rule is answered before the reviewer and before any allow.
func TestAToolThatNeedsValidationIsAskedInEveryMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []backendv1.ExecutionMode{
		backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE,
		backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
	} {
		binary, argsFile := fakeClaude(t, `echo '{"type":"result","subtype":"success","is_error":false,"result":"ok"}'`)
		if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{
			RunID: "run-1", JobID: "job-1", WorkingDirectory: t.TempDir(),
			Policy: policy.Policy{Mode: mode},
			CoreTools: []mcp.CoreTool{
				{Name: "working_directory_set", RequiresValidation: true},
				{Name: "task_create"},
			},
		}, &recordingSink{}); err != nil {
			t.Fatalf("%s: running the job: %v", mode, err)
		}

		args := readArgs(t, argsFile)
		settings := ""
		for i, arg := range args {
			if arg == "--settings" && i+1 < len(args) {
				settings = args[i+1]
			}
		}
		if !strings.Contains(settings, `"mcp__threavia__working_directory_set"`) {
			t.Errorf("%s: settings %s must ask about working_directory_set", mode, settings)
		}
		if strings.Contains(settings, "task_create") {
			t.Errorf("%s: settings %s must not ask about task_create", mode, settings)
		}
	}
}
