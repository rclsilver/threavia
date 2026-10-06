package backendconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// Options configures the control server.
type Options struct {
	// HeartbeatInterval is advertised to backends in the Welcome message.
	HeartbeatInterval time.Duration
}

// Server implements the BackendControl service.
//
// This is the connection skeleton: it authenticates the backend, performs the
// Hello/Welcome handshake, installs the connection lease and keeps the stream
// alive. Persisting events, dispatching Jobs and reconciling state are the next
// implementation steps and are deliberately not faked here: in particular, Core
// does not acknowledge Job events it has not persisted, so backends keep them in
// their local durable buffer and replay them later (spec section 10).
type Server struct {
	backendv1.UnimplementedBackendControlServer

	registry *Registry
	resolver TokenResolver
	logger   *slog.Logger
	opts     Options
}

// NewServer builds the control server.
func NewServer(registry *Registry, resolver TokenResolver, opts Options, logger *slog.Logger) *Server {
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 15 * time.Second
	}
	return &Server{
		registry: registry,
		resolver: resolver,
		logger:   logger,
		opts:     opts,
	}
}

// Registry exposes the live connections, for the Core services dispatching
// commands to backends.
func (s *Server) Registry() *Registry { return s.registry }

type recvResult struct {
	msg *backendv1.BackendToCore
	err error
}

// Connect handles one backend control stream for its whole lifetime.
func (s *Server) Connect(stream backendv1.BackendControl_ConnectServer) error {
	ctx := stream.Context()

	instanceID, err := s.authenticate(ctx)
	if err != nil {
		return err
	}

	hello, err := s.receiveHello(stream)
	if err != nil {
		return err
	}

	conn, previous := s.registry.Register(instanceID)
	defer s.registry.Release(conn)

	logger := s.logger.With(
		slog.String("backendInstanceId", string(instanceID)),
		slog.String("connectionId", conn.ID()),
	)
	if previous != nil {
		logger.Info("superseding previous backend connection", slog.String("previousConnectionId", previous.ID()))
	}
	logger.Info("backend connected",
		slog.String("instanceName", hello.GetInstanceName()),
		slog.String("backend", hello.GetBackend().GetName()),
		slog.String("backendVersion", hello.GetBackend().GetVersion()),
		slog.String("sdk", hello.GetSdk().GetName()),
		slog.Uint64("protocolVersion", uint64(hello.GetProtocol().GetVersion())),
		slog.Any("capabilities", capabilityNames(hello.GetCapabilities())),
		slog.Int("maxConcurrentRuns", int(hello.GetCapacity().GetMaxConcurrentRuns())),
	)
	defer logger.Info("backend disconnected")

	// The Welcome must be the first message Core sends, before the writer
	// goroutine may interleave anything else.
	welcome := &backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_Welcome{
			Welcome: &backendv1.Welcome{
				ConnectionId:             conn.ID(),
				BackendInstanceId:        string(instanceID),
				ServerTime:               timestamppb.Now(),
				HeartbeatIntervalSeconds: uint32(s.opts.HeartbeatInterval.Seconds()),
			},
		},
	}
	if err := stream.Send(welcome); err != nil {
		return err
	}

	writerDone := make(chan error, 1)
	go func() { writerDone <- writeLoop(ctx, stream, conn) }()

	recvCh := make(chan recvResult, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			select {
			case recvCh <- recvResult{msg: msg, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-conn.Done():
			switch conn.Reason() {
			case ReasonSuperseded:
				// A newer connection holds the lease; this one receives no new
				// work, so it is closed rather than left half-live.
				return status.Error(codes.Aborted, "connection superseded by a newer backend connection")
			case ReasonShutdown:
				return status.Error(codes.Unavailable, "core is shutting down")
			default:
				return status.Error(codes.Unavailable, "connection closed by core")
			}

		case err := <-writerDone:
			if err == nil {
				// The writer stopped because the connection or the context was
				// closed; loop once more to report the actual reason. The
				// channel holds a single value, so this cannot spin.
				continue
			}
			return err

		case result := <-recvCh:
			if result.err != nil {
				return streamError(result.err)
			}
			if err := s.handle(conn, result.msg, logger); err != nil {
				return err
			}
		}
	}
}

// authenticate resolves the BackendInstance behind the "authorization: Bearer"
// metadata header presented on Connect. The token itself is never logged.
func (s *Server) authenticate(ctx context.Context) (domain.BackendInstanceID, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing request metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing backend credential")
	}
	token, ok := bearerToken(values[0])
	if !ok {
		return "", status.Error(codes.Unauthenticated, "malformed backend credential")
	}
	instanceID, err := s.resolver.Resolve(ctx, token)
	if err != nil {
		if errors.Is(err, ErrUnknownBackendToken) {
			return "", status.Error(codes.Unauthenticated, "unknown backend credential")
		}
		return "", status.Error(codes.Internal, "cannot resolve backend credential")
	}
	return instanceID, nil
}

// receiveHello reads and validates the mandatory first message of the stream.
func (s *Server) receiveHello(stream backendv1.BackendControl_ConnectServer) (*backendv1.Hello, error) {
	first, err := stream.Recv()
	if err != nil {
		return nil, streamError(err)
	}
	hello := first.GetHello()
	if hello == nil {
		return nil, status.Error(codes.InvalidArgument, "first control message must be hello")
	}
	if version := hello.GetProtocol().GetVersion(); version != ProtocolVersion {
		return nil, status.Errorf(codes.FailedPrecondition,
			"unsupported protocol version %d, core speaks %d", version, ProtocolVersion)
	}
	return hello, nil
}

// handle processes one backend message.
func (s *Server) handle(conn *Connection, msg *backendv1.BackendToCore, logger *slog.Logger) error {
	switch body := msg.GetMessage().(type) {
	case *backendv1.BackendToCore_Hello:
		return status.Error(codes.InvalidArgument, "hello may only be sent once, at the start of the stream")

	case *backendv1.BackendToCore_Heartbeat:
		at := body.Heartbeat.GetSentAt().AsTime()
		if at.IsZero() {
			at = time.Now()
		}
		conn.NoteHeartbeat(at)
		logger.Debug("heartbeat", slog.Time("sentAt", at))
		return nil

	case *backendv1.BackendToCore_StatusUpdate:
		logger.Info("backend status update",
			slog.String("status", body.StatusUpdate.GetStatus().String()),
			slog.String("providerAuth", body.StatusUpdate.GetProviderAuthState().String()),
			slog.Int("activeRuns", int(body.StatusUpdate.GetCapacity().GetActiveRuns())),
			slog.Int("maxConcurrentRuns", int(body.StatusUpdate.GetCapacity().GetMaxConcurrentRuns())),
		)
		return nil

	case *backendv1.BackendToCore_ReconcileState:
		logger.Info("backend reconcile state", slog.Int("runs", len(body.ReconcileState.GetRuns())))
		return nil

	case *backendv1.BackendToCore_JobEvent:
		// Not persisted yet, and therefore not acknowledged: the backend keeps
		// the event in its durable local buffer and replays it after the Core
		// event pipeline lands.
		logger.Debug("job event received but not persisted yet",
			slog.String("jobId", body.JobEvent.GetJobId()),
			slog.Uint64("backendSequence", body.JobEvent.GetBackendSequence()),
		)
		return nil

	case *backendv1.BackendToCore_EphemeralJobEvent:
		logger.Debug("ephemeral job event",
			slog.String("jobId", body.EphemeralJobEvent.GetJobId()),
			slog.String("kind", body.EphemeralJobEvent.GetKind()),
		)
		return nil

	case *backendv1.BackendToCore_CommandResult:
		logger.Debug("command result",
			slog.String("commandId", body.CommandResult.GetCommandId()),
			slog.Bool("accepted", body.CommandResult.GetAccepted()),
		)
		return nil

	case *backendv1.BackendToCore_CoreToolRequest:
		// Core Tools are not implemented yet. Answering with an explicit error
		// keeps the agent from waiting forever.
		request := body.CoreToolRequest
		logger.Debug("core tool request rejected", slog.String("tool", request.GetName()))
		conn.Send(&backendv1.CoreToBackend{
			Message: &backendv1.CoreToBackend_CoreToolResponse{
				CoreToolResponse: &backendv1.CoreToolResponse{
					RequestId: request.GetRequestId(),
					Error: &backendv1.Error{
						Code:    codes.Unimplemented.String(),
						Message: fmt.Sprintf("core tool %q is not implemented yet", request.GetName()),
					},
				},
			},
		})
		return nil

	default:
		logger.Warn("unknown control message ignored")
		return nil
	}
}

// writeLoop is the single writer of the stream: gRPC streams do not allow
// concurrent sends.
func writeLoop(ctx context.Context, stream backendv1.BackendControl_ConnectServer, conn *Connection) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-conn.Done():
			return nil
		case msg, ok := <-conn.Outbound():
			if !ok {
				return nil
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

// bearerToken extracts the credential from an Authorization header value.
func bearerToken(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// streamError normalises a stream read error: a backend going away is expected,
// not a Core failure.
func streamError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.Canceled, codes.Unavailable:
			return nil
		}
	}
	return err
}

func capabilityNames(capabilities []backendv1.Capability) []string {
	out := make([]string, 0, len(capabilities))
	for _, c := range capabilities {
		out = append(out, c.String())
	}
	return out
}

// Shutdown ends every live control stream so a graceful gRPC stop drains
// immediately instead of waiting for long-lived streams to end on their own.
// Backends treat it as a normal disconnection and reconnect.
func (s *Server) Shutdown() {
	s.registry.CloseAll(ReasonShutdown)
}
