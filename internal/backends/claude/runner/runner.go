// Package runner drives the local Claude Code provider process.
//
// Everything provider-specific lives here: credentials, the native session, the
// filesystem and the local tools all belong to the BackendInstance
// (THREAVIA_SPEC_V1.md section 7). Core never sees any of it.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ErrNotImplemented is returned by the Claude runner until the provider
// integration lands (specification section 31, step 6).
var ErrNotImplemented = errors.New("claude runner not implemented yet")

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
}

// Session is the provider native session backing a Run.
type Session struct {
	NativeSessionID string
}

// Runner is the provider integration contract the adapter depends on.
type Runner interface {
	// Start creates a new provider native session and begins the Job.
	Start(ctx context.Context, params StartParams) (*Session, error)
	// Resume continues an existing provider native session.
	Resume(ctx context.Context, params StartParams) (*Session, error)
	// Cancel stops a running Job. The Job only reaches CANCELLED once this has
	// actually stopped the work.
	Cancel(ctx context.Context, jobID string) error
}

// Claude runs the Claude Code CLI.
type Claude struct {
	binary string
}

// New returns a Claude runner using the given executable, "claude" by default.
func New(binary string) *Claude {
	if binary == "" {
		binary = "claude"
	}
	return &Claude{binary: binary}
}

// Binary returns the configured executable.
func (c *Claude) Binary() string { return c.binary }

// Available reports whether the Claude Code CLI can be found. A missing CLI is a
// DEGRADED condition, not a fatal startup error: the backend stays connected and
// reports why it cannot work.
func (c *Claude) Available() error {
	path, err := exec.LookPath(c.binary)
	if err != nil {
		return fmt.Errorf("claude executable %q not found: %w", c.binary, err)
	}
	if path == "" {
		return fmt.Errorf("claude executable %q not found", c.binary)
	}
	return nil
}

// Start implements Runner.
func (c *Claude) Start(context.Context, StartParams) (*Session, error) {
	return nil, ErrNotImplemented
}

// Resume implements Runner.
func (c *Claude) Resume(context.Context, StartParams) (*Session, error) {
	return nil, ErrNotImplemented
}

// Cancel implements Runner.
func (c *Claude) Cancel(context.Context, string) error {
	return ErrNotImplemented
}
