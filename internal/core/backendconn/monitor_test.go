package backendconn

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func newMonitoredServer(offlineAfter time.Duration) *Server {
	return NewServer(NewRegistry(), NewStaticTokenResolver(nil), NopSink{},
		Options{HeartbeatInterval: time.Millisecond, OfflineAfter: offlineAfter},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestSweepClosesASilentBackend pins the half of OFFLINE inference a dropped
// connection cannot cover: a backend whose process wedged keeps its stream open
// and simply stops speaking.
func TestSweepClosesASilentBackend(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	server := newMonitoredServer(time.Minute)
	server.registry.nowFn = func() time.Time { return now }

	silent, _ := server.registry.Register(domain.BackendInstanceID("wedged"))
	healthy, _ := server.registry.Register(domain.BackendInstanceID("healthy"))

	silent.NoteHeartbeat(now.Add(-2 * time.Minute))
	healthy.NoteHeartbeat(now)

	server.sweep()

	select {
	case <-silent.Done():
	default:
		t.Fatal("a backend that stopped heartbeating must have its connection closed")
	}
	if silent.Reason() != ReasonStale {
		t.Errorf("close reason = %q, want %q", silent.Reason(), ReasonStale)
	}

	select {
	case <-healthy.Done():
		t.Fatal("a backend that is still heartbeating must be left alone")
	default:
	}
}

// TestSweepGivesAFreshConnectionTime pins that a backend which has only just
// connected is not killed before it has had a chance to heartbeat.
func TestSweepGivesAFreshConnectionTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	server := newMonitoredServer(time.Minute)
	server.registry.nowFn = func() time.Time { return now }

	fresh, _ := server.registry.Register(domain.BackendInstanceID("fresh"))
	server.sweep()

	select {
	case <-fresh.Done():
		t.Fatal("a connection that just opened must not be closed before the deadline")
	default:
	}

	// Once the deadline passes with nothing said, it goes.
	server.registry.nowFn = func() time.Time { return now.Add(2 * time.Minute) }
	server.sweep()
	select {
	case <-fresh.Done():
	default:
		t.Fatal("a connection silent past the deadline must be closed")
	}
}

// TestMonitorStopsWithItsContext keeps the watcher from outliving Core.
func TestMonitorStopsWithItsContext(t *testing.T) {
	t.Parallel()

	server := newMonitoredServer(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { defer close(done); server.Monitor(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Monitor must return when its context is cancelled")
	}
}

// TestMonitorWithoutADeadlineDoesNothing pins that a zero deadline disables the
// check rather than closing everything immediately.
func TestMonitorWithoutADeadlineDoesNothing(t *testing.T) {
	t.Parallel()

	server := newMonitoredServer(0)
	conn, _ := server.registry.Register(domain.BackendInstanceID("backend"))

	done := make(chan struct{})
	go func() { defer close(done); server.Monitor(context.Background()) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Monitor must return immediately when no deadline is configured")
	}
	select {
	case <-conn.Done():
		t.Fatal("a disabled check must not close connections")
	default:
	}
}

// TestSilentBackendIsToldToReconnect pins what the backend actually sees, end to
// end: its stream is closed with a status that tells it to come back and
// reconcile rather than to give up.
func TestSilentBackendIsToldToReconnect(t *testing.T) {
	t.Parallel()

	server, conn := newTestCore(t)
	// Deliberately short: this client never heartbeats.
	server.opts.OfflineAfter = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(authorised(context.Background(), testToken), 5*time.Second)
	defer cancel()

	stream, err := backendv1.NewBackendControlClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("opening the control stream: %v", err)
	}
	if err := stream.Send(validHello()); err != nil {
		t.Fatalf("sending hello: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("awaiting welcome: %v", err)
	}

	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	go server.Monitor(monitorCtx)

	if _, err := stream.Recv(); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
	if !waitFor(t, func() bool { return server.Registry().Len() == 0 }) {
		t.Fatal("the registry must drop a connection it closed")
	}
}
