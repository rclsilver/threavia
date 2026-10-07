package domain

import "fmt"

// ExecutionMode is how much the agent may do without asking (spec section 17).
type ExecutionMode string

const (
	// ExecutionInteractive asks the user before every action that is not purely
	// read-only.
	ExecutionInteractive ExecutionMode = "INTERACTIVE"
	// ExecutionGuarded asks only for actions that change something, and lets
	// reading proceed.
	ExecutionGuarded ExecutionMode = "GUARDED"
	// ExecutionAutonomous never asks. It is not permission to do anything: the
	// limits below still apply, and are what make the mode safe to offer.
	ExecutionAutonomous ExecutionMode = "AUTONOMOUS"
)

func (m ExecutionMode) String() string { return string(m) }

// Valid reports whether m is a known ExecutionMode.
func (m ExecutionMode) Valid() bool {
	switch m {
	case ExecutionInteractive, ExecutionGuarded, ExecutionAutonomous:
		return true
	default:
		return false
	}
}

// ExecutionPolicy bounds what a Run may do.
//
// Section 17 is explicit that these are enforced rather than suggested: a policy
// forbidding a push rejects the push, instead of asking the model not to. The
// permission gate in the backend is where that happens, which is why the policy
// travels with every Job.
type ExecutionPolicy struct {
	Mode ExecutionMode `json:"mode"`

	AllowFilesystemWrite bool `json:"allowFilesystemWrite"`
	AllowGitCommit       bool `json:"allowGitCommit"`
	AllowGitPush         bool `json:"allowGitPush"`
	AllowNetwork         bool `json:"allowNetwork"`

	// MaxDurationSeconds stops a Run that has been going too long. Zero means no
	// limit.
	MaxDurationSeconds int `json:"maxDurationSeconds,omitempty"`
	// MaxActions stops a Run that has taken too many steps. Zero means no limit.
	MaxActions int `json:"maxActions,omitempty"`
}

// DefaultExecutionPolicy is what a Session gets when nothing is configured: ask
// before acting, and refuse to push.
//
// The default is deliberately the most restrained of the three modes. A policy
// that has to be loosened on purpose is a policy someone thought about.
func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{
		Mode:                 ExecutionInteractive,
		AllowFilesystemWrite: true,
		AllowGitCommit:       true,
		AllowGitPush:         false,
		AllowNetwork:         true,
	}
}

// Validate checks a policy.
func (p ExecutionPolicy) Validate() error {
	if !p.Mode.Valid() {
		return fmt.Errorf("unknown execution mode %q", p.Mode)
	}
	if p.MaxDurationSeconds < 0 || p.MaxActions < 0 {
		return fmt.Errorf("execution limits cannot be negative")
	}
	// An autonomous Run with no bound on duration or actions is a Run nobody can
	// stop, which is the one combination section 17 exists to prevent.
	if p.Mode == ExecutionAutonomous && p.MaxDurationSeconds == 0 && p.MaxActions == 0 {
		return fmt.Errorf("autonomous execution requires a duration or action limit")
	}
	return nil
}

// Effective returns the policy that applies, letting a Job override its
// Session.
func Effective(session, job *ExecutionPolicy) ExecutionPolicy {
	if job != nil {
		return *job
	}
	if session != nil {
		return *session
	}
	return DefaultExecutionPolicy()
}
