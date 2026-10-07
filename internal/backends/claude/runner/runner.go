// Package runner drives the local Claude Code provider process.
//
// Everything provider-specific lives here: the credentials, the native session,
// the filesystem and the local tools all belong to the BackendInstance
// (THREAVIA_SPEC_V1.md section 7). Core never sees any of it, and nothing in
// this package knows about Core.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
	"github.com/rclsilver/threavia/internal/backends/claude/policy"
)

// Errors returned by the runner.
var (
	// ErrUnknownJob is returned when cancelling a Job that is not running here.
	ErrUnknownJob = errors.New("job is not running on this backend")
	// ErrProviderUnavailable means the Claude Code CLI cannot be found.
	ErrProviderUnavailable = errors.New("claude code is not available")
)

// StartParams describes the work to start or resume.
type StartParams struct {
	RunID string
	JobID string
	// NativeSessionID is empty when a fresh provider session must be created for
	// the Run, and set when the existing one must be resumed. Its actual
	// existence is validated here, at resume time, never by Core.
	NativeSessionID string
	// WorkingDirectory is the resolved physical cwd. It is the initial directory
	// only, never a filesystem boundary: the agent may work elsewhere subject to
	// real OS permissions.
	WorkingDirectory string
	Prompt           string
	// ProjectName and ProjectDescription come from the compact ProjectContext
	// Core builds at Job start.
	ProjectName        string
	ProjectDescription string
	// CoreTools are the provider-independent tools Core declared for this Job.
	// The runner forwards them to the local tool endpoint and never interprets
	// them: a backend hardcodes no tool name.
	CoreTools []mcp.CoreTool
	// ProjectInstructions are the provider-independent project rules Core sent,
	// and LocalInstructions the private ones of this machine (spec section 18).
	// The adapter maps both onto the provider mechanism; Core models no provider
	// file.
	ProjectInstructions string
	LocalInstructions   string
	// SkillDirectory is the assembled per-Job plugin directory exposing the
	// effective Skills, or empty when the Run has none.
	SkillDirectory string
	// Policy bounds the Run. The permission gate enforces what the agent may do;
	// the runner enforces how long and how much, which no gate can see.
	Policy policy.Policy
}

// Runner is the provider integration contract the adapter depends on.
type Runner interface {
	// Run executes a Job to completion, emitting everything it observes to the
	// sink. It returns when the provider process has exited.
	Run(ctx context.Context, params StartParams, sink Sink) error
	// Cancel stops a running Job. The Job only reaches CANCELLED once this has
	// actually stopped the work.
	Cancel(jobID string) error
	// Available reports whether the provider can run at all.
	Available() error
}

// lookPath reports whether an executable can be found.
func lookPath(binary string) error {
	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("%w: %q not found: %v", ErrProviderUnavailable, binary, err)
	}
	if path == "" {
		return fmt.Errorf("%w: %q not found", ErrProviderUnavailable, binary)
	}
	return nil
}
