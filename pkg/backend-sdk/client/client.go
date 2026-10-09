package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	sdkevents "github.com/rclsilver/threavia/pkg/backend-sdk/events"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
	sdktools "github.com/rclsilver/threavia/pkg/backend-sdk/tools"
)

// Errors returned by the SDK.
var (
	// ErrUnsupportedCommand is returned by a Handler that does not implement a
	// command. Core receives it as a rejected CommandResult.
	ErrUnsupportedCommand = errors.New("command not supported by this backend")
	// ErrNotConnected is returned when no control stream is established.
	ErrNotConnected = errors.New("not connected to core")
)

// stableConnection is how long a connection must last before the reconnection
// backoff is reset to its minimum.
const stableConnection = time.Minute

// session is one established control stream.
type session struct {
	connectionID string
	send         chan *backendv1.BackendToCore
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *session) close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Client maintains the single outbound control stream to Core and exposes the
// backend-to-Core operations.
type Client struct {
	cfg     Config
	handler Handler
	store   state.Store
	logger  *slog.Logger
	events  *sdkevents.Factory
	pending *sdktools.Pending

	activeRuns atomic.Int32

	skillMu      sync.Mutex
	skillFetches map[string]*skillFetch

	mu      sync.RWMutex
	current *session
}

// New builds a Client. The store provides the durable local state that lets the
// backend survive a Core outage.
func New(cfg Config, handler Handler, store state.Store, logger *slog.Logger) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("a handler is required")
	}
	if store == nil {
		return nil, errors.New("a durable state store is required")
	}
	if logger == nil {
		return nil, errors.New("a logger is required")
	}
	return &Client{
		cfg:     cfg,
		handler: handler,
		store:   store,
		logger:  logger,
		events:  sdkevents.NewFactory(store),
		pending: sdktools.NewPending(),
	}, nil
}

// Events returns the event factory bound to the durable state.
func (c *Client) Events() *sdkevents.Factory { return c.events }

// SetActiveRuns updates the active Run count reported to Core.
func (c *Client) SetActiveRuns(n int32) { c.activeRuns.Store(n) }

// ConnectionID returns the current connection lease, empty when disconnected.
func (c *Client) ConnectionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.current == nil {
		return ""
	}
	return c.current.connectionID
}

// Connected reports whether a control stream is established.
func (c *Client) Connected() bool { return c.ConnectionID() != "" }

// Run keeps the control stream connected until ctx is cancelled. It returns nil
// on a clean shutdown; a Core outage is a normal condition handled by
// reconnecting, never a fatal error.
func (c *Client) Run(ctx context.Context) error {
	backoff := c.cfg.ReconnectMinBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		startedAt := time.Now()
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			c.logger.Warn("core control stream closed", slog.String("error", err.Error()))
		} else {
			c.logger.Info("core control stream closed")
		}

		if time.Since(startedAt) >= stableConnection {
			backoff = c.cfg.ReconnectMinBackoff
		}

		c.logger.Info("reconnecting to core", slog.Duration("in", backoff))
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}

		if backoff *= 2; backoff > c.cfg.ReconnectMaxBackoff {
			backoff = c.cfg.ReconnectMaxBackoff
		}
	}
}

// runOnce establishes one control stream and serves it until it drops.
func (c *Client) runOnce(ctx context.Context) (err error) {
	opts, err := c.cfg.dialOptions()
	if err != nil {
		return err
	}

	conn, err := grpc.NewClient(c.cfg.CoreAddress, opts...)
	if err != nil {
		return fmt.Errorf("create core client: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := waitForReady(ctx, conn, c.cfg.DialTimeout); err != nil {
		return err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamCtx = metadata.AppendToOutgoingContext(streamCtx, "authorization", "Bearer "+c.cfg.Token)

	stream, err := backendv1.NewBackendControlClient(conn).Connect(streamCtx)
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}

	if err := stream.Send(c.hello()); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("await welcome: %w", err)
	}
	welcome := first.GetWelcome()
	if welcome == nil {
		return errors.New("first core message must be a welcome")
	}

	sess := &session{
		connectionID: welcome.GetConnectionId(),
		send:         make(chan *backendv1.BackendToCore, c.cfg.SendBuffer),
		done:         make(chan struct{}),
	}
	c.setSession(sess)
	defer func() {
		c.clearSession(sess)
		c.pending.FailAll(ErrNotConnected)
		// A cancelled caller context is a clean shutdown, not a failure.
		cause := err
		if ctx.Err() != nil {
			cause = nil
		}
		c.handler.OnDisconnected(ctx, cause)
	}()

	c.logger.Info("connected to core",
		slog.String("connectionId", welcome.GetConnectionId()),
		slog.String("backendInstanceId", welcome.GetBackendInstanceId()),
	)

	if err := c.handler.OnConnected(ctx, welcome); err != nil {
		return fmt.Errorf("handler rejected the connection: %w", err)
	}

	// Reconciliation first: Core needs to know what actually happened locally
	// before it sends any new work.
	if err := c.sendReconcileState(ctx); err != nil {
		c.logger.Warn("cannot report reconciliation state", slog.String("error", err.Error()))
	}
	if err := c.replayPending(ctx); err != nil {
		c.logger.Warn("cannot replay buffered events", slog.String("error", err.Error()))
	}

	writerDone := make(chan error, 1)
	go func() {
		err := writeLoop(streamCtx, stream, sess)
		if err != nil {
			// Tear the stream down so the blocking Recv below returns instead
			// of waiting on a connection the writer already gave up on.
			cancel()
		}
		writerDone <- err
	}()

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		c.heartbeatLoop(streamCtx, sess)
	}()
	defer func() {
		sess.close()
		<-heartbeatDone
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			select {
			case werr := <-writerDone:
				if werr != nil {
					return werr
				}
			default:
			}
			return err
		}
		c.dispatch(ctx, msg)
	}
}

// hello builds the mandatory first message of the stream.
func (c *Client) hello() *backendv1.BackendToCore {
	return &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_Hello{
			Hello: &backendv1.Hello{
				Protocol:     &backendv1.ProtocolInfo{Version: ProtocolVersion},
				Sdk:          &backendv1.SdkInfo{Name: SDKName, Version: SDKVersion},
				Backend:      &backendv1.BackendInfo{Name: c.cfg.BackendName, Version: c.cfg.BackendVersion},
				InstanceName: c.cfg.InstanceName,
				Capabilities: c.cfg.Capabilities,
				Features:     c.cfg.featureFlags(),
				Capacity:     c.capacity(),
			},
		},
	}
}

func (c *Client) capacity() *backendv1.Capacity {
	return &backendv1.Capacity{
		MaxConcurrentRuns: c.cfg.MaxConcurrentRuns,
		ActiveRuns:        c.activeRuns.Load(),
	}
}

// dispatch routes one Core message.
func (c *Client) dispatch(ctx context.Context, msg *backendv1.CoreToBackend) {
	commandID := msg.GetCommandId()

	var err error
	switch body := msg.GetMessage().(type) {
	case *backendv1.CoreToBackend_Welcome:
		c.logger.Warn("unexpected welcome received mid-stream")
		return

	case *backendv1.CoreToBackend_EventAck:
		ack := body.EventAck
		if ackErr := c.store.Ack(ctx, ack.GetJobId(), ack.GetThroughBackendSequence()); ackErr != nil {
			c.logger.Error("cannot drop acknowledged events",
				slog.String("jobId", ack.GetJobId()),
				slog.String("error", ackErr.Error()))
		}
		return

	case *backendv1.CoreToBackend_SkillBundle:
		c.onSkillBundle(body.SkillBundle)
		return

	case *backendv1.CoreToBackend_CoreToolResponse:
		c.pending.Complete(body.CoreToolResponse)
		return

	case *backendv1.CoreToBackend_WorkspaceDiffRequest:
		go c.answerDiff(ctx, body.WorkspaceDiffRequest)
		return

	case *backendv1.CoreToBackend_StartJob:
		err = c.handler.OnStartJob(ctx, body.StartJob)
	case *backendv1.CoreToBackend_CancelJob:
		err = c.handler.OnCancelJob(ctx, body.CancelJob)
	case *backendv1.CoreToBackend_UpdateJobPolicy:
		err = c.handler.OnUpdateJobPolicy(ctx, body.UpdateJobPolicy)
	case *backendv1.CoreToBackend_ValidationResolution:
		err = c.handler.OnValidationResolution(ctx, body.ValidationResolution)
	case *backendv1.CoreToBackend_UserInputResolution:
		err = c.handler.OnUserInputResolution(ctx, body.UserInputResolution)
	case *backendv1.CoreToBackend_JobInputNow:
		err = c.handler.OnJobInputNow(ctx, body.JobInputNow)
	case *backendv1.CoreToBackend_JobInputNext:
		err = c.handler.OnJobInputNext(ctx, body.JobInputNext)
	case *backendv1.CoreToBackend_ReconcileInstruction:
		err = c.handler.OnReconcileInstruction(ctx, body.ReconcileInstruction)
	default:
		err = fmt.Errorf("unknown command")
	}

	if commandID == "" {
		if err != nil {
			c.logger.Warn("command failed", slog.String("error", err.Error()))
		}
		return
	}
	c.sendCommandResult(ctx, commandID, err)
}

func (c *Client) sendCommandResult(ctx context.Context, commandID string, cause error) {
	result := &backendv1.CommandResult{CommandId: commandID, Accepted: cause == nil}
	if cause != nil {
		code := "REJECTED"
		if errors.Is(cause, ErrUnsupportedCommand) {
			code = "UNSUPPORTED"
		}
		result.Error = &backendv1.Error{Code: code, Message: cause.Error()}
	}
	if err := c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_CommandResult{CommandResult: result},
	}); err != nil {
		c.logger.Warn("cannot report command result", slog.String("error", err.Error()))
	}
}

// SendEvent durably records a Job event and sends it to Core.
//
// The event is stored before being sent, so a Core outage or a crash cannot lose
// it: it stays buffered until Core acknowledges persisting it.
func (c *Client) SendEvent(ctx context.Context, event *backendv1.JobEvent) error {
	if event == nil {
		return errors.New("event is required")
	}
	if err := c.store.AppendPending(ctx, event); err != nil {
		return fmt.Errorf("record event locally: %w", err)
	}
	if err := c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_JobEvent{JobEvent: event},
	}); err != nil {
		// Buffered: it is replayed on the next connection.
		c.logger.Debug("event buffered until core is reachable",
			slog.String("jobId", event.GetJobId()),
			slog.Uint64("backendSequence", event.GetBackendSequence()))
	}
	return nil
}

// SendEphemeral streams a liveness-only event. It is dropped when disconnected:
// ephemeral signals are never retained.
func (c *Client) SendEphemeral(ctx context.Context, event *backendv1.EphemeralJobEvent) {
	_ = c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_EphemeralJobEvent{EphemeralJobEvent: event},
	})
}

// SendStatus reports the backend operational status, conditions and provider
// authentication state. OFFLINE is never reportable: Core infers it.
func (c *Client) SendStatus(ctx context.Context, status backendv1.BackendOperationalStatus, providerAuth backendv1.ProviderAuthState, conditions ...*backendv1.Condition) error {
	if status == backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_UNSPECIFIED {
		return errors.New("an operational status is required")
	}
	return c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_StatusUpdate{
			StatusUpdate: &backendv1.StatusUpdate{
				Status:            status,
				Conditions:        conditions,
				Capacity:          c.capacity(),
				ProviderAuthState: providerAuth,
			},
		},
	})
}

// Invoke calls a Core Tool and waits for the response. It implements
// tools.Invoker.
func (c *Client) Invoke(ctx context.Context, call sdktools.Call) (*structpb.Struct, error) {
	requestID := uuid.NewString()
	resultCh := c.pending.Begin(requestID)

	if err := c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_CoreToolRequest{
			CoreToolRequest: &backendv1.CoreToolRequest{
				RequestId: requestID,
				RunId:     call.RunID,
				JobId:     call.JobID,
				Name:      call.Name,
				Input:     call.Input,
				File:      call.File,
			},
		},
	}); err != nil {
		c.pending.Cancel(requestID)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.pending.Cancel(requestID)
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.Output, result.Err
	}
}

// sendReconcileState reports what the backend believes is actually happening
// locally, so Core can converge its desired state.
func (c *Client) sendReconcileState(ctx context.Context) error {
	runs, err := c.store.Runs(ctx)
	if err != nil {
		return fmt.Errorf("read local runs: %w", err)
	}
	jobs, err := c.store.Jobs(ctx)
	if err != nil {
		return fmt.Errorf("read local jobs: %w", err)
	}

	jobsByRun := make(map[string][]*backendv1.JobState, len(runs))
	for _, job := range jobs {
		jobsByRun[job.RunID] = append(jobsByRun[job.RunID], &backendv1.JobState{
			JobId:               job.JobID,
			Status:              job.Status,
			LastBackendSequence: job.LastSequence,
		})
	}

	report := make([]*backendv1.RunState, 0, len(runs))
	for _, run := range runs {
		report = append(report, &backendv1.RunState{
			RunId:           run.RunID,
			NativeSessionId: run.NativeSessionID,
			ResumeStatus:    run.ResumeStatus,
			Jobs:            jobsByRun[run.RunID],
		})
	}

	return c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_ReconcileState{
			ReconcileState: &backendv1.ReconcileState{Runs: report},
		},
	})
}

// replayPending resends every event Core has not acknowledged yet. Delivery is
// at-least-once; Core deduplicates on the backend event id.
func (c *Client) replayPending(ctx context.Context) error {
	events, err := c.store.PendingEvents(ctx)
	if err != nil {
		return fmt.Errorf("read buffered events: %w", err)
	}
	if len(events) == 0 {
		return nil
	}
	c.logger.Info("replaying buffered events", slog.Int("count", len(events)))
	for _, event := range events {
		if err := c.enqueue(ctx, &backendv1.BackendToCore{
			Message: &backendv1.BackendToCore_JobEvent{JobEvent: event},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) heartbeatLoop(ctx context.Context, sess *session) {
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sess.done:
			return
		case <-ticker.C:
			msg := &backendv1.BackendToCore{
				Message: &backendv1.BackendToCore_Heartbeat{
					Heartbeat: &backendv1.Heartbeat{
						SentAt:   timestamppb.Now(),
						Capacity: c.capacity(),
					},
				},
			}
			select {
			case sess.send <- msg:
			case <-sess.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

// answerDiff computes the diff Core asked for and sends it back under the
// same request id.
func (c *Client) answerDiff(ctx context.Context, req *backendv1.WorkspaceDiffRequest) {
	var answer *backendv1.WorkspaceDiff
	if differ, ok := c.handler.(WorkspaceDiffer); ok {
		answer = differ.OnWorkspaceDiffRequest(ctx, req)
	}
	if answer == nil {
		answer = &backendv1.WorkspaceDiff{Error: &backendv1.Error{
			Code: "UNSUPPORTED", Message: "this backend cannot show the diff of a change",
		}}
	}
	answer.RequestId = req.GetRequestId()
	if err := c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_WorkspaceDiff{WorkspaceDiff: answer},
	}); err != nil {
		c.logger.Warn("cannot answer a diff request", slog.String("error", err.Error()))
	}
}

func (c *Client) enqueue(ctx context.Context, msg *backendv1.BackendToCore) error {
	c.mu.RLock()
	sess := c.current
	c.mu.RUnlock()
	if sess == nil {
		return ErrNotConnected
	}
	select {
	case sess.send <- msg:
		return nil
	case <-sess.done:
		return ErrNotConnected
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) setSession(sess *session) {
	c.mu.Lock()
	c.current = sess
	c.mu.Unlock()
}

func (c *Client) clearSession(sess *session) {
	c.mu.Lock()
	if c.current == sess {
		c.current = nil
	}
	c.mu.Unlock()
	sess.close()
}

// writeLoop is the single writer of the stream: gRPC streams do not allow
// concurrent sends.
func writeLoop(ctx context.Context, stream backendv1.BackendControl_ConnectClient, sess *session) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sess.done:
			return nil
		case msg := <-sess.send:
			if err := stream.Send(msg); err != nil {
				sess.close()
				return err
			}
		}
	}
}

// waitForReady blocks until the connection is usable or timeout expires.
func waitForReady(ctx context.Context, conn *grpc.ClientConn, timeout time.Duration) error {
	conn.Connect()

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		switch state := conn.GetState(); state {
		case connectivity.Ready:
			return nil
		case connectivity.Shutdown:
			return errors.New("core connection shut down")
		default:
			if !conn.WaitForStateChange(waitCtx, state) {
				return fmt.Errorf("cannot reach core at %s: %w", conn.Target(), waitCtx.Err())
			}
		}
	}
}
