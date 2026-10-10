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
	"syscall"
	"time"

	"github.com/google/uuid"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
)

// stopGrace is how long a cancelled process gets to exit on its own before it
// is killed.
const stopGrace = 10 * time.Second

// maxLine bounds one line of provider output. Claude Code emits whole JSON
// objects per line and a tool result can be large.
const maxLine = 8 << 20

// Claude runs the Claude Code CLI.
type Claude struct {
	binary string
	tools  *mcp.Server
	// readable are the directories a Job may read beyond the one it works in.
	readable []string
	// scratch is where each Run gets a directory of its own to write in.
	scratch string
	// available is what a Job can run by name, read from PATH at start.
	available []string
	logger    *slog.Logger

	mu      sync.Mutex
	running map[string]*execution
}

// execution is one live provider process.
type execution struct {
	stop      context.CancelFunc
	cancelled bool

	// The provider reads its messages from stdin as stream-json, which is what
	// lets a message reach a turn already under way. Guarded by the runner
	// mutex, like the fields above.
	stdin io.WriteCloser
	// injected counts the messages written after the first, and consumed how
	// many of them the provider has echoed back as read. The process is told
	// to finish only once a turn ends with nothing written left unread.
	injected int
	consumed int
	// closed is set once stdin is closed: the process is finishing its last
	// turn and nothing more can reach it.
	closed bool
	// interrupts numbers the control requests, which the provider answers by
	// id.
	interrupts int
	// policy is the one the Job started with, whose supervision statement goes
	// in front of every message the Job receives.
	policy policy.Policy
}

// ErrJobFinishing is returned when a message arrives after the provider was
// told the Job is over: it can no longer reach that process.
var ErrJobFinishing = errors.New("the job is finishing and takes no more input")

// Inject hands a message to a Job that is running.
//
// Without interrupt, the provider reads it at its next step and the current
// turn carries on with it in mind: the "next" delivery of the spec. With
// interrupt, the current turn is stopped first and the message starts a new
// one in the same process and the same provider session: "now".
func (c *Claude) Inject(jobID, text string, interrupt bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	live, ok := c.running[jobID]
	if !ok {
		return ErrUnknownJob
	}
	if live.closed || live.stdin == nil {
		return ErrJobFinishing
	}
	if interrupt {
		live.interrupts++
		request, err := json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": fmt.Sprintf("interrupt-%d", live.interrupts),
			"request":    map[string]any{"subtype": "interrupt"},
		})
		if err != nil {
			return err
		}
		if _, err := live.stdin.Write(append(request, '\n')); err != nil {
			return fmt.Errorf("interrupt the provider: %w", err)
		}
	}
	if err := writeUserMessage(live.stdin, supervised(live.policy, text)); err != nil {
		return err
	}
	live.injected++
	return nil
}

// writeUserMessage writes one user message in the provider's stream-json input
// format.
func writeUserMessage(stdin io.Writer, text string) error {
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
	if err != nil {
		return err
	}
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write to the provider: %w", err)
	}
	return nil
}

// consumedInput records that the provider read a message it was sent. The
// first echo is the message that started the Job, which is not counted.
func (c *Claude) consumedInput(jobID string, first bool) {
	if first {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if live, ok := c.running[jobID]; ok {
		live.consumed++
	}
}

// turnEnded closes stdin when a turn ends with every message read, which tells
// the provider to exit once it is done. A message written but not read yet
// keeps the process open: it starts the next turn, or it is the one an
// interrupt made room for.
func (c *Claude) turnEnded(jobID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	live, ok := c.running[jobID]
	if !ok || live.closed || live.consumed < live.injected {
		return
	}
	live.closed = true
	if live.stdin != nil {
		_ = live.stdin.Close()
	}
}

// NewClaude builds the runner. The MCP server is where permission prompts and
// agent questions are turned into Threavia requests.
// Options are what the runner needs from the machine it runs on.
type Options struct {
	// Readable names the directories the agent may read without asking, beyond
	// the one a Job works in. They are the backend's discovery roots: the trees
	// this machine keeps its projects in, which is where a Job legitimately
	// looks when the answer is in the repository next door.
	Readable []string
	// Scratch is where a Run writes the files it will read back.
	Scratch string
}

// NewClaude builds the runner.
func NewClaude(binary string, tools *mcp.Server, opts Options, logger *slog.Logger) *Claude {
	if binary == "" {
		binary = "claude"
	}
	sweepScratch(opts.Scratch, logger)
	return &Claude{
		binary:    binary,
		tools:     tools,
		readable:  opts.Readable,
		scratch:   opts.Scratch,
		available: executablesOnPath(),
		logger:    logger,
		running:   make(map[string]*execution),
	}
}

// sweepScratch removes what previous Runs left behind and nobody came back for.
func sweepScratch(root string, logger *slog.Logger) { runner.SweepScratch(root, logger) }

// ScratchDir names the directory a Run writes its intermediate files in,
// without making it. The permission gate needs the name to say where a file
// belongs, and saying so must not create directories as a side effect.
func ScratchDir(root, runID string) string {
	if root == "" || runID == "" {
		return ""
	}
	return filepath.Join(root, runID)
}

// scratchFor returns the directory this Run writes its intermediate files in,
// making it if it is not there yet.
//
// Per Run rather than per Job, because that is the lifetime the agent works to:
// it writes a file in answer to one message and reads it back in answer to the
// next. A directory emptied between the two would be worse than none, since it
// would fail only sometimes.
func (c *Claude) scratchFor(runID string) string {
	dir := ScratchDir(c.scratch, runID)
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		c.logger.Warn("cannot make the scratch directory, the agent will use its own",
			slog.String("path", dir), slog.String("error", err.Error()))
		return ""
	}
	return dir
}

// executablesOnPath lists, once, what a Job can run by name.
func executablesOnPath() []string { return runner.ExecutablesOnPath() }

// Binary returns the configured executable.
func (c *Claude) Binary() string { return c.binary }

// Available implements Runner. A missing CLI is a DEGRADED condition, not a
// fatal startup error: the backend stays connected and reports why it cannot
// work.
func (c *Claude) Available() error { return lookPath(c.binary) }

// Cancel implements Runner. It stops the whole process group, because the
// provider spawns children of its own.
func (c *Claude) Cancel(jobID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	live, ok := c.running[jobID]
	if !ok {
		return ErrUnknownJob
	}
	live.cancelled = true
	live.stop()
	return nil
}

// Run implements Runner: it executes one Job to completion.
func (c *Claude) Run(ctx context.Context, params StartParams, sink Sink) error {
	if err := c.Available(); err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "PROVIDER_UNAVAILABLE", err.Error(), nil)
	}

	// A per-Job endpoint token: the only process that can reach this Job's
	// prompts is the one started for it.
	token := uuid.NewString()
	endpoint := c.tools.Register(params.JobID, token, params.CoreTools)
	defer c.tools.Unregister(token)

	mcpConfig, err := mcp.Config(endpoint)
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	scratch := c.scratchFor(params.RunID)
	mcpConfigPath, removeConfig, err := writeMCPConfig(scratch, mcpConfig)
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	defer removeConfig()

	// The backend mints the native session id rather than discovering it, so a
	// Run is resumable from its very first message.
	nativeSessionID := params.NativeSessionID
	resuming := nativeSessionID != ""
	if !resuming {
		nativeSessionID = uuid.NewString()
	}

	// The duration limit is enforced here because no permission gate can see
	// time passing: a Run that asks for nothing can still run forever.
	runCtx, stop := context.WithCancel(ctx)
	if seconds := params.Policy.MaxDurationSeconds; seconds > 0 {
		runCtx, stop = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	}
	defer stop()

	live := &execution{stop: stop}
	c.mu.Lock()
	c.running[params.JobID] = live
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.running, params.JobID)
		c.mu.Unlock()
	}()

	// Captured before the process starts, so what the agent changed is told
	// apart from what was already dirty in the working directory.
	before := workspace.Observe(ctx, params.WorkingDirectory)

	cmd := c.command(runCtx, params, scratch, nativeSessionID, resuming, mcpConfigPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	// Kept open while the Job runs: a message sent to it travels the same way
	// as the first one, and the process exits once stdin is closed.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	defer func() { _ = stdin.Close() }()

	c.logger.Info("starting claude code",
		slog.String("jobId", params.JobID),
		slog.String("nativeSessionId", nativeSessionID),
		slog.Bool("resume", resuming),
		slog.String("cwd", params.WorkingDirectory))

	if err := cmd.Start(); err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "SPAWN_FAILED", err.Error(), nil)
	}
	if err := writeUserMessage(stdin, supervised(params.Policy, params.Prompt)); err != nil {
		c.logger.Error("cannot hand the message to the provider", slog.String("error", err.Error()))
	}
	c.mu.Lock()
	live.stdin = stdin
	live.policy = params.Policy
	c.mu.Unlock()

	if err := sink.JobStarted(ctx, params.RunID, params.JobID); err != nil {
		c.logger.Error("cannot report the job start", slog.String("error", err.Error()))
	}
	// Reported straight away: Core records it on the Run, so a later message
	// resumes this exact provider session even if this Job fails.
	if err := sink.NativeSessionBound(ctx, params.RunID, params.JobID, nativeSessionID); err != nil {
		c.logger.Error("cannot report the native session", slog.String("error", err.Error()))
	}

	var diagnostics strings.Builder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.drainStderr(stderr, &diagnostics)
	}()

	outcome := c.consume(ctx, stdout, params, sink)
	if outcome.stoppedBy != "" {
		// Stopped mid-stream: nothing reads the output any more, and a process
		// left waiting on its next message would never exit.
		stop()
	}
	wg.Wait()
	waitErr := cmd.Wait()

	// Reported before the terminal event, so a client that reacts to the end of
	// a Job already has the change summary in hand.
	if summary := workspace.Since(ctx, params.WorkingDirectory, before); !summary.Empty() {
		if err := sink.WorkspaceChanged(ctx, params.RunID, params.JobID, summary); err != nil {
			c.logger.Error("cannot report the workspace changes", slog.String("error", err.Error()))
		}
	}

	c.mu.Lock()
	cancelled := live.cancelled
	c.mu.Unlock()

	// Cancellation wins over whatever the process said on its way out: the user
	// asked for it to stop, and it did.
	if cancelled {
		c.logger.Info("claude code cancelled", slog.String("jobId", params.JobID))
		return sink.JobCancelled(ctx, params.RunID, params.JobID)
	}

	// A policy stop is not a user cancellation and not a provider failure: the
	// Run was stopped because it reached a limit someone set, and the agent and
	// the timeline should both say so.
	if outcome.stoppedBy != "" {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "POLICY_LIMIT", outcome.stoppedBy, outcome.usage)
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "POLICY_LIMIT",
			fmt.Sprintf("stopped by the execution policy after %d seconds", params.Policy.MaxDurationSeconds), outcome.usage)
	}

	switch {
	case outcome.completed && !outcome.failed:
		return sink.JobCompleted(ctx, params.RunID, params.JobID, outcome.summary, outcome.usage)
	case outcome.completed:
		return sink.JobFailed(ctx, params.RunID, params.JobID, "PROVIDER_ERROR", outcome.summary, outcome.usage)
	default:
		// The process ended without a result line: report what it printed,
		// rather than pretending the Job finished.
		message := strings.TrimSpace(diagnostics.String())
		if message == "" && waitErr != nil {
			message = waitErr.Error()
		}
		if message == "" {
			message = "claude code exited without producing a result"
		}
		if resuming {
			message = "could not resume the provider session: " + message
		}
		return sink.JobFailed(ctx, params.RunID, params.JobID, "PROVIDER_ERROR", message, outcome.usage)
	}
}

// writeMCPConfig puts the --mcp-config value in a file only this account can
// read, and returns its path and what removes it once the Job is over.
//
// A file rather than the argument itself, because the configuration carries
// the Job's endpoint token and a command line is readable by every account on
// the machine, in /proc and in ps. The token is what keeps another local
// process away from this Job's prompts and project knowledge.
//
// In the Run's scratch directory when there is one, which is private already;
// otherwise in a private directory of its own.
func writeMCPConfig(scratch, config string) (string, func(), error) {
	dir, cleanup := scratch, func() {}
	if dir == "" {
		temp, err := os.MkdirTemp("", "threavia-mcp-")
		if err != nil {
			return "", nil, fmt.Errorf("make a private directory for the tool configuration: %w", err)
		}
		dir, cleanup = temp, func() { _ = os.RemoveAll(temp) }
	}
	// CreateTemp makes the file 0600 whatever the umask, and a name of its own
	// keeps two Jobs of one Run apart.
	file, err := os.CreateTemp(dir, ".mcp-*.json")
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write the tool configuration: %w", err)
	}
	path := file.Name()
	_, err = file.WriteString(config)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		cleanup()
		return "", nil, fmt.Errorf("write the tool configuration: %w", err)
	}
	return path, func() { _ = os.Remove(path); cleanup() }, nil
}

// command builds the provider invocation.
func (c *Claude) command(ctx context.Context, params StartParams, scratch, nativeSessionID string, resuming bool, mcpConfigPath string) *exec.Cmd {
	native := params.Policy.Native()
	// A tool Core says needs validation is asked about whatever the mode. The
	// permission tool asks it, but only if the provider calls it: in SUPERVISED
	// its own reviewer settles a call first, and an allow rule would settle it
	// in any mode. An ask rule is answered before either.
	for _, tool := range params.CoreTools {
		if tool.RequiresValidation {
			native.Ask = append(native.Ask, "mcp__"+mcp.ServerName+"__"+tool.Name)
		}
	}
	// A FILE_READ refusal in the policy still wins: deny is answered first.
	native.AdditionalDirectories = c.readable
	if scratch != "" {
		native.AdditionalDirectories = append(native.AdditionalDirectories, scratch)
	}

	args := []string{
		"--print",
		"--output-format", "stream-json",
		// Messages arrive on stdin as stream-json, so one can reach a turn
		// under way; each is echoed back once read, which is how the runner
		// knows when the last one has been answered.
		"--input-format", "stream-json",
		"--replay-user-messages",
		"--verbose",
		// Permission prompts travel to Threavia instead of blocking a terminal.
		"--permission-prompt-tool", mcp.PermissionTool,
		"--mcp-config", mcpConfigPath,
		"--strict-mcp-config",
		"--append-system-prompt", systemPrompt(params, c.available, scratch),
		// The policy, in the provider's own vocabulary. What it can settle from
		// these it settles itself — its read-only set is better at reading a
		// shell command than anything maintained here — and the permission tool
		// above hears only what is left.
		"--permission-mode", native.Mode,
	}

	// Which of this machine's settings the provider may read. None, as a rule:
	// a Session runs under the policy Core states. SUPERVISED is the exception,
	// and the Native translation is where that is argued.
	args = append(args, "--setting-sources", native.SettingSources)
	if settings, err := native.Settings(); err != nil {
		// Running without them costs prompts, not safety: every refusal in the
		// policy is enforced a second time by the permission gate, which is
		// where the ones that matter are read properly anyway.
		c.logger.Error("cannot render the provider permissions, falling back to the gate alone",
			slog.String("jobId", params.JobID), slog.String("error", err.Error()))
	} else {
		args = append(args, "--settings", settings)
	}
	if params.SkillDirectory != "" {
		// A session-scoped plugin is the one mechanism that adds Skills for a
		// single run without writing into the user repository or into their own
		// Claude Code configuration.
		args = append(args, "--plugin-dir", params.SkillDirectory)
	}
	if resuming {
		args = append(args, "--resume", nativeSessionID)
	} else {
		args = append(args, "--session-id", nativeSessionID)
	}

	cmd := exec.CommandContext(ctx, c.binary, args...)
	cmd.Dir = params.WorkingDirectory
	// Its own process group, so cancelling stops the children the provider
	// spawned too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return stopGroup(cmd) }
	cmd.WaitDelay = stopGrace
	return cmd
}

// stopGroup asks the whole process group to stop.
func stopGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// systemPrompt tells the agent about the Threavia-specific tool and about the
// project it is working on. Both providers receive the same text.
func systemPrompt(params StartParams, available []string, scratch string) string {
	return runner.SystemPrompt(params, available, scratch)
}

// drainStderr keeps the pipe moving and remembers the tail for diagnostics.
func (c *Claude) drainStderr(stderr io.Reader, into *strings.Builder) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	for scanner.Scan() {
		line := scanner.Text()
		c.logger.Debug("claude code stderr", slog.String("line", line))
		if into.Len() < 4096 {
			into.WriteString(line)
			into.WriteString("\n")
		}
	}
}

// withSupervision puts what the reviewer needs in front of the message.
//
// It goes in the message rather than in the system prompt because that is the
// only place the reviewer reads: it sees the user's messages and the
// repository's own instruction file, never the system prompt a backend
// appends. So the statement travels as something the user said, which is what
// it is.
//
// In front of every message, not once at the start. The reviewer re-reads the
// conversation on each check, and a long session loses its oldest messages to
// compaction: a boundary stated once would quietly stop applying, at no visible
// moment. Repeating it costs a few lines and keeps it true.
//
// Only in SUPERVISED. The other modes have no reviewer to address, and putting
// it there would be saying something to nobody while spending the agent's
// context on it.
func withSupervision(params StartParams) string {
	return supervised(params.Policy, params.Prompt)
}

// supervised is withSupervision for any message of the Job, including one
// sent while it runs: that one is read by the reviewer like the first.
func supervised(p policy.Policy, text string) string {
	statement := strings.TrimSpace(p.Supervision)
	if statement == "" || p.Mode != backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED {
		return text
	}
	return "Standing instructions for this project, which apply to everything below:\n" +
		statement + "\n\n" + text
}
