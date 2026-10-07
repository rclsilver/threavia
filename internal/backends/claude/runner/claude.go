package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
	"github.com/rclsilver/threavia/internal/backends/claude/workspace"
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

	cmd := c.command(runCtx, params, nativeSessionID, resuming, mcpConfig)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "INTERNAL", err.Error(), nil)
	}
	cmd.Stdin = strings.NewReader(params.Prompt)

	c.logger.Info("starting claude code",
		slog.String("jobId", params.JobID),
		slog.String("nativeSessionId", nativeSessionID),
		slog.Bool("resume", resuming),
		slog.String("cwd", params.WorkingDirectory))

	if err := cmd.Start(); err != nil {
		return sink.JobFailed(ctx, params.RunID, params.JobID, "SPAWN_FAILED", err.Error(), nil)
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
// project it is working on.
func systemPrompt(params StartParams) string {
	var b strings.Builder
	b.WriteString("You are running inside Threavia, a control plane for coding agents. ")
	b.WriteString("There is no interactive terminal: the user may be on another device entirely, ")
	b.WriteString("and may take a long time to answer.\n\n")
	b.WriteString("Whenever you need information, a choice or a decision from the user, ")
	b.WriteString("call the mcp__" + mcp.ServerName + "__" + mcp.ToolAskUser + " tool and wait for the answer. ")
	b.WriteString("Never guess, and never stop and ask in plain text: a plain-text question reaches nobody.\n")

	if len(params.CoreTools) > 0 {
		// The tool descriptions say when to use each one; this says why they
		// exist at all, which is the part an agent cannot infer from a schema.
		b.WriteString("\nThis project has a memory that outlives this conversation: ")
		b.WriteString("decisions, tasks and the history of what was already done, possibly ")
		b.WriteString("in another session or on another machine. The mcp__" + mcp.ServerName)
		b.WriteString("__ tools read and write it. Search it before assuming work is new, ")
		b.WriteString("and record what a later session would otherwise have to rediscover.\n")
	}

	if params.ProjectName != "" {
		b.WriteString("\nProject: " + params.ProjectName)
		if params.ProjectDescription != "" {
			b.WriteString(" — " + params.ProjectDescription)
		}
		b.WriteString("\n")
	}
	// Instructions reach the provider through its system prompt rather than
	// through a file: Threavia never writes CLAUDE.md or AGENTS.md into someone
	// else's repository, and a Run must not leave provider configuration behind
	// (spec section 18).
	if instructions := effectiveInstructions(params); instructions != "" {
		b.WriteString("\nProject instructions, which take precedence over your defaults:\n")
		b.WriteString(instructions)
		b.WriteString("\n")
	}

	if params.SkillDirectory != "" {
		b.WriteString("\nThis project has Skills available to you. Use them when they apply ")
		b.WriteString("rather than improvising the same work from scratch.\n")
	}

	return b.String()
}

// effectiveInstructions is the provider-independent project rules followed by
// the private rules of this machine, which is the split of specification
// section 18. The local ones come last so a machine can qualify a project rule.
func effectiveInstructions(params StartParams) string {
	parts := make([]string, 0, 2)
	if project := strings.TrimSpace(params.ProjectInstructions); project != "" {
		parts = append(parts, project)
	}
	if local := strings.TrimSpace(params.LocalInstructions); local != "" {
		parts = append(parts, local)
	}
	return strings.Join(parts, "\n\n")
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
