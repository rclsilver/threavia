// Package externalmcp owns backend-local MCP connections and credentials.
package externalmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
)

type Connection struct {
	Session *sdk.ClientSession
	cancel  context.CancelFunc
}

func (c *Connection) Close() { _ = c.Session.Close(); c.cancel() }

type headerTransport struct {
	headers http.Header
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// The SDK sends DELETE during Close with a detached context. Bound this
	// cleanup so an unresponsive server cannot hold cancellation indefinitely.
	_, hasDeadline := r.Context().Deadline()
	if r.Method == http.MethodDelete || r.Method == http.MethodPost && !hasDeadline {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		r = r.Clone(ctx)
	}
	q := r.Clone(r.Context())
	q.Header = r.Header.Clone()
	for k, v := range t.headers {
		q.Header[k] = v
	}
	response, err := t.base.RoundTrip(q)
	if r.Method == http.MethodDelete && response != nil && response.Body != nil {
		_ = response.Body.Close()
		response.Body = http.NoBody
	}
	return response, err
}

// Connect never installs software or opens a browser. Server failures and
// authentication failures are explicit; a tool call is never replayed here.
func Connect(ctx context.Context, v mcpconfig.Server, root, cwd string) (*Connection, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	var transport sdk.Transport
	if v.Transport == "stdio" {
		path, err := exec.LookPath(v.Command)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("command is not installed on this backend")
		}
		cmd := exec.CommandContext(lifetime, path, v.Args...)
		cmd.Dir = cwd
		cmd.Env = []string{}
		// No ambient credentials, preload variables, or inherited execution hooks.
		for _, key := range []string{"PATH", "HOME", "USER", "LANG", "TMPDIR", "SYSTEMROOT"} {
			if value, ok := os.LookupEnv(key); ok {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		for key, ref := range v.Env {
			value, ok := os.LookupEnv(ref)
			if !ok {
				cancel()
				return nil, fmt.Errorf("missing backend environment reference %s", ref)
			}
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		transport = &sdk.CommandTransport{Command: cmd, TerminateDuration: time.Second}
	} else {
		headers := http.Header{}
		for key, ref := range v.Headers {
			value, ok := os.LookupEnv(ref)
			if !ok {
				cancel()
				return nil, fmt.Errorf("missing backend environment reference %s", ref)
			}
			headers.Set(key, value)
		}
		var base http.RoundTripper = http.DefaultTransport
		if v.OAuth {
			var err error
			base, err = tokenTransport(lifetime, root, v)
			if err != nil {
				cancel()
				return nil, err
			}
		}
		client := &http.Client{Transport: headerTransport{headers: headers, base: base}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		transport = &sdk.StreamableClientTransport{Endpoint: v.URL, HTTPClient: client, MaxRetries: -1}
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "threavia", Version: "1"}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}})
	initCtx, stop := context.WithTimeout(lifetime, 20*time.Second)
	defer stop()
	session, err := client.Connect(initCtx, transport, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("MCP connection failed; check backend availability and authentication")
	}
	if session.InitializeResult() == nil || session.InitializeResult().Capabilities == nil {
		_ = session.Close()
		cancel()
		return nil, fmt.Errorf("MCP server returned invalid capabilities")
	}
	return &Connection{Session: session, cancel: cancel}, nil
}

func (c *Connection) Tools(ctx context.Context) ([]*sdk.Tool, error) {
	out := []*sdk.Tool{}
	if c.Session.InitializeResult().Capabilities.Tools == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for t, err := range c.Session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("MCP tool discovery failed")
		}
		if t == nil || t.Name == "" || len(t.Name) > 128 {
			return nil, fmt.Errorf("MCP server returned an invalid tool name")
		}
		if len(out) >= 512 {
			return nil, fmt.Errorf("MCP server exposes too many tools")
		}
		out = append(out, t)
	}
	return out, nil
}
func (c *Connection) Call(ctx context.Context, name string, input map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	v, err := c.Session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		return nil, fmt.Errorf("MCP call failed; outcome may be uncertain. Do not automatically repeat a write")
	}
	return v, nil
}
func Probe(ctx context.Context, v mcpconfig.Server, root, cwd string) (map[string]any, error) {
	c, err := Connect(ctx, v, root, cwd)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	tools, err := c.Tools(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"state": "CONNECTED", "tools": tools, "serverId": v.ID, "resources": c.Session.InitializeResult().Capabilities.Resources != nil, "prompts": c.Session.InitializeResult().Capabilities.Prompts != nil}, nil
}
func Decode(data any) (mcpconfig.Server, error) {
	raw, err := json.Marshal(data)
	var v mcpconfig.Server
	if err == nil {
		err = json.Unmarshal(raw, &v)
	}
	return v, err
}
func Selected(v mcpconfig.Server, name string) bool { return slices.Contains(v.Tools, name) }
