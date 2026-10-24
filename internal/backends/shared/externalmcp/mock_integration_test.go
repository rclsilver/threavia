package externalmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rclsilver/threavia/internal/dev/mcpmock"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
)

func mockFixture(t *testing.T) (*mcpmock.Server, *httptest.Server, mcpconfig.Server, string) {
	t.Helper()
	host := httptest.NewUnstartedServer(nil)
	server, err := mcpmock.New("http://" + host.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	host.Config.Handler = server
	host.Start()
	t.Cleanup(host.Close)
	return server, host, mcpconfig.Server{ID: "oauth-mock", Name: "oauth_mock", Transport: "http", URL: host.URL + "/mcp", OAuth: true}, t.TempDir()
}
func consentMock(t *testing.T, v mcpconfig.Server, decision string) (map[string]any, string) {
	t.Helper()
	started, err := StartAuth(context.Background(), v, "http://127.0.0.1:18083/settings/mcp")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page, err := client.Get(started["authorizationUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(page.Body)
	page.Body.Close()
	match := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindSubmatch(raw)
	if page.StatusCode != 200 || len(match) != 2 {
		t.Fatalf("consent page status=%d", page.StatusCode)
	}
	target, _ := url.Parse(started["authorizationUrl"].(string))
	target.Path = "/consent"
	target.RawQuery = ""
	response, err := client.PostForm(target.String(), url.Values{"flow": {string(match[1])}, "decision": {decision}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("consent status=%d", response.StatusCode)
	}
	return started, response.Header.Get("Location")
}
func loginMock(t *testing.T, v mcpconfig.Server, root string) {
	t.Helper()
	started, callback := consentMock(t, v, "allow")
	if _, err := FinishAuth(context.Background(), root, v, started["state"].(string), callback); err != nil {
		t.Fatal(err)
	}
}
func mockJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	r, ok := v.(*sdk.CallToolResult)
	if !ok || r.IsError || len(r.Content) == 0 {
		t.Fatalf("unexpected mock result %T", v)
	}
	text, ok := r.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatal("missing text content")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestMockOAuthRealDiscoveryRefreshCRUDAndLogout(t *testing.T) {
	server, _, v, root := mockFixture(t)
	if c, err := Connect(context.Background(), v, root, ""); err == nil {
		c.Close()
		t.Fatal("connection worked before OAuth login")
	}
	loginMock(t, v, root)
	c, err := Connect(context.Background(), v, root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 5 {
		t.Fatalf("tools=%d err=%v", len(tools), err)
	}
	out, err := c.Call(context.Background(), "mock_read", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if mockJSON(t, out)["account"] != "mock-user" {
		t.Fatal("wrong fictional account")
	}
	out, err = c.Call(context.Background(), "mock_create_note", map[string]any{"text": "temporary test"})
	if err != nil {
		t.Fatal(err)
	}
	id := mockJSON(t, out)["id"].(string)
	if _, err = c.Call(context.Background(), "mock_delete_note", map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
	out, err = c.Call(context.Background(), "mock_list_notes", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mockJSON(t, out)["notes"].(map[string]any)) != 0 {
		t.Fatal("mock note not deleted")
	}
	raw, err := os.ReadFile(tokenPath(root, v.ID))
	if err != nil {
		t.Fatal(err)
	}
	var g grant
	if json.Unmarshal(raw, &g) != nil {
		t.Fatal("invalid backend grant")
	}
	g.Token.Expiry = time.Now().Add(-time.Hour)
	if err = writeGrant(root, v, g); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Call(context.Background(), "mock_read", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if server.Snapshot().Refreshes != 1 {
		t.Fatal("refresh-token flow was not used exactly once")
	}
	if err = Logout(root, v); err != nil {
		t.Fatal(err)
	}
	before := server.Snapshot().Reads
	if _, err = c.Call(context.Background(), "mock_read", map[string]any{}); err == nil {
		t.Fatal("existing connection survived local logout")
	}
	if server.Snapshot().Reads != before {
		t.Fatal("logged-out call reached mock")
	}
	loginMock(t, v, root)
	next, err := Connect(context.Background(), v, root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = next.Call(context.Background(), "mock_read", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Call(context.Background(), "mock_read", map[string]any{}); err == nil {
		t.Fatal("old connection silently adopted new grant")
	}
}
func TestMockRejectsPKCEAndResourceMismatchAndCodeReplay(t *testing.T) {
	_, host, v, root := mockFixture(t)
	started, callback := consentMock(t, v, "allow")
	authMu.Lock()
	flow := flows[started["state"].(string)]
	authMu.Unlock()
	cb, _ := url.Parse(callback)
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {flow.config.ClientID}, "redirect_uri": {flow.config.RedirectURL}, "resource": {v.URL}, "code": {cb.Query().Get("code")}, "code_verifier": {"wrong-verifier"}}
	post := func(want int) {
		t.Helper()
		r, e := http.PostForm(host.URL+"/token", form)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("token status=%d want=%d", r.StatusCode, want)
		}
	}
	post(400)
	form.Set("code_verifier", flow.verifier)
	form.Set("resource", host.URL+"/other")
	post(400)
	if _, err := FinishAuth(context.Background(), root, v, started["state"].(string), callback); err != nil {
		t.Fatal(err)
	}
	form.Set("resource", v.URL)
	post(400)
}
func TestMockConsentDenialAndLoopbackOnly(t *testing.T) {
	_, host, v, root := mockFixture(t)
	started, callback := consentMock(t, v, "deny")
	if !strings.Contains(callback, "error=access_denied") {
		t.Fatal("consent refusal not returned")
	}
	if _, err := FinishAuth(context.Background(), root, v, started["state"].(string), callback); err == nil {
		t.Fatal("refused consent succeeded")
	}
	if _, err := tokenTransport(context.Background(), root, v); err == nil {
		t.Fatal("refused consent saved credentials")
	}
	if _, err := mcpmock.New("https://example.com"); err == nil {
		t.Fatal("mock accepted non-loopback origin")
	}
	r, err := http.Post(host.URL+"/register", "application/json", strings.NewReader(`{"redirect_uris":["https://example.com/callback"],"token_endpoint_auth_method":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("mock registered external redirect")
	}
}
func TestMockSlowReadIsCancelledOnTheServer(t *testing.T) {
	server, _, v, root := mockFixture(t)
	loginMock(t, v, root)
	c, err := Connect(context.Background(), v, root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := c.Call(ctx, "mock_slow_read", map[string]any{}); done <- e }()
	deadline := time.Now().Add(3 * time.Second)
	for server.Snapshot().SlowStarted == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.Snapshot().SlowStarted != 1 {
		t.Fatal("slow tool did not start")
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancelled call succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not return")
	}
	deadline = time.Now().Add(2 * time.Second)
	for server.Snapshot().SlowCancelled == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stats := server.Snapshot()
	if stats.SlowCancelled != 1 || stats.SlowCompleted != 0 {
		t.Fatalf("slow call counters=%+v", stats)
	}
}
