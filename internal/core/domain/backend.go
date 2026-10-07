package domain

import "time"

// BackendOperationalStatus is the operational state of a BackendInstance
// (spec section 7).
//
// BUSY is deliberately not a status: capacity is modelled separately by
// Capacity.
type BackendOperationalStatus string

const (
	BackendStarting BackendOperationalStatus = "STARTING"
	BackendReady    BackendOperationalStatus = "READY"
	BackendDegraded BackendOperationalStatus = "DEGRADED"
	// BackendOffline is never self-reported: Core infers it from a missing
	// heartbeat or a closed control stream.
	BackendOffline BackendOperationalStatus = "OFFLINE"
)

func (s BackendOperationalStatus) String() string { return string(s) }

// Valid reports whether s is a known BackendOperationalStatus.
func (s BackendOperationalStatus) Valid() bool {
	_, ok := backendOperationalTransitions[s]
	return ok
}

// SelfReportable reports whether a backend may claim status s for itself.
func (s BackendOperationalStatus) SelfReportable() bool {
	switch s {
	case BackendStarting, BackendReady, BackendDegraded:
		return true
	default:
		return false
	}
}

// backendOperationalTransitions allows any observation-driven change except
// re-entering STARTING from a live state: a backend that restarts first goes
// OFFLINE, because the control stream drops.
var backendOperationalTransitions = map[BackendOperationalStatus]map[BackendOperationalStatus]bool{
	BackendStarting: {
		BackendReady:    true,
		BackendDegraded: true,
		BackendOffline:  true,
	},
	BackendReady: {
		BackendDegraded: true,
		BackendOffline:  true,
	},
	BackendDegraded: {
		BackendReady:   true,
		BackendOffline: true,
	},
	BackendOffline: {
		BackendStarting: true,
		BackendReady:    true,
		BackendDegraded: true,
	},
}

// CanTransition reports whether a BackendInstance may move from s to to.
func (s BackendOperationalStatus) CanTransition(to BackendOperationalStatus) bool {
	allowed, ok := backendOperationalTransitions[s]
	if !ok {
		return false
	}
	return allowed[to]
}

// Transition validates a BackendInstance operational status change.
func (s BackendOperationalStatus) Transition(to BackendOperationalStatus) (BackendOperationalStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("backend operational status", s, to)
	}
	return to, nil
}

// BackendOwnershipStatus is the registration lifecycle of a BackendInstance
// (spec section 8).
type BackendOwnershipStatus string

const (
	// BackendUnclaimed is produced by shared-key registration, before a user
	// enters the one-time claim code.
	BackendUnclaimed BackendOwnershipStatus = "UNCLAIMED"
	// BackendClaimed means the instance is permanently associated with a user.
	BackendClaimed BackendOwnershipStatus = "CLAIMED"
	// BackendRevoked invalidates the persistent backend credentials. The record
	// is kept for historical references; a returning backend must register again
	// as a new BackendInstance, so REVOKED is terminal.
	BackendRevoked BackendOwnershipStatus = "REVOKED"
)

func (s BackendOwnershipStatus) String() string { return string(s) }

// Valid reports whether s is a known BackendOwnershipStatus.
func (s BackendOwnershipStatus) Valid() bool {
	_, ok := backendOwnershipTransitions[s]
	return ok
}

var backendOwnershipTransitions = map[BackendOwnershipStatus]map[BackendOwnershipStatus]bool{
	BackendUnclaimed: {
		BackendClaimed: true,
		BackendRevoked: true,
	},
	BackendClaimed: {
		BackendRevoked: true,
	},
	BackendRevoked: {},
}

// CanTransition reports whether a BackendInstance may move from s to to.
func (s BackendOwnershipStatus) CanTransition(to BackendOwnershipStatus) bool {
	allowed, ok := backendOwnershipTransitions[s]
	if !ok {
		return false
	}
	return allowed[to]
}

// Transition validates a BackendInstance ownership status change.
func (s BackendOwnershipStatus) Transition(to BackendOwnershipStatus) (BackendOwnershipStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("backend ownership status", s, to)
	}
	return to, nil
}

// ProviderAuthState is the coarse provider authentication state Core is allowed
// to see. Provider credentials never live in Core (spec sections 2 and 7).
type ProviderAuthState string

const (
	ProviderAuthenticated          ProviderAuthState = "AUTHENTICATED"
	ProviderAuthenticationRequired ProviderAuthState = "AUTHENTICATION_REQUIRED"
)

func (s ProviderAuthState) String() string { return string(s) }

// Valid reports whether s is a known ProviderAuthState.
func (s ProviderAuthState) Valid() bool {
	switch s {
	case ProviderAuthenticated, ProviderAuthenticationRequired:
		return true
	default:
		return false
	}
}

// Capability is an extensible BackendInstance capability (spec section 7).
type Capability string

const (
	// CapabilityCode implies the whole mandatory CODE semantic contract:
	// resumable native session where the provider supports it,
	// ValidationRequest, UserInputRequest and Core Tools integration.
	CapabilityCode Capability = "CODE"
	// CapabilityInteraction is used notably for conversational/Voice adaptation.
	CapabilityInteraction Capability = "INTERACTION"
	// CapabilityReview is declared for forward compatibility only; it is post-V1.
	CapabilityReview Capability = "REVIEW"
)

func (c Capability) String() string { return string(c) }

// Valid reports whether c is a known Capability.
func (c Capability) Valid() bool {
	switch c {
	case CapabilityCode, CapabilityInteraction, CapabilityReview:
		return true
	default:
		return false
	}
}

// Capacity is the declared concurrency of a BackendInstance. It is explicitly
// not an operational status.
type Capacity struct {
	MaxConcurrentRuns int `json:"maxConcurrentRuns"`
	ActiveRuns        int `json:"activeRuns"`
}

// Draining reports whether the backend accepts no new Run. Setting
// MaxConcurrentRuns to 0 is the documented way to drain a backend.
func (c Capacity) Draining() bool { return c.MaxConcurrentRuns <= 0 }

// HasFreeSlot reports whether the backend can accept one more Run.
func (c Capacity) HasFreeSlot() bool {
	return !c.Draining() && c.ActiveRuns < c.MaxConcurrentRuns
}

// Condition is why a BackendInstance is in the state it reports
// (spec section 7).
//
// The operational status says a backend is DEGRADED; a condition says which
// thing is wrong, in the vocabulary the backend chose. Core stores and shows it
// without interpreting it: a new provider must be able to explain itself
// without a Core release.
type Condition struct {
	Type    string    `json:"type"`
	Status  string    `json:"status"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"observedAt"`
}
