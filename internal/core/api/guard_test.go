package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/api"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/config"
	"github.com/rclsilver/threavia/web"
)

// guardedRouter is a router with no service behind it. Every refusal under test
// happens before a handler reaches the service, so it needs no database; a
// request that gets through lands on the 501 every unknown /api/v1 route
// answers, which is how these tests tell "refused" from "handled".
func guardedRouter(t *testing.T, hosts []string) *httptest.Server {
	t.Helper()

	authenticator, err := auth.New(auth.Config{Mode: auth.ModeNone, DevUserID: "thomas"})
	if err != nil {
		t.Fatalf("building the authenticator: %v", err)
	}
	webUI, err := web.Handler()
	if err != nil {
		t.Fatalf("building the web client handler: %v", err)
	}
	server := httptest.NewServer(api.NewRouter(api.Options{
		Authenticator: authenticator,
		Version:       "test",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		WebUI:         webUI,
		AllowedHosts:  hosts,
	}))
	t.Cleanup(server.Close)
	return server
}

// send performs one request; host, when set, replaces the Host header.
func send(t *testing.T, server *httptest.Server, method, path, host string, headers map[string]string, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if host != "" {
		req.Host = host
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

// A browser lets any page send a text/plain body without asking the server
// first, so a JSON body that does not say it is JSON is refused.
func TestABodyMustBeDeclaredAsJSON(t *testing.T) {
	server := guardedRouter(t, nil)

	for _, contentType := range []string{"text/plain", "text/plain; charset=utf-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		resp := send(t, server, http.MethodPost, "/api/v1/projects", "",
			map[string]string{"Content-Type": contentType}, `{"name":"Threavia"}`)
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("POST with %s = %d, want 415", contentType, resp.StatusCode)
		}
	}

	resp := send(t, server, http.MethodPost, "/api/v1/projects", "", nil, `{"name":"Threavia"}`)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("POST of a body with no Content-Type = %d, want 415", resp.StatusCode)
	}
}

// A write from a page of another site is refused before it is handled;
// the client's own pages, and anything that is not a browser, get through.
func TestCrossSiteWritesAreRefused(t *testing.T) {
	server := guardedRouter(t, nil)

	cases := []struct {
		name    string
		method  string
		host    string
		headers map[string]string
		want    int
	}{
		{"another site's origin", http.MethodPost, "localhost:8080", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"a cross-site fetch", http.MethodPost, "", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"a same-site fetch", http.MethodPost, "", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"an opaque origin", http.MethodPost, "", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"a delete from another site", http.MethodDelete, "localhost:8080", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"a same-origin fetch", http.MethodPost, "", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://localhost:5173"}, http.StatusNotImplemented},
		{"a navigation the user started", http.MethodPost, "", map[string]string{"Sec-Fetch-Site": "none"}, http.StatusNotImplemented},
		{"the dev server's proxy, on another port", http.MethodPost, "localhost:8080", map[string]string{"Origin": "http://localhost:5173"}, http.StatusNotImplemented},
		{"a script, with neither header", http.MethodPost, "", nil, http.StatusNotImplemented},
		{"a read from another site", http.MethodGet, "", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusNotImplemented},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, server, tc.method, "/api/v1/not-a-route", tc.host, tc.headers, "")
			if resp.StatusCode != tc.want {
				t.Errorf("%s = %d, want %d", tc.method, resp.StatusCode, tc.want)
			}
		})
	}
}

// Registration needs no user credential, so it does not go through the
// authenticated path, and must make the origin check on its own.
func TestRegistrationRefusesCrossSiteRequests(t *testing.T) {
	server := guardedRouter(t, nil)

	for _, headers := range []map[string]string{
		{"Origin": "https://evil.example"},
		{"Sec-Fetch-Site": "cross-site"},
	} {
		headers["Content-Type"] = "application/json"
		resp := send(t, server, http.MethodPost, "/api/v1/backends/register", "localhost:8080", headers, `{}`)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("POST /api/v1/backends/register with %v = %d, want 403", headers, resp.StatusCode)
		}
	}

	// Without either header the request reaches the handler, which refuses a
	// body that is not JSON: the origin check let it through.
	resp := send(t, server, http.MethodPost, "/api/v1/backends/register", "",
		map[string]string{"Content-Type": "text/plain"}, `{}`)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("POST /api/v1/backends/register from a script = %d, want 415", resp.StatusCode)
	}
}

// In mode none Core answers only for this machine, which is what defeats DNS
// rebinding; the probes answer whatever the Host.
func TestModeNoneAnswersOnlyForThisMachine(t *testing.T) {
	server := guardedRouter(t, config.LocalHosts)

	cases := []struct {
		path string
		host string
		want int
	}{
		{"/api/v1/not-a-route", "attacker.example:8080", http.StatusMisdirectedRequest},
		{"/api/v1/not-a-route", "attacker.example", http.StatusMisdirectedRequest},
		{"/", "attacker.example:8080", http.StatusMisdirectedRequest},
		{"/api/v1/not-a-route", "localhost:8080", http.StatusNotImplemented},
		{"/api/v1/not-a-route", "LOCALHOST", http.StatusNotImplemented},
		{"/api/v1/not-a-route", "127.0.0.1:8080", http.StatusNotImplemented},
		{"/api/v1/not-a-route", "[::1]:8080", http.StatusNotImplemented},
		{"/healthz", "10.0.0.12:8080", http.StatusOK},
	}
	for _, tc := range cases {
		resp := send(t, server, http.MethodGet, tc.path, tc.host, nil, "")
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s for host %s = %d, want %d", tc.path, tc.host, resp.StatusCode, tc.want)
		}
	}
}

// An entry with a port allows that port only.
func TestAllowedHostsWithAPort(t *testing.T) {
	server := guardedRouter(t, []string{"threavia.lan:8443"})

	if resp := send(t, server, http.MethodGet, "/api/v1/not-a-route", "threavia.lan:8443", nil, ""); resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("the listed host and port = %d, want 501", resp.StatusCode)
	}
	if resp := send(t, server, http.MethodGet, "/api/v1/not-a-route", "threavia.lan:80", nil, ""); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("the listed host on another port = %d, want 421", resp.StatusCode)
	}
}

var securityHeaders = map[string]string{
	"X-Content-Type-Options":  "nosniff",
	"X-Frame-Options":         "DENY",
	"Content-Security-Policy": "frame-ancestors 'none'",
	"Referrer-Policy":         "strict-origin-when-cross-origin",
}

func assertSecurityHeaders(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	for name, want := range securityHeaders {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("%s: %s = %q, want %q", what, name, got, want)
		}
	}
}

// The web client cannot be framed, and no answer is sniffed into something it
// is not, whichever part of Core sent it and whether it succeeded or not.
func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	server := guardedRouter(t, config.LocalHosts)

	assertSecurityHeaders(t, "GET /", send(t, server, http.MethodGet, "/", "", nil, ""))
	assertSecurityHeaders(t, "GET /api/v1/auth", send(t, server, http.MethodGet, "/api/v1/auth", "", nil, ""))
	assertSecurityHeaders(t, "a refused host", send(t, server, http.MethodGet, "/", "attacker.example", nil, ""))
	assertSecurityHeaders(t, "a refused origin", send(t, server, http.MethodPost, "/api/v1/not-a-route", "", map[string]string{"Sec-Fetch-Site": "cross-site"}, ""))
}

// What the web client sends is still handled: a JSON body with a charset, a
// body-less request, and a same-origin fetch. Needs the database, because these
// get as far as the service.
func TestTheWebClientsRequestsStillWork(t *testing.T) {
	c := newCore(t)

	resp := send(t, c.http, http.MethodGet, "/api/v1/projects", "", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/projects = %d, want 200", resp.StatusCode)
	}
	assertSecurityHeaders(t, "GET /api/v1/projects", resp)

	resp = send(t, c.http, http.MethodPost, "/api/v1/projects", "", map[string]string{
		"Content-Type":   "application/json; charset=utf-8",
		"Sec-Fetch-Site": "same-origin",
		"Origin":         c.http.URL,
	}, `{"name":"Threavia"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("a same-origin POST of JSON = %d, want 201", resp.StatusCode)
	}

	// A route whose body is optional takes no body and no Content-Type: the
	// service then refuses the empty claim, but not for its media type.
	resp = send(t, c.http, http.MethodPost, "/api/v1/backends/claim", "", nil, "")
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Errorf("a body-less POST = 415, want it handled")
	}
}
