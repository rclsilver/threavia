package backendconn

import (
	"context"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// TestRegisterSupersedesThePreviousConnection pins the lease rule of
// specification section 9: only one connection is active per BackendInstance and
// the newest one takes over.
func TestRegisterSupersedesThePreviousConnection(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	instanceID := domain.BackendInstanceID("backend-1")

	first, superseded := registry.Register(instanceID)
	if superseded != nil {
		t.Fatal("the first connection supersedes nothing")
	}

	second, superseded := registry.Register(instanceID)
	if superseded != first {
		t.Fatal("the second connection must supersede the first")
	}
	if first.ID() == second.ID() {
		t.Fatal("each connection must receive its own lease id")
	}

	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("the superseded connection must be notified")
	}

	// A superseded connection receives no new work.
	if first.Send(&backendv1.CoreToBackend{}) {
		t.Error("a superseded connection must not accept new commands")
	}
	if !second.Send(&backendv1.CoreToBackend{}) {
		t.Error("the active connection must accept commands")
	}

	active, ok := registry.Lookup(instanceID)
	if !ok || active != second {
		t.Fatal("the registry must hold the newest connection")
	}
}

// TestReleaseDoesNotEvictTheSuccessor pins that a late cleanup from a superseded
// connection cannot unregister the connection that replaced it.
func TestReleaseDoesNotEvictTheSuccessor(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	instanceID := domain.BackendInstanceID("backend-1")

	first, _ := registry.Register(instanceID)
	second, _ := registry.Register(instanceID)

	registry.Release(first)

	active, ok := registry.Lookup(instanceID)
	if !ok || active != second {
		t.Fatal("releasing a superseded connection must leave the newest one registered")
	}

	registry.Release(second)
	if _, ok := registry.Lookup(instanceID); ok {
		t.Fatal("releasing the active connection must unregister it")
	}
	if registry.Len() != 0 {
		t.Fatalf("registry length = %d, want 0", registry.Len())
	}
}

// TestStaleDetectsMissingHeartbeats backs Core inferring OFFLINE, which a
// backend can never report itself.
func TestStaleDetectsMissingHeartbeats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	registry := NewRegistry()
	registry.nowFn = func() time.Time { return now }

	healthy, _ := registry.Register(domain.BackendInstanceID("healthy"))
	silent, _ := registry.Register(domain.BackendInstanceID("silent"))

	healthy.NoteHeartbeat(now)
	silent.NoteHeartbeat(now.Add(-2 * time.Minute))

	stale := registry.Stale(time.Minute)
	if len(stale) != 1 || stale[0] != silent {
		t.Fatalf("Stale() returned %d connections, want only the silent one", len(stale))
	}
}

// TestConnectionWithoutHeartbeatUsesConnectionTime keeps a backend that never
// sent a heartbeat from looking healthy forever.
func TestConnectionWithoutHeartbeatUsesConnectionTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	registry := NewRegistry()
	registry.nowFn = func() time.Time { return now.Add(-2 * time.Minute) }

	conn, _ := registry.Register(domain.BackendInstanceID("quiet"))
	registry.nowFn = func() time.Time { return now }

	stale := registry.Stale(time.Minute)
	if len(stale) != 1 || stale[0] != conn {
		t.Fatal("a connection that never sent a heartbeat must become stale")
	}
}

func TestStaticTokenResolver(t *testing.T) {
	t.Parallel()

	resolver := NewStaticTokenResolver(map[string]string{
		"token-a": "backend-a",
		"token-b": "backend-b",
		"":        "ignored",
		"token-c": "",
	})
	if resolver.Len() != 2 {
		t.Fatalf("resolver length = %d, want 2", resolver.Len())
	}

	instanceID, err := resolver.Resolve(context.Background(), "token-a")
	if err != nil {
		t.Fatalf("resolving a known token: %v", err)
	}
	if instanceID != "backend-a" {
		t.Errorf("instance id = %q, want %q", instanceID, "backend-a")
	}

	if _, err := resolver.Resolve(context.Background(), "token-z"); err != ErrUnknownBackendToken {
		t.Fatalf("resolving an unknown token: got %v, want ErrUnknownBackendToken", err)
	}
}

// TestEmptyResolverRejectsEveryBackend pins the safe default: with no credential
// configured, no backend can connect.
func TestEmptyResolverRejectsEveryBackend(t *testing.T) {
	t.Parallel()

	resolver := NewStaticTokenResolver(nil)
	if _, err := resolver.Resolve(context.Background(), "anything"); err != ErrUnknownBackendToken {
		t.Fatalf("got %v, want ErrUnknownBackendToken", err)
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer  abc ", "abc", true},
		{"Basic abc", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		got, ok := bearerToken(tc.header)
		if ok != tc.ok || got != tc.want {
			t.Errorf("bearerToken(%q) = (%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

// TestCloseAllDrainsTheConnections backs an immediate graceful shutdown: the
// control streams end instead of holding the server open for their lifetime.
func TestCloseAllDrainsTheConnections(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	first, _ := registry.Register(domain.BackendInstanceID("backend-1"))
	second, _ := registry.Register(domain.BackendInstanceID("backend-2"))

	registry.CloseAll(ReasonShutdown)

	for _, conn := range []*Connection{first, second} {
		select {
		case <-conn.Done():
		case <-time.After(time.Second):
			t.Fatalf("connection %s was not closed", conn.ID())
		}
		if conn.Reason() != ReasonShutdown {
			t.Errorf("close reason = %q, want %q", conn.Reason(), ReasonShutdown)
		}
	}
}

// TestCloseReasonRecordsTheFirstCause keeps a later release from rewriting why a
// connection actually went away.
func TestCloseReasonRecordsTheFirstCause(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	first, _ := registry.Register(domain.BackendInstanceID("backend-1"))
	registry.Register(domain.BackendInstanceID("backend-1"))

	registry.Release(first)

	if first.Reason() != ReasonSuperseded {
		t.Errorf("close reason = %q, want %q", first.Reason(), ReasonSuperseded)
	}
}
