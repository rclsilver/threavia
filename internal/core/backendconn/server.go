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

// ErrCoreToolUnavailable is returned when no Core Tool implementation is wired.
var ErrCoreToolUnavailable = errors.New("core tools are not available")

// ErrSkillUnavailable is returned when no Skill distribution is wired.
var ErrSkillUnavailable = errors.New("skills are not available")

// Options configures the control server.
type Options struct {
	// HeartbeatInterval is advertised to backends in the Welcome message, and is
	// how often Monitor looks for backends that went quiet.
	HeartbeatInterval time.Duration
	// OfflineAfter is how long a connection may go without a heartbeat before
	// Core infers OFFLINE and closes it. Zero disables the check.
	OfflineAfter time.Duration
}

// Server implements the BackendControl service.
//
// It owns the transport only: the stream, the handshake, the connection lease
// and the ordering guarantees. Everything it learns is handed to a Sink, so this
// package knows nothing about Core state.
type Server struct {
	backendv1.UnimplementedBackendControlServer

	registry *Registry
	resolver TokenResolver
	sink     Sink
	logger   *slog.Logger
	opts     Options
}

// NewServer builds the control server.
func NewServer(registry *Registry, resolver TokenResolver, sink Sink, opts Options, logger *slog.Logger) *Server {
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 15 * time.Second
	}
	if sink == nil {
		sink = NopSink{}
	}
	return &Server{
		registry: registry,
		resolver: resolver,
		sink:     sink,
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
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

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

	// Detached from the stream context so that the disconnection bookkeeping
	// still runs once the stream is gone.
	lifecycleCtx := context.WithoutCancel(ctx)
	defer func() {
		s.sink.Disconnected(lifecycleCtx, instanceID, conn.ID())
		logger.Info("backend disconnected")
	}()

	if err := s.sink.Connected(ctx, instanceID, conn.ID(), hello); err != nil {
		return status.Errorf(codes.Internal, "cannot accept the backend connection: %v", err)
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
			case ReasonStale:
				return status.Error(codes.DeadlineExceeded, "no heartbeat received, reconnect and reconcile")
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
			if err := s.handle(ctx, instanceID, conn, result.msg, logger); err != nil {
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
func (s *Server) handle(ctx context.Context, instanceID domain.BackendInstanceID, conn *Connection, msg *backendv1.BackendToCore, logger *slog.Logger) error {
	switch body := msg.GetMessage().(type) {
	case *backendv1.BackendToCore_Hello:
		return status.Error(codes.InvalidArgument, "hello may only be sent once, at the start of the stream")

	case *backendv1.BackendToCore_Heartbeat:
		at := body.Heartbeat.GetSentAt().AsTime()
		if at.IsZero() {
			at = time.Now()
		}
		conn.NoteHeartbeat(at)
		s.sink.Heartbeat(ctx, instanceID, at, body.Heartbeat.GetCapacity())
		return nil

	case *backendv1.BackendToCore_StatusUpdate:
		logger.Info("backend status update",
			slog.String("status", body.StatusUpdate.GetStatus().String()),
			slog.String("providerAuth", body.StatusUpdate.GetProviderAuthState().String()),
			slog.Int("activeRuns", int(body.StatusUpdate.GetCapacity().GetActiveRuns())),
		)
		s.sink.StatusUpdate(ctx, instanceID, body.StatusUpdate)
		return nil

	case *backendv1.BackendToCore_ReconcileState:
		logger.Info("backend reconcile state", slog.Int("runs", len(body.ReconcileState.GetRuns())))
		s.sink.ReconcileState(ctx, instanceID, body.ReconcileState)
		return nil

	case *backendv1.BackendToCore_JobEvent:
		event := body.JobEvent
		acked, err := s.sink.JobEvent(ctx, instanceID, event)
		if err != nil {
			// Not acknowledged: the backend keeps the event buffered and
			// replays it. Dropping the connection would only delay the retry.
			logger.Error("cannot persist a job event",
				slog.String("jobId", event.GetJobId()),
				slog.Uint64("backendSequence", event.GetBackendSequence()),
				slog.String("error", err.Error()))
			return nil
		}
		if acked > 0 {
			conn.Send(&backendv1.CoreToBackend{
				Message: &backendv1.CoreToBackend_EventAck{
					EventAck: &backendv1.EventAck{
						RunId:                  event.GetRunId(),
						JobId:                  event.GetJobId(),
						ThroughBackendSequence: acked,
					},
				},
			})
		}
		return nil

	case *backendv1.BackendToCore_EphemeralJobEvent:
		s.sink.EphemeralJobEvent(ctx, instanceID, body.EphemeralJobEvent)
		return nil

	case *backendv1.BackendToCore_CommandResult:
		s.sink.CommandResult(ctx, instanceID, body.CommandResult)
		return nil

	case *backendv1.BackendToCore_CoreToolRequest:
		request := body.CoreToolRequest
		// A manager may wait on workers on this same connection. Keep reading
		// their events, other tool requests and heartbeats while it waits.
		go func() {
			result, err := s.sink.CoreToolRequest(ctx, instanceID, request)
			response := &backendv1.CoreToolResponse{RequestId: request.GetRequestId(), Result: result}
			if err != nil {
				// Answering with an explicit error keeps the agent from waiting
				// forever on a tool Core cannot run.
				response.Error = &backendv1.Error{
					Code:    codes.Unimplemented.String(),
					Message: fmt.Sprintf("core tool %q: %v", request.GetName(), err),
				}
			}
			conn.Send(&backendv1.CoreToBackend{
				Message: &backendv1.CoreToBackend_CoreToolResponse{CoreToolResponse: response},
			})
		}()
		return nil

	case *backendv1.BackendToCore_SkillInventory:
		logger.Info("backend skill inventory",
			slog.Int("local", len(body.SkillInventory.GetLocal())))
		s.sink.SkillInventory(ctx, instanceID, body.SkillInventory)
		return nil

	case *backendv1.BackendToCore_SkillFetchRequest:
		// Served from its own goroutine: a bundle is megabytes, and the control
		// stream must keep carrying Job traffic while it is sent.
		go s.sendSkillBundle(context.WithoutCancel(ctx), instanceID, conn, body.SkillFetchRequest, logger)
		return nil

	case *backendv1.BackendToCore_WorkspaceDiff:
		s.sink.WorkspaceDiff(ctx, instanceID, body.WorkspaceDiff)
		return nil

	case *backendv1.BackendToCore_RepositoryStatus:
		s.sink.RepositoryStatus(ctx, instanceID, body.RepositoryStatus)
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

// skillChunkBytes is how much of a bundle travels in one message. gRPC bounds a
// message, and a Skill can carry references and assets.
const skillChunkBytes = 256 << 10

// sendSkillBundle streams a Skill bundle to the backend that asked for it.
//
// A failure is reported in-band rather than dropped: a backend waiting for a
// bundle must learn it is not coming, or the Job that needs the Skill hangs.
func (s *Server) sendSkillBundle(ctx context.Context, instanceID domain.BackendInstanceID, conn *Connection, request *backendv1.SkillFetchRequest, logger *slog.Logger) {
	refuse := func(err error) {
		conn.Send(&backendv1.CoreToBackend{
			Message: &backendv1.CoreToBackend_SkillBundle{
				SkillBundle: &backendv1.SkillBundle{
					RequestId: request.GetRequestId(),
					SkillId:   request.GetSkillId(),
					Last:      true,
					Error: &backendv1.Error{
						Code:    codes.NotFound.String(),
						Message: fmt.Sprintf("skill %q: %v", request.GetSkillId(), err),
					},
				},
			},
		})
	}

	body, err := s.sink.SkillBundle(ctx, instanceID, request.GetSkillId())
	if err != nil {
		logger.Warn("cannot serve a skill bundle",
			slog.String("skillId", request.GetSkillId()), slog.String("error", err.Error()))
		refuse(err)
		return
	}
	defer func() { _ = body.Close() }()

	buffer := make([]byte, skillChunkBytes)
	var index uint32
	for {
		n, readErr := body.Read(buffer)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			conn.Send(&backendv1.CoreToBackend{
				Message: &backendv1.CoreToBackend_SkillBundle{
					SkillBundle: &backendv1.SkillBundle{
						RequestId:  request.GetRequestId(),
						SkillId:    request.GetSkillId(),
						Chunk:      chunk,
						ChunkIndex: index,
					},
				},
			})
			index++
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			logger.Error("cannot read a skill bundle",
				slog.String("skillId", request.GetSkillId()), slog.String("error", readErr.Error()))
			refuse(readErr)
			return
		}
	}

	conn.Send(&backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_SkillBundle{
			SkillBundle: &backendv1.SkillBundle{
				RequestId:  request.GetRequestId(),
				SkillId:    request.GetSkillId(),
				ChunkIndex: index,
				Last:       true,
			},
		},
	})
}
