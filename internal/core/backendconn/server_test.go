package backendconn

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

const testToken = "t0ken"

// newTestCore starts the control service on an in-memory listener.
func newTestCore(t *testing.T) (*Server, *grpc.ClientConn) {
	t.Helper()

	registry := NewRegistry()
	resolver := NewStaticTokenResolver(map[string]string{testToken: "backend-1"})
	server := NewServer(registry, resolver, NopSink{}, Options{HeartbeatInterval: time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	backendv1.RegisterBackendControlServer(grpcServer, server)

	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("creating the test client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return server, conn
}

func authorised(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

func validHello() *backendv1.BackendToCore {
	return &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_Hello{
			Hello: &backendv1.Hello{
				Protocol:     &backendv1.ProtocolInfo{Version: ProtocolVersion},
				Sdk:          &backendv1.SdkInfo{Name: "go", Version: "test"},
				Backend:      &backendv1.BackendInfo{Name: "claude", Version: "test"},
				InstanceName: "laptop",
				Capabilities: []backendv1.Capability{backendv1.Capability_CAPABILITY_CODE},
				Capacity:     &backendv1.Capacity{MaxConcurrentRuns: 1},
			},
		},
	}
}

// TestConnectHandshake pins the Hello/Welcome exchange and the connection lease.
func TestConnectHandshake(t *testing.T) {
	t.Parallel()

	server, conn := newTestCore(t)
	ctx, cancel := context.WithTimeout(authorised(context.Background(), testToken), 5*time.Second)
	defer cancel()

	stream, err := backendv1.NewBackendControlClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("opening the control stream: %v", err)
	}
	if err := stream.Send(validHello()); err != nil {
		t.Fatalf("sending hello: %v", err)
	}

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("awaiting welcome: %v", err)
	}
	welcome := first.GetWelcome()
	if welcome == nil {
		t.Fatal("the first core message must be a welcome")
	}
	if welcome.GetConnectionId() == "" {
		t.Error("the welcome must carry a connection lease id")
	}
	if welcome.GetBackendInstanceId() != "backend-1" {
		t.Errorf("backend instance id = %q, want %q", welcome.GetBackendInstanceId(), "backend-1")
	}
	if welcome.GetHeartbeatIntervalSeconds() != 1 {
		t.Errorf("heartbeat interval = %d, want 1", welcome.GetHeartbeatIntervalSeconds())
	}

	// A heartbeat keeps the connection alive and is recorded.
	sentAt := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	if err := stream.Send(&backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_Heartbeat{
			Heartbeat: &backendv1.Heartbeat{SentAt: timestamppb.New(sentAt)},
		},
	}); err != nil {
		t.Fatalf("sending a heartbeat: %v", err)
	}

	registered := waitFor(t, func() bool {
		registered, ok := server.Registry().Lookup("backend-1")
		return ok && registered.LastHeartbeat().Equal(sentAt)
	})
	if !registered {
		t.Fatal("the heartbeat must be recorded on the registered connection")
	}
}

// TestConnectRejectsUnknownCredentials pins that a backend without a valid
// credential never reaches the handshake.
func TestConnectRejectsUnknownCredentials(t *testing.T) {
	t.Parallel()

	_, conn := newTestCore(t)

	cases := []struct {
		name string
		ctx  context.Context
	}{
		{"no metadata", context.Background()},
		{"unknown token", authorised(context.Background(), "wrong")},
		{"malformed header", metadata.AppendToOutgoingContext(context.Background(), "authorization", "Basic abc")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(tc.ctx, 5*time.Second)
			defer cancel()

			stream, err := backendv1.NewBackendControlClient(conn).Connect(ctx)
			if err != nil {
				t.Fatalf("opening the control stream: %v", err)
			}
			_ = stream.Send(validHello())
			if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("got %v, want Unauthenticated", err)
			}
		})
	}
}

// TestConnectRequiresHelloFirst pins that the stream starts with Hello.
func TestConnectRequiresHelloFirst(t *testing.T) {
	t.Parallel()

	_, conn := newTestCore(t)
	ctx, cancel := context.WithTimeout(authorised(context.Background(), testToken), 5*time.Second)
	defer cancel()

	stream, err := backendv1.NewBackendControlClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("opening the control stream: %v", err)
	}
	if err := stream.Send(&backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_Heartbeat{Heartbeat: &backendv1.Heartbeat{}},
	}); err != nil {
		t.Fatalf("sending a heartbeat: %v", err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got %v, want InvalidArgument", err)
	}
}

// TestConnectRejectsAnotherProtocolVersion pins that the protocol version is the
// contractual compatibility boundary.
func TestConnectRejectsAnotherProtocolVersion(t *testing.T) {
	t.Parallel()

	_, conn := newTestCore(t)
	ctx, cancel := context.WithTimeout(authorised(context.Background(), testToken), 5*time.Second)
	defer cancel()

	stream, err := backendv1.NewBackendControlClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("opening the control stream: %v", err)
	}

	hello := validHello()
	hello.GetHello().Protocol = &backendv1.ProtocolInfo{Version: ProtocolVersion + 1}
	if err := stream.Send(hello); err != nil {
		t.Fatalf("sending hello: %v", err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("got %v, want FailedPrecondition", err)
	}
}

// TestCoreToolRequestIsAnsweredWithAnError keeps an agent from waiting forever
// on a Core Tool that is not implemented yet.
func TestCoreToolRequestIsAnsweredWithAnError(t *testing.T) {
	t.Parallel()

	_, conn := newTestCore(t)
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

	if err := stream.Send(&backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_CoreToolRequest{
			CoreToolRequest: &backendv1.CoreToolRequest{RequestId: "req-1", Name: "task_create"},
		},
	}); err != nil {
		t.Fatalf("sending a core tool request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("awaiting the core tool response: %v", err)
	}
	response := msg.GetCoreToolResponse()
	if response == nil {
		t.Fatalf("expected a core tool response, got %T", msg.GetMessage())
	}
	if response.GetRequestId() != "req-1" {
		t.Errorf("request id = %q, want %q", response.GetRequestId(), "req-1")
	}
	if response.GetError() == nil {
		t.Error("an unimplemented core tool must answer with an error")
	}
}

// TestSecondConnectionSupersedesTheFirst pins the connection lease end to end.
func TestSecondConnectionSupersedesTheFirst(t *testing.T) {
	t.Parallel()

	_, conn := newTestCore(t)
	client := backendv1.NewBackendControlClient(conn)

	firstCtx, cancelFirst := context.WithTimeout(authorised(context.Background(), testToken), 5*time.Second)
	defer cancelFirst()
	first, err := client.Connect(firstCtx)
	if err != nil {
		t.Fatalf("opening the first control stream: %v", err)
	}
	if err := first.Send(validHello()); err != nil {
		t.Fatalf("sending hello: %v", err)
	}
	if _, err := first.Recv(); err != nil {
		t.Fatalf("awaiting the first welcome: %v", err)
	}

	secondCtx, cancelSecond := context.WithTimeout(authorised(context.Background(), testToken), 5*time.Second)
	defer cancelSecond()
	second, err := client.Connect(secondCtx)
	if err != nil {
		t.Fatalf("opening the second control stream: %v", err)
	}
	if err := second.Send(validHello()); err != nil {
		t.Fatalf("sending hello: %v", err)
	}
	if _, err := second.Recv(); err != nil {
		t.Fatalf("awaiting the second welcome: %v", err)
	}

	if _, err := first.Recv(); status.Code(err) != codes.Aborted {
		t.Fatalf("the superseded stream must be closed with Aborted, got %v", err)
	}
}

// waitFor polls condition for up to a second.
func waitFor(t *testing.T, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return condition()
}

// TestShutdownClosesTheControlStreams pins that Core ends the long-lived control
// streams itself, so a graceful stop drains at once instead of waiting for them.
func TestShutdownClosesTheControlStreams(t *testing.T) {
	t.Parallel()

	server, conn := newTestCore(t)
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

	server.Shutdown()

	if _, err := stream.Recv(); status.Code(err) != codes.Unavailable {
		t.Fatalf("got %v, want Unavailable", err)
	}
	if server.Registry().Len() != 0 {
		t.Errorf("registry still holds %d connections after shutdown", server.Registry().Len())
	}
}
