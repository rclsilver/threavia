package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/api"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/service"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
	sdkclient "github.com/rclsilver/threavia/pkg/backend-sdk/client"
	sdkevents "github.com/rclsilver/threavia/pkg/backend-sdk/events"
	sdkstate "github.com/rclsilver/threavia/pkg/backend-sdk/state"
	sdktools "github.com/rclsilver/threavia/pkg/backend-sdk/tools"
)

// core is a whole Threavia Core wired the way the binary wires it: a real
// database, the real services, the real HTTP surface and the real gRPC control
// service, reachable in memory.
type core struct {
	t         *testing.T
	backendID string
	lastBody  string
	store     *postgres.Store
	svc       *service.Service
	http      *httptest.Server
	dialer    grpc.DialOption
}

func newCore(t *testing.T) *core {
	t.Helper()

	store, db := pgtest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	registry := backendconn.NewRegistry()
	svc := service.New(store, events.NewBroker(), registry, logger)

	control := backendconn.NewServer(registry, svc, svc,
		backendconn.Options{HeartbeatInterval: 500 * time.Millisecond}, logger)

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	backendv1.RegisterBackendControlServer(grpcServer, control)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	authenticator, err := auth.New(auth.Config{Mode: auth.ModeNone, DevUserID: "thomas"})
	if err != nil {
		t.Fatalf("building the authenticator: %v", err)
	}

	server := httptest.NewServer(api.NewRouter(api.Options{
		Service:       svc,
		Authenticator: authenticator,
		Database:      db,
		Version:       "test",
		Logger:        logger,
	}))
	t.Cleanup(server.Close)

	return &core{
		t:     t,
		store: store,
		svc:   svc,
		http:  server,
		dialer: grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	}
}

// do performs an API call and decodes the JSON response into target.
func (c *core) do(method, path string, body any, target any, headers ...[2]string) int {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encoding the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, c.http.URL+path, reader)
	if err != nil {
		c.t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, header := range headers {
		req.Header.Set(header[0], header[1])
	}

	resp, err := c.http.Client().Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, _ := io.ReadAll(resp.Body)
	c.lastBody = string(payload)
	if target != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(payload, target); err != nil {
			c.t.Fatalf("decoding the response of %s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// mustDo fails the test unless the call returned the expected status.
func (c *core) mustDo(method, path string, body any, target any, want int, headers ...[2]string) {
	c.t.Helper()
	if got := c.do(method, path, body, target, headers...); got != want {
		c.t.Fatalf("%s %s = %d, want %d: %s", method, path, got, want, c.lastBody)
	}
}

// fakeBackend is a BackendInstance built on the real SDK. It records the
// commands Core sends and lets a test emit events on demand.
type fakeBackend struct {
	sdkclient.BaseHandler

	t      *testing.T
	sdk    *sdkclient.Client
	events *sdkevents.Factory

	starts       chan *backendv1.StartJob
	cancels      chan *backendv1.CancelJob
	validations  chan *backendv1.ValidationResolution
	inputs       chan *backendv1.UserInputResolution
	reconciles   chan *backendv1.ReconcileInstruction
	acceptStarts bool
}

func (b *fakeBackend) OnStartJob(_ context.Context, cmd *backendv1.StartJob) error {
	b.starts <- cmd
	return nil
}

func (b *fakeBackend) OnCancelJob(_ context.Context, cmd *backendv1.CancelJob) error {
	b.cancels <- cmd
	return nil
}

func (b *fakeBackend) OnValidationResolution(_ context.Context, cmd *backendv1.ValidationResolution) error {
	b.validations <- cmd
	return nil
}

func (b *fakeBackend) OnUserInputResolution(_ context.Context, cmd *backendv1.UserInputResolution) error {
	b.inputs <- cmd
	return nil
}

func (b *fakeBackend) OnReconcileInstruction(_ context.Context, cmd *backendv1.ReconcileInstruction) error {
	b.reconciles <- cmd
	return nil
}

// registerBackend creates a BackendInstance through the public API, the way a
// real backend does: a one-shot user token in, a persistent credential out.
func (c *core) registerBackend(name string) (id, credential string) {
	c.t.Helper()

	var token struct {
		Token string `json:"token"`
	}
	c.mustDo(http.MethodPost, "/api/v1/backend-tokens", map[string]any{"label": name}, &token, http.StatusCreated)

	var registered struct {
		BackendInstanceID string `json:"backendInstanceId"`
		Credential        string `json:"credential"`
		Claimed           bool   `json:"claimed"`
	}
	c.mustDo(http.MethodPost, "/api/v1/backends/register",
		map[string]any{"name": name, "capabilities": []string{"CODE"}, "token": token.Token},
		&registered, http.StatusCreated)

	if !registered.Claimed {
		c.t.Fatal("a token registration must yield an owned instance")
	}
	c.backendID = registered.BackendInstanceID
	return registered.BackendInstanceID, registered.Credential
}

// connectBackend starts a fake backend against Core and waits until it holds
// the connection lease.
func (c *core) connectBackend(credential string) *fakeBackend {
	c.t.Helper()

	backend := &fakeBackend{
		t:           c.t,
		starts:      make(chan *backendv1.StartJob, 8),
		cancels:     make(chan *backendv1.CancelJob, 8),
		validations: make(chan *backendv1.ValidationResolution, 8),
		inputs:      make(chan *backendv1.UserInputResolution, 8),
		reconciles:  make(chan *backendv1.ReconcileInstruction, 8),
	}

	cfg := sdkclient.DefaultConfig()
	cfg.CoreAddress = "passthrough:///bufnet"
	cfg.Token = credential
	cfg.InstanceName = "test-backend"
	cfg.BackendName = "fake"
	cfg.BackendVersion = "test"
	cfg.TLS.Enabled = false
	cfg.HeartbeatInterval = 200 * time.Millisecond
	cfg.DialOptions = []grpc.DialOption{c.dialer}

	sdk, err := sdkclient.New(cfg, backend, sdkstate.NewMemoryStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		c.t.Fatalf("building the backend client: %v", err)
	}
	backend.sdk = sdk
	backend.events = sdk.Events()

	ctx, cancel := context.WithCancel(context.Background())
	c.t.Cleanup(cancel)
	go func() { _ = sdk.Run(ctx) }()

	waitUntil(c.t, "the backend to connect", func() bool { return sdk.Connected() })
	return backend
}

// waitUntil polls a condition, which is how a test observes work that crosses a
// goroutine boundary without sleeping on a guess.
func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// receive takes one value from a channel, or fails.
func receive[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// expectNothing asserts that nothing arrives, for the invariants that are about
// absence.
func expectNothing[T any](t *testing.T, what string, ch <-chan T) {
	t.Helper()
	select {
	case value := <-ch:
		t.Fatalf("unexpected %s: %v", what, value)
	case <-time.After(300 * time.Millisecond):
	}
}

// startSession performs the first send and returns the Session id.
func (c *core) startSession(projectID, backendID, dirID, message string) string {
	c.t.Helper()

	body := map[string]any{
		"projectId":         projectID,
		"backendInstanceId": backendID,
		"message":           message,
	}
	if dirID != "" {
		body["workingDirectoryId"] = dirID
	}

	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", body, &started, http.StatusCreated)
	return started.Session.ID
}

// snapshot reads the current state of a Session the way a client opening it
// does.
func (c *core) snapshot(sessionID string) snapshotResponse {
	c.t.Helper()
	var snapshot snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+sessionID, nil, &snapshot, http.StatusOK)
	return snapshot
}

// countEvents counts the events of one type in the Session timeline.
func (c *core) countEvents(sessionID, eventType string) int {
	c.t.Helper()
	count := 0
	for _, event := range c.snapshot(sessionID).Events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

// jobStatus reads the current status of a Job from the Session snapshot.
func (c *core) jobStatus(sessionID, jobID string) string {
	c.t.Helper()
	for _, job := range c.snapshot(sessionID).Jobs {
		if job.ID == jobID {
			return job.Status
		}
	}
	return ""
}

// mustEvent builds an event, failing the test if the SDK cannot.
func (b *fakeBackend) mustEvent(t *testing.T, _ context.Context, build func() (*backendv1.JobEvent, error)) *backendv1.JobEvent {
	t.Helper()
	event, err := build()
	if err != nil {
		t.Fatalf("building a backend event: %v", err)
	}
	return event
}

// emit sends an event to Core through the real SDK path, so it is buffered
// durably, deduplicated and acknowledged exactly like a production one.
func (b *fakeBackend) emit(t *testing.T, ctx context.Context, event *backendv1.JobEvent) {
	t.Helper()
	if err := b.sdk.SendEvent(ctx, event); err != nil {
		t.Fatalf("sending a backend event: %v", err)
	}
}

// callCoreTool invokes a Core Tool the way an agent does: through the real
// control stream, and waits for Core's answer.
func (b *fakeBackend) callCoreTool(t *testing.T, ctx context.Context, runID, jobID, name string, input map[string]any) map[string]any {
	t.Helper()

	encoded, err := structpb.NewStruct(input)
	if err != nil {
		t.Fatalf("encoding the tool input: %v", err)
	}

	result, err := b.sdk.Invoke(ctx, sdktools.Call{
		RunID: runID, JobID: jobID, Name: name, Input: encoded,
	})
	if err != nil {
		t.Fatalf("core tool %s: %v", name, err)
	}
	if result == nil {
		return map[string]any{}
	}
	return result.AsMap()
}

// callCoreToolExpectingFailure invokes a Core Tool that should be refused.
func (b *fakeBackend) callCoreToolExpectingFailure(t *testing.T, ctx context.Context, runID, jobID, name string, input map[string]any) error {
	t.Helper()

	encoded, _ := structpb.NewStruct(input)
	_, err := b.sdk.Invoke(ctx, sdktools.Call{
		RunID: runID, JobID: jobID, Name: name, Input: encoded,
	})
	if err == nil {
		t.Fatalf("core tool %s was expected to fail", name)
	}
	return err
}

// createProject creates a Project through the public API and returns its id.
func (c *core) createProject(name string) string {
	c.t.Helper()

	var project struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/projects", map[string]any{"name": name}, &project, http.StatusCreated)
	return project.ID
}

// doRaw performs a call whose body is not JSON, which is what an upload is.
func (c *core) doRaw(method, path, contentType, body string, target any) int {
	c.t.Helper()

	req, err := http.NewRequest(method, c.http.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.http.Client().Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, _ := io.ReadAll(resp.Body)
	c.lastBody = string(payload)
	if target != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(payload, target); err != nil {
			c.t.Fatalf("decoding the response of %s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// download returns the raw body and the headers of a GET, for the routes that
// do not answer JSON.
func (c *core) download(path string) (string, http.Header) {
	c.t.Helper()

	resp, err := c.http.Client().Get(c.http.URL + path)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, payload)
	}
	return string(payload), resp.Header
}
