package client_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// fakeCore is a minimal Core control service: it completes the handshake,
// records everything the backend sends and forwards the commands a test pushes.
type fakeCore struct {
	backendv1.UnimplementedBackendControlServer

	received chan *backendv1.BackendToCore
	commands chan *backendv1.CoreToBackend
	metadata chan metadata.MD
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		received: make(chan *backendv1.BackendToCore, 64),
		commands: make(chan *backendv1.CoreToBackend, 8),
		metadata: make(chan metadata.MD, 4),
	}
}

func (f *fakeCore) Connect(stream backendv1.BackendControl_ConnectServer) error {
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		select {
		case f.metadata <- md:
		default:
		}
	}

	hello, err := stream.Recv()
	if err != nil {
		return err
	}
	f.received <- hello

	if err := stream.Send(&backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_Welcome{
			Welcome: &backendv1.Welcome{
				ConnectionId:             "lease-1",
				BackendInstanceId:        "backend-1",
				ServerTime:               timestamppb.Now(),
				HeartbeatIntervalSeconds: 1,
			},
		},
	}); err != nil {
		return err
	}

	readerDone := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				readerDone <- err
				return
			}
			f.received <- msg
		}
	}()

	for {
		select {
		case err := <-readerDone:
			return err
		case <-stream.Context().Done():
			return stream.Context().Err()
		case cmd := <-f.commands:
			if err := stream.Send(cmd); err != nil {
				return err
			}
		}
	}
}

// await returns the first received message matching match, or fails.
func (f *fakeCore) await(t *testing.T, what string, match func(*backendv1.BackendToCore) bool) *backendv1.BackendToCore {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-f.received:
			if match(msg) {
				return msg
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
			return nil
		}
	}
}

// startFakeCore serves the fake Core on an in-memory listener and returns the
// dial option reaching it.
func startFakeCore(t *testing.T) (*fakeCore, grpc.DialOption) {
	t.Helper()

	core := newFakeCore()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	backendv1.RegisterBackendControlServer(server, core)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return core, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	})
}

func testConfig(dialer grpc.DialOption) client.Config {
	cfg := client.DefaultConfig()
	cfg.CoreAddress = "passthrough:///bufnet"
	cfg.Token = "t0ken"
	cfg.InstanceName = "laptop"
	cfg.BackendName = "claude"
	cfg.BackendVersion = "test"
	cfg.TLS.Enabled = false
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.DialOptions = []grpc.DialOption{dialer}
	return cfg
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingHandler captures the Welcome the SDK receives.
type recordingHandler struct {
	client.BaseHandler
	connected chan *backendv1.Welcome
}

func (h *recordingHandler) OnConnected(_ context.Context, welcome *backendv1.Welcome) error {
	h.connected <- welcome
	return nil
}

func TestClientCompletesTheHandshake(t *testing.T) {
	t.Parallel()

	core, dialer := startFakeCore(t)
	handler := &recordingHandler{connected: make(chan *backendv1.Welcome, 1)}

	sdk, err := client.New(testConfig(dialer), handler, state.NewMemoryStore(), discardLogger())
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- sdk.Run(ctx) }()

	// The credential travels as a bearer token and is never part of the payload.
	select {
	case md := <-core.metadata:
		if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer t0ken" {
			t.Fatalf("authorization metadata = %v, want a bearer token", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the connection metadata")
	}

	hello := core.await(t, "hello", func(m *backendv1.BackendToCore) bool { return m.GetHello() != nil }).GetHello()
	if hello.GetProtocol().GetVersion() != client.ProtocolVersion {
		t.Errorf("protocol version = %d, want %d", hello.GetProtocol().GetVersion(), client.ProtocolVersion)
	}
	if hello.GetInstanceName() != "laptop" {
		t.Errorf("instance name = %q, want %q", hello.GetInstanceName(), "laptop")
	}
	if len(hello.GetCapabilities()) != 1 || hello.GetCapabilities()[0] != backendv1.Capability_CAPABILITY_CODE {
		t.Errorf("capabilities = %v, want CODE", hello.GetCapabilities())
	}

	select {
	case welcome := <-handler.connected:
		if welcome.GetConnectionId() != "lease-1" {
			t.Errorf("connection id = %q, want %q", welcome.GetConnectionId(), "lease-1")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the handler to be notified")
	}

	// Reconciliation state is reported before any new work is accepted.
	core.await(t, "reconcile state", func(m *backendv1.BackendToCore) bool { return m.GetReconcileState() != nil })
	// Heartbeats keep flowing.
	core.await(t, "heartbeat", func(m *backendv1.BackendToCore) bool { return m.GetHeartbeat() != nil })

	if !sdk.Connected() {
		t.Error("the client must report itself connected")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

// TestBufferedEventsAreReplayed pins that events Core never acknowledged are
// resent on the next connection (specification section 10).
func TestBufferedEventsAreReplayed(t *testing.T) {
	t.Parallel()

	core, dialer := startFakeCore(t)
	store := state.NewMemoryStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	buffered := &backendv1.JobEvent{
		BackendEventId:  "evt-1",
		BackendSequence: 1,
		RunId:           "run-1",
		JobId:           "job-1",
	}
	if err := store.AppendPending(ctx, buffered); err != nil {
		t.Fatalf("buffering an event: %v", err)
	}

	sdk, err := client.New(testConfig(dialer), &recordingHandler{connected: make(chan *backendv1.Welcome, 1)}, store, discardLogger())
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}
	go func() { _ = sdk.Run(ctx) }()

	replayed := core.await(t, "replayed event", func(m *backendv1.BackendToCore) bool {
		return m.GetJobEvent().GetBackendEventId() == "evt-1"
	})
	if replayed.GetJobEvent().GetBackendSequence() != 1 {
		t.Errorf("replayed sequence = %d, want 1", replayed.GetJobEvent().GetBackendSequence())
	}

	// Acknowledging it drops it from the durable buffer.
	core.commands <- &backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_EventAck{
			EventAck: &backendv1.EventAck{RunId: "run-1", JobId: "job-1", ThroughBackendSequence: 1},
		},
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		pending, err := store.PendingEvents(ctx)
		if err != nil {
			t.Fatalf("reading buffered events: %v", err)
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d events are still buffered after the acknowledgement", len(pending))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSendEventIsDurableBeforeItIsSent pins that an event is recorded locally
// even when Core is unreachable, so it is never lost.
func TestSendEventIsDurableBeforeItIsSent(t *testing.T) {
	t.Parallel()

	_, dialer := startFakeCore(t)
	store := state.NewMemoryStore()

	sdk, err := client.New(testConfig(dialer), client.BaseHandler{}, store, discardLogger())
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}

	ctx := context.Background()
	event, err := sdk.Events().AgentMessage(ctx, "run-1", "job-1", "hello")
	if err != nil {
		t.Fatalf("building an event: %v", err)
	}

	// Never connected: SendEvent still records the event durably.
	if err := sdk.SendEvent(ctx, event); err != nil {
		t.Fatalf("sending an event while disconnected: %v", err)
	}

	pending, err := store.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading buffered events: %v", err)
	}
	if len(pending) != 1 || pending[0].GetBackendEventId() != event.GetBackendEventId() {
		t.Fatalf("the event must stay buffered until core acknowledges it, got %d", len(pending))
	}
}

// TestUnsupportedCommandIsRejected pins that a backend never silently accepts a
// command it cannot honour.
func TestUnsupportedCommandIsRejected(t *testing.T) {
	t.Parallel()

	core, dialer := startFakeCore(t)
	sdk, err := client.New(testConfig(dialer), client.BaseHandler{}, state.NewMemoryStore(), discardLogger())
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sdk.Run(ctx) }()

	core.await(t, "hello", func(m *backendv1.BackendToCore) bool { return m.GetHello() != nil })

	core.commands <- &backendv1.CoreToBackend{
		CommandId: "cmd-1",
		Message: &backendv1.CoreToBackend_StartJob{
			StartJob: &backendv1.StartJob{RunId: "run-1", JobId: "job-1", Prompt: "analyse ce projet"},
		},
	}

	result := core.await(t, "command result", func(m *backendv1.BackendToCore) bool {
		return m.GetCommandResult().GetCommandId() == "cmd-1"
	}).GetCommandResult()

	if result.GetAccepted() {
		t.Error("an unsupported command must not be accepted")
	}
	if result.GetError().GetCode() != "UNSUPPORTED" {
		t.Errorf("error code = %q, want %q", result.GetError().GetCode(), "UNSUPPORTED")
	}
}
