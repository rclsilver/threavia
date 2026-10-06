package runner

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
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
	logger *slog.Logger

	mu      sync.Mutex
	running map[string]*execution
}

// execution is one live provider process.
type execution struct {
	stop      context.CancelFunc
	cancelled bool
}

// NewClaude builds the runner. The MCP server is where permission prompts and
// agent questions are turned into Threavia requests.
func NewClaude(binary string, tools *mcp.Server, logger *slog.Logger) *Claude {
	if binary == "" {
		binary = "claude"
	}
	return &Claude{
		binary:  binary,
		tools:   tools,
		logger:  logger,
		running: make(map[string]*execution),
	}
}

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
		return sink.JobFailed(ctx, params.RunID, params.JobID, "PROVIDER_UNAVAILABLE", err.Error())
	}

	// A per-Job endpoint token: the only process that can reach this Job's
	// prompts is the one started for it.
	token := uuid.NewString()
	endpoint := c.tools.Register(params.JobID, token)
	defer c.tools.Unregister(token)

	mcpConfig, err := mcp.Config(endpoint)
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error())
	}

	// The backend mints the native session id rather than discovering it, so a
	// Run is resumable from its very first message.
	nativeSessionID := params.NativeSessionID
	resuming := nativeSessionID != ""
	if !resuming {
		nativeSessionID = uuid.NewString()
	}

	runCtx, stop := context.WithCancel(ctx)
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

	cmd := c.command(runCtx, params, nativeSessionID, resuming, mcpConfig)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error())
	}
	cmd.Stdin = strings.NewReader(params.Prompt)

	c.logger.Info("starting claude code",
		slog.String("jobId", params.JobID),
		slog.String("nativeSessionId", nativeSessionID),
		slog.Bool("resume", resuming),
		slog.String("cwd", params.WorkingDirectory))

	if err := cmd.Start(); err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "SPAWN_FAILED", err.Error())
	}

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
	wg.Wait()
	waitErr := cmd.Wait()

	c.mu.Lock()
	cancelled := live.cancelled
	c.mu.Unlock()

	// Cancellation wins over whatever the process said on its way out: the user
	// asked for it to stop, and it did.
	if cancelled {
		c.logger.Info("claude code cancelled", slog.String("jobId", params.JobID))
		return sink.JobCancelled(ctx, params.RunID, params.JobID)
	}

	switch {
	case outcome.completed && !outcome.failed:
		return sink.JobCompleted(ctx, params.RunID, params.JobID, outcome.summary)
	case outcome.completed:
		return sink.JobFailed(ctx, params.RunID, params.JobID, "PROVIDER_ERROR", outcome.summary)
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
		return sink.JobFailed(ctx, params.RunID, params.JobID, "PROVIDER_ERROR", message)
	}
}

// command builds the provider invocation.
func (c *Claude) command(ctx context.Context, params StartParams, nativeSessionID string, resuming bool, mcpConfig string) *exec.Cmd {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		// Permission prompts travel to Threavia instead of blocking a terminal.
		"--permission-prompt-tool", mcp.PermissionTool,
		"--mcp-config", mcpConfig,
		"--strict-mcp-config",
		"--append-system-prompt", systemPrompt(params),
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
// project it is working on.
func systemPrompt(params StartParams) string {
	var b strings.Builder
	b.WriteString("You are running inside Threavia, a control plane for coding agents. ")
	b.WriteString("There is no interactive terminal: the user may be on another device entirely, ")
	b.WriteString("and may take a long time to answer.\n\n")
	b.WriteString("Whenever you need information, a choice or a decision from the user, ")
	b.WriteString("call the mcp__" + mcp.ServerName + "__" + mcp.ToolAskUser + " tool and wait for the answer. ")
	b.WriteString("Never guess, and never stop and ask in plain text: a plain-text question reaches nobody.\n")

	if params.ProjectName != "" {
		b.WriteString("\nProject: " + params.ProjectName)
		if params.ProjectDescription != "" {
			b.WriteString(" — " + params.ProjectDescription)
		}
		b.WriteString("\n")
	}
	return b.String()
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
