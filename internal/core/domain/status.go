package domain

import "fmt"

// ErrInvalidTransition is returned by every CanTransition/Transition helper when
// a state change is not allowed by the specification.
var ErrInvalidTransition = fmt.Errorf("invalid state transition")

// transitionError builds a descriptive ErrInvalidTransition.
func transitionError(kind string, from, to fmt.Stringer) error {
	return fmt.Errorf("%w: %s %s -> %s", ErrInvalidTransition, kind, from, to)
}

// ProjectStatus is the lifecycle of a Project (spec sections 3.1 and 21).
type ProjectStatus string

const (
	ProjectActive   ProjectStatus = "ACTIVE"
	ProjectArchived ProjectStatus = "ARCHIVED"
)

func (s ProjectStatus) String() string { return string(s) }

// Valid reports whether s is a known ProjectStatus.
func (s ProjectStatus) Valid() bool {
	switch s {
	case ProjectActive, ProjectArchived:
		return true
	default:
		return false
	}
}

// CanTransition reports whether a Project may move from s to to.
func (s ProjectStatus) CanTransition(to ProjectStatus) bool {
	if !s.Valid() || !to.Valid() {
		return false
	}
	// Archive and restore are the only transitions; permanent deletion is a
	// separate explicit operation, not a status.
	return s != to
}

// Transition validates a Project status change.
func (s ProjectStatus) Transition(to ProjectStatus) (ProjectStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("project status", s, to)
	}
	return to, nil
}

// SessionStatus is the lifecycle of a Session (spec sections 3.2 and 21).
type SessionStatus string

const (
	SessionActive   SessionStatus = "ACTIVE"
	SessionArchived SessionStatus = "ARCHIVED"
)

func (s SessionStatus) String() string { return string(s) }

// Valid reports whether s is a known SessionStatus.
func (s SessionStatus) Valid() bool {
	switch s {
	case SessionActive, SessionArchived:
		return true
	default:
		return false
	}
}

// CanTransition reports whether a Session may move from s to to.
func (s SessionStatus) CanTransition(to SessionStatus) bool {
	if !s.Valid() || !to.Valid() {
		return false
	}
	return s != to
}

// Transition validates a Session status change.
func (s SessionStatus) Transition(to SessionStatus) (SessionStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("session status", s, to)
	}
	return to, nil
}

// ResumeStatus describes whether the provider native session bound to a Run can
// still be resumed (spec section 3.4). It is observational: the backend is the
// only component that can establish it, at actual resume time.
type ResumeStatus string

const (
	ResumeUnknown     ResumeStatus = "UNKNOWN"
	ResumeAvailable   ResumeStatus = "AVAILABLE"
	ResumeUnavailable ResumeStatus = "UNAVAILABLE"
)

func (s ResumeStatus) String() string { return string(s) }

// Valid reports whether s is a known ResumeStatus.
func (s ResumeStatus) Valid() bool {
	switch s {
	case ResumeUnknown, ResumeAvailable, ResumeUnavailable:
		return true
	default:
		return false
	}
}

// JobStatus is the lifecycle of a Job (spec section 3.5).
type JobStatus string

const (
	JobQueued            JobStatus = "QUEUED"
	JobRunning           JobStatus = "RUNNING"
	JobWaitingInput      JobStatus = "WAITING_INPUT"
	JobWaitingValidation JobStatus = "WAITING_VALIDATION"
	JobWaitingBackend    JobStatus = "WAITING_BACKEND"
	JobCancelling        JobStatus = "CANCELLING"
	JobCompleted         JobStatus = "COMPLETED"
	JobFailed            JobStatus = "FAILED"
	JobCancelled         JobStatus = "CANCELLED"
)

func (s JobStatus) String() string { return string(s) }

// Valid reports whether s is a known JobStatus.
func (s JobStatus) Valid() bool {
	_, ok := jobTransitions[s]
	return ok
}

// Terminal reports whether s is a final Job status.
func (s JobStatus) Terminal() bool {
	switch s {
	case JobCompleted, JobFailed, JobCancelled:
		return true
	default:
		return false
	}
}

// Active reports whether a Job in status s occupies the single active slot of
// its Run. A Run has at most one active Job and may have several queued ones
// (spec section 3.4).
func (s JobStatus) Active() bool {
	switch s {
	case JobRunning, JobWaitingInput, JobWaitingValidation, JobWaitingBackend, JobCancelling:
		return true
	default:
		return false
	}
}

// Waiting reports whether a Job is blocked on a pending attention item or on
// backend availability. Waiting states never time out (spec section 3.5).
func (s JobStatus) Waiting() bool {
	switch s {
	case JobWaitingInput, JobWaitingValidation, JobWaitingBackend:
		return true
	default:
		return false
	}
}

// jobTransitions is the authoritative Job state machine.
//
// Notable rules from the specification:
//   - cancelling a RUNNING Job goes through CANCELLING and only reaches
//     CANCELLED once the backend confirms the stop; while the backend is offline
//     the Job stays CANCELLING and is reconciled on reconnect;
//   - a QUEUED Job has nothing running on a backend, so it is cancelled
//     directly;
//   - CANCELLING may still resolve to COMPLETED or FAILED: the backend is the
//     source of truth for what actually happened locally (spec section 9);
//   - WAITING_BACKEND may resolve to any outcome reported at reconciliation.
var jobTransitions = map[JobStatus]map[JobStatus]bool{
	JobQueued: {
		JobRunning:        true,
		JobWaitingBackend: true,
		JobCancelled:      true,
		JobFailed:         true,
	},
	JobRunning: {
		JobWaitingInput:      true,
		JobWaitingValidation: true,
		JobWaitingBackend:    true,
		JobCancelling:        true,
		JobCompleted:         true,
		JobFailed:            true,
	},
	JobWaitingInput: {
		JobRunning:        true,
		JobWaitingBackend: true,
		JobCancelling:     true,
		JobFailed:         true,
	},
	JobWaitingValidation: {
		JobRunning:        true,
		JobWaitingBackend: true,
		JobCancelling:     true,
		JobFailed:         true,
	},
	JobWaitingBackend: {
		JobRunning:           true,
		JobWaitingInput:      true,
		JobWaitingValidation: true,
		JobCancelling:        true,
		JobCompleted:         true,
		JobFailed:            true,
	},
	JobCancelling: {
		JobCancelled: true,
		JobCompleted: true,
		JobFailed:    true,
	},
	JobCompleted: {},
	JobFailed:    {},
	JobCancelled: {},
}

// CanTransition reports whether a Job may move from s to to.
func (s JobStatus) CanTransition(to JobStatus) bool {
	allowed, ok := jobTransitions[s]
	if !ok {
		return false
	}
	return allowed[to]
}

// Transition validates a Job status change.
func (s JobStatus) Transition(to JobStatus) (JobStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("job status", s, to)
	}
	return to, nil
}

// AttentionStatus is the state of a persistent actionable object, that is a
// ValidationRequest or a UserInputRequest (spec section 6).
//
// Pending attention is current state, never an unread-event counter: once an
// item is resolved, every client drops its pending UI.
type AttentionStatus string

const (
	AttentionPending  AttentionStatus = "PENDING"
	AttentionResolved AttentionStatus = "RESOLVED"
)

func (s AttentionStatus) String() string { return string(s) }

// Valid reports whether s is a known AttentionStatus.
func (s AttentionStatus) Valid() bool {
	switch s {
	case AttentionPending, AttentionResolved:
		return true
	default:
		return false
	}
}

// CanTransition reports whether an attention item may move from s to to. The
// resolution is one-way and atomic: the first valid response wins.
func (s AttentionStatus) CanTransition(to AttentionStatus) bool {
	return s == AttentionPending && to == AttentionResolved
}

// Transition validates an attention item status change.
func (s AttentionStatus) Transition(to AttentionStatus) (AttentionStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("attention status", s, to)
	}
	return to, nil
}
