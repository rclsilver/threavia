package externalmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
)

func testServer(calls *atomic.Int64) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	s.AddTool(&sdk.Tool{Name: "counter", Description: "Fixture counter", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		calls.Add(1)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
	})
	return s
}
func TestStreamableHTTPDiscoveryAndCallAreNotReplayed(t *testing.T) {
	var calls atomic.Int64
	s := testServer(&calls)
	host := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil))
	defer host.Close()
	v := mcpconfig.Server{ID: "fixture", Name: "fixture", Transport: "http", URL: host.URL, Tools: []string{"counter"}}
	c, err := Connect(context.Background(), v, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools=%v, err=%v", tools, err)
	}
	if _, err = c.Call(context.Background(), "counter", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("call count=%d", calls.Load())
	}
}

func TestAWriteWithAnUncertainResponseIsNotRepeated(t *testing.T) {
	var calls atomic.Int64
	server := testServer(&calls)
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var message struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &message)
			if message.Method == "tools/call" {
				rec := httptest.NewRecorder()
				inner.ServeHTTP(rec, r)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
	defer host.Close()
	v := mcpconfig.Server{Name: "fixture", Transport: "http", URL: host.URL}
	c, err := Connect(context.Background(), v, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Call(context.Background(), "counter", map[string]any{}); err == nil {
		t.Fatal("uncertain response was treated as success")
	}
	if calls.Load() != 1 {
		t.Fatalf("write ran %d times", calls.Load())
	}
}
func TestImportPreviewsNeverExposeInlineSecretsOrEnableServers(t *testing.T) {
	result := ImportDeclarations(map[string]map[string]any{"docs": {"command": "server", "args": []any{"--token", "synthetic-secret"}, "env": map[string]any{"TOKEN": "synthetic-secret", "REF": "${DOCS_TOKEN}"}, "headers": map[string]any{"Authorization": "synthetic-secret"}}, "unsafe": {"url": "https://example.test/?token=synthetic-secret"}})
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "synthetic-secret") {
		t.Fatal("inline credential leaked")
	}
	if !strings.Contains(string(raw), "DOCS_TOKEN") {
		t.Fatal("safe reference was lost")
	}
	if strings.Contains(string(raw), `"enabled":true`) {
		t.Fatal("import silently enabled a server")
	}
}
func TestStdioFixture(t *testing.T) {
	if os.Getenv("THREAVIA_MCP_FIXTURE") != "yes" {
		return
	}
	var calls atomic.Int64
	s := testServer(&calls)
	if err := s.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestLocalStdioProcessIsClosedWithTheConnection(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("THREAVIA_MCP_TEST_SWITCH", "yes")
	v := mcpconfig.Server{ID: "local", Name: "local", Transport: "stdio", Command: exe, Args: []string{"-test.run=^TestStdioFixture$"}, Env: map[string]string{"THREAVIA_MCP_FIXTURE": "THREAVIA_MCP_TEST_SWITCH"}, Tools: []string{"counter"}}
	c, err := Connect(context.Background(), v, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 1 {
		c.Close()
		t.Fatalf("stdio discovery failed: %v", err)
	}
	c.Close()
	if _, err = c.Call(context.Background(), "counter", map[string]any{}); err == nil {
		t.Fatal("closed connection accepted a call")
	}
}
