package adapter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestExternalMCPReadsKeepPolicyAndWritesNeedApproval(t *testing.T) {
	p := policy.Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_GUARDED, AllowNetwork: true}
	a := &Adapter{waiters: map[string]*waiter{}, jobs: map[string]*jobState{"job": {policy: p, requests: map[string]struct{}{}}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	v := mcpconfig.Server{ID: "fixture", Name: "docs", Transport: "http", Tools: []string{"read", "write"}, ReadTools: []string{"read"}}
	calls := 0
	call := func() (any, error) { calls++; return "ok", nil }
	if _, err := a.callExternalMCP(context.Background(), "job", v, "read", map[string]any{}, call); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || a.policyOf("job").Mode != p.Mode {
		t.Fatal("read changed policy or failed to call")
	}
	// No connected approval channel: a write must fail without executing.
	if _, err := a.callExternalMCP(context.Background(), "job", v, "write", map[string]any{}, call); err == nil {
		t.Fatal("write ran without validation")
	}
	if calls != 1 {
		t.Fatal("unapproved write reached server")
	}
	p.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "mcp__docs__read"}}
	a.jobs["job"].policy = p
	if _, err := a.callExternalMCP(context.Background(), "job", v, "read", map[string]any{}, call); err == nil {
		t.Fatal("explicit denial was bypassed")
	}
}

type startupFixtureRunner struct{ called chan struct{} }

func (r *startupFixtureRunner) Run(context.Context, runner.StartParams, runner.Sink) error {
	r.called <- struct{}{}
	return nil
}
func (*startupFixtureRunner) Available() error                  { return nil }
func (*startupFixtureRunner) Cancel(string) error               { return runner.ErrUnknownJob }
func (*startupFixtureRunner) Inject(string, string, bool) error { return nil }

func TestCancelDuringMCPStartupNeverStartsTheProvider(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); host.Close() }()
	cfg := Default()
	cfg.StatePath = t.TempDir() + "/state.db"
	cfg.Provider.DefaultWorkingDirectory = t.TempDir()
	cfg.Client.TLS.Enabled = false
	cfg.Client.Token = "fixture"
	store := state.NewMemoryStore()
	provider := &startupFixtureRunner{called: make(chan struct{}, 1)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(cfg, provider, store, logger)
	sdkClient, err := client.New(cfg.Client, a, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	a.Bind(sdkClient)
	declaration := toStruct(map[string]any{"id": "fixture", "name": "docs", "transport": "http", "url": host.URL, "required": true})
	if err := a.OnStartJob(context.Background(), &backendv1.StartJob{RunId: "run", JobId: "job", ProjectContext: &backendv1.ProjectContext{McpServers: []*structpb.Struct{declaration}}, ExecutionPolicy: &backendv1.ExecutionPolicy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_GUARDED, AllowNetwork: true}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP initialization did not start")
	}
	if err := a.OnCancelJob(context.Background(), &backendv1.CancelJob{RunId: "run", JobId: "job"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		_, busy := a.jobs["job"]
		a.mu.Unlock()
		if !busy {
			if len(provider.called) > 0 {
				t.Fatal("provider started after cancellation")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancelled MCP preparation did not stop")
}
func TestExternalMCPProxyKeepsOnlySelectedTools(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	for _, name := range []string{"read", "hidden"} {
		server.AddTool(&sdk.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		})
	}
	host := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer host.Close()
	cfg := Default()
	cfg.StatePath = t.TempDir() + "/state.db"
	a := &Adapter{cfg: cfg, jobs: map[string]*jobState{"job": {policy: policy.Policy{AllowNetwork: true}}}}
	v := mcpconfig.Effective{Server: mcpconfig.Server{ID: "fixture", Name: "docs", Transport: "http", URL: host.URL, Tools: []string{"read"}, ReadTools: []string{"read"}}, Required: true}
	data := toStruct(map[string]any{"id": v.ID, "name": v.Name, "transport": v.Transport, "url": v.URL, "tools": []any{"read"}, "readTools": []any{"read"}, "required": true})
	params := runner.StartParams{JobID: "job"}
	close, err := a.prepareMCP(context.Background(), &params, &backendv1.ProjectContext{McpServers: []*structpb.Struct{data}})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if len(params.ExternalTools) != 1 || len(params.CoreTools) != 1 {
		t.Fatalf("external=%d core=%d", len(params.ExternalTools), len(params.CoreTools))
	}
	if len("mcp__threavia__"+params.ExternalTools[0].Name) > 64 {
		t.Fatal("provider tool alias too long")
	}
	// Diagnostic results must pass through protobuf (SDK structs are not native Struct values).
	input := toStruct(map[string]any{"id": v.ID, "name": v.Name, "transport": "http", "url": host.URL})
	answer := a.OnMCPRequest(context.Background(), &backendv1.McpRequest{Operation: "probe", Input: input})
	if answer.Error != nil {
		t.Fatal(answer.Error)
	}
	if answer.Result.AsMap()["state"] != "CONNECTED" {
		t.Fatal("missing probe state")
	}
}

type reviewBoundaryRunner struct {
	entered, release chan struct{}
	observed         chan error
}

func (r *reviewBoundaryRunner) Run(ctx context.Context, _ runner.StartParams, _ runner.Sink) error {
	close(r.entered)
	<-r.release
	r.observed <- ctx.Err()
	return nil
}
func (*reviewBoundaryRunner) Available() error                  { return nil }
func (*reviewBoundaryRunner) Cancel(string) error               { return runner.ErrUnknownJob }
func (*reviewBoundaryRunner) Inject(string, string, bool) error { return nil }
func TestRegressionCancelAtProviderHandoff(t *testing.T) {
	cfg := Default()
	cfg.StatePath = t.TempDir() + "/state.db"
	cfg.Provider.DefaultWorkingDirectory = t.TempDir()
	cfg.Client.TLS.Enabled = false
	cfg.Client.Token = "fixture"
	store := state.NewMemoryStore()
	provider := &reviewBoundaryRunner{entered: make(chan struct{}), release: make(chan struct{}), observed: make(chan error, 1)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(cfg, provider, store, logger)
	sdkClient, err := client.New(cfg.Client, a, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	a.Bind(sdkClient)
	if err := a.OnStartJob(context.Background(), &backendv1.StartJob{RunId: "run", JobId: "job"}); err != nil {
		t.Fatal(err)
	}
	<-provider.entered
	err = a.OnCancelJob(context.Background(), &backendv1.CancelJob{RunId: "run", JobId: "job"})
	close(provider.release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-provider.observed; err == nil {
		t.Fatal("provider launch context remains live after cancellation was acknowledged")
	}
}

func TestRegressionResourceWrapperNamesAreDistinctFromServerTools(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "resource_list_resources", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	server.AddResource(&sdk.Resource{URI: "fixture://read", Name: "fixture"}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{}, nil
	})
	host := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer host.Close()
	cfg := Default()
	cfg.StatePath = t.TempDir() + "/state.db"
	a := &Adapter{cfg: cfg, jobs: map[string]*jobState{"job": {policy: policy.Policy{AllowNetwork: true}}}}
	declaration := toStruct(map[string]any{"id": "fixture", "name": "docs", "transport": "http", "url": host.URL, "tools": []any{"resource_list_resources"}, "resources": true, "required": true})
	params := runner.StartParams{JobID: "job"}
	close, err := a.prepareMCP(context.Background(), &params, &backendv1.ProjectContext{McpServers: []*structpb.Struct{declaration}})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	seen := map[string]bool{}
	for _, tool := range params.ExternalTools {
		if seen[tool.Name] {
			t.Fatalf("duplicate tool alias %s: server tool and resource wrapper collide", tool.Name)
		}
		seen[tool.Name] = true
	}
}
