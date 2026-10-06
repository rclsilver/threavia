package domain

import (
	"errors"
	"testing"
)

// TestBackendOfflineIsNotSelfReportable pins that OFFLINE is inferred by Core
// from a missing heartbeat and can never be claimed by a backend.
func TestBackendOfflineIsNotSelfReportable(t *testing.T) {
	t.Parallel()

	selfReportable := map[BackendOperationalStatus]bool{
		BackendStarting: true,
		BackendReady:    true,
		BackendDegraded: true,
		BackendOffline:  false,
	}
	for status, want := range selfReportable {
		if got := status.SelfReportable(); got != want {
			t.Errorf("BackendOperationalStatus(%s).SelfReportable() = %v, want %v", status, got, want)
		}
	}
}

func TestBackendOperationalTransitions(t *testing.T) {
	t.Parallel()

	all := []BackendOperationalStatus{BackendStarting, BackendReady, BackendDegraded, BackendOffline}
	allowed := map[BackendOperationalStatus][]BackendOperationalStatus{
		BackendStarting: {BackendReady, BackendDegraded, BackendOffline},
		BackendReady:    {BackendDegraded, BackendOffline},
		BackendDegraded: {BackendReady, BackendOffline},
		BackendOffline:  {BackendStarting, BackendReady, BackendDegraded},
	}

	for _, from := range all {
		expected := make(map[BackendOperationalStatus]bool, len(allowed[from]))
		for _, to := range allowed[from] {
			expected[to] = true
		}
		for _, to := range all {
			if got := from.CanTransition(to); got != expected[to] {
				t.Errorf("BackendOperationalStatus(%s).CanTransition(%s) = %v, want %v", from, to, got, expected[to])
			}
		}
	}

	if BackendOperationalStatus("BUSY").Valid() {
		t.Error("BUSY is not a backend status, capacity is modelled separately")
	}
}

// TestBackendRevokedIsTerminal pins that a revoked backend never comes back: it
// must register again as a new BackendInstance.
func TestBackendRevokedIsTerminal(t *testing.T) {
	t.Parallel()

	for _, to := range []BackendOwnershipStatus{BackendUnclaimed, BackendClaimed, BackendRevoked} {
		if BackendRevoked.CanTransition(to) {
			t.Errorf("a revoked backend must not transition to %s", to)
		}
	}
	if _, err := BackendRevoked.Transition(BackendClaimed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reclaiming a revoked backend: got %v, want ErrInvalidTransition", err)
	}
}

func TestBackendOwnershipTransitions(t *testing.T) {
	t.Parallel()

	if !BackendUnclaimed.CanTransition(BackendClaimed) {
		t.Error("an unclaimed backend must be claimable")
	}
	if !BackendUnclaimed.CanTransition(BackendRevoked) {
		t.Error("an unclaimed backend must be revocable")
	}
	if !BackendClaimed.CanTransition(BackendRevoked) {
		t.Error("a claimed backend must be revocable")
	}
	if BackendClaimed.CanTransition(BackendUnclaimed) {
		t.Error("claiming a backend is permanent, it must not become unclaimed again")
	}
}

func TestCapability(t *testing.T) {
	t.Parallel()

	for _, capability := range []Capability{CapabilityCode, CapabilityInteraction, CapabilityReview} {
		if !capability.Valid() {
			t.Errorf("Capability(%s) must be valid", capability)
		}
	}
	if Capability("DEPLOY").Valid() {
		t.Error("Capability(DEPLOY) must not be valid")
	}

	instance := BackendInstance{Capabilities: []Capability{CapabilityCode}}
	if !instance.HasCapability(CapabilityCode) {
		t.Error("the instance advertises CODE")
	}
	if instance.HasCapability(CapabilityReview) {
		t.Error("the instance does not advertise REVIEW")
	}
}

// TestCapacityDraining pins that maxConcurrentRuns = 0 drains a backend.
func TestCapacityDraining(t *testing.T) {
	t.Parallel()

	cases := []struct {
		capacity    Capacity
		draining    bool
		hasFreeSlot bool
	}{
		{Capacity{MaxConcurrentRuns: 0, ActiveRuns: 0}, true, false},
		{Capacity{MaxConcurrentRuns: 2, ActiveRuns: 1}, false, true},
		{Capacity{MaxConcurrentRuns: 2, ActiveRuns: 2}, false, false},
		{Capacity{MaxConcurrentRuns: 0, ActiveRuns: 1}, true, false},
	}

	for _, tc := range cases {
		if got := tc.capacity.Draining(); got != tc.draining {
			t.Errorf("%+v.Draining() = %v, want %v", tc.capacity, got, tc.draining)
		}
		if got := tc.capacity.HasFreeSlot(); got != tc.hasFreeSlot {
			t.Errorf("%+v.HasFreeSlot() = %v, want %v", tc.capacity, got, tc.hasFreeSlot)
		}
	}
}

func TestProviderAuthState(t *testing.T) {
	t.Parallel()

	if !ProviderAuthenticated.Valid() || !ProviderAuthenticationRequired.Valid() {
		t.Error("both provider authentication states must be valid")
	}
	if ProviderAuthState("EXPIRED").Valid() {
		t.Error("core only ever sees the two coarse provider authentication states")
	}
}

func TestIdentifiersAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 100)
	for range 100 {
		id := NewUUID()
		if id == "" {
			t.Fatal("NewUUID returned an empty identifier")
		}
		if seen[id] {
			t.Fatalf("NewUUID returned a duplicate identifier %q", id)
		}
		seen[id] = true
	}
}
