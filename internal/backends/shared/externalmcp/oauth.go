package externalmcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"golang.org/x/oauth2"
)

type grant struct {
	Generation string        `json:"generation"`
	URL        string        `json:"url"`
	Issuer     string        `json:"issuer"`
	Config     oauth2.Config `json:"config"`
	Token      *oauth2.Token `json:"token"`
}
type authFlow struct {
	server   mcpconfig.Server
	issuer   string
	config   oauth2.Config
	verifier string
	expiry   time.Time
}

var authMu sync.Mutex
var flows = map[string]authFlow{}
var credentialMu sync.Mutex

func random() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func tokenPath(root, id string) string { // IDs are generated UUIDs; never accept a path segment from imported files.
	return filepath.Join(root, "mcp-tokens", hex.EncodeToString([]byte(id))+".json")
}
func writeGrant(root string, v mcpconfig.Server, g grant) error {
	credentialMu.Lock()
	defer credentialMu.Unlock()
	dir := filepath.Dir(tokenPath(root, v.ID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "grant-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, tokenPath(root, v.ID))
}

type savedSource struct {
	generation string
	ctx        context.Context
	root       string
	server     mcpconfig.Server
}

var refreshLocks sync.Map

func (s *savedSource) Token() (*oauth2.Token, error) {
	lock, _ := refreshLocks.LoadOrStore(tokenPath(s.root, s.server.ID), &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	raw, err := os.ReadFile(tokenPath(s.root, s.server.ID))
	var g grant
	if err != nil || json.Unmarshal(raw, &g) != nil || g.Token == nil {
		return nil, fmt.Errorf("MCP authentication required on this backend")
	}
	if g.URL != s.server.URL || g.Generation != s.generation || s.server.OAuthClientID != "" && g.Config.ClientID != s.server.OAuthClientID {
		return nil, fmt.Errorf("MCP account credentials changed; start a new Job to use the new connection")
	}
	refreshHTTP := *authHTTP
	refreshHTTP.Transport = resourceTokenTransport{resource: s.server.URL, base: http.DefaultTransport}
	t, e := g.Config.TokenSource(context.WithValue(s.ctx, oauth2.HTTPClient, &refreshHTTP), g.Token).Token()
	if e != nil {
		return nil, fmt.Errorf("MCP authentication expired; reconnect this server")
	}
	if t.AccessToken != g.Token.AccessToken || t.RefreshToken != g.Token.RefreshToken {
		g.Token = t
		if e := writeGrant(s.root, s.server, g); e != nil {
			return nil, fmt.Errorf("cannot save refreshed MCP credentials")
		}
	}
	return t, nil
}

// resourceTokenTransport binds refresh requests to the same MCP resource as login.
// This client is used only by the public-client token source, never by MCP calls.
type resourceTokenTransport struct {
	resource string
	base     http.RoundTripper
}

func (t resourceTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("OAuth refresh body missing")
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("cannot read OAuth refresh request")
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil || form.Get("grant_type") != "refresh_token" {
		return nil, fmt.Errorf("invalid OAuth refresh request")
	}
	form.Set("resource", t.resource)
	encoded := form.Encode()
	r := req.Clone(req.Context())
	r.Body = io.NopCloser(strings.NewReader(encoded))
	r.ContentLength = int64(len(encoded))
	r.GetBody = nil
	return t.base.RoundTrip(r)
}

func tokenTransport(ctx context.Context, root string, v mcpconfig.Server) (http.RoundTripper, error) {
	raw, err := os.ReadFile(tokenPath(root, v.ID))
	if err != nil {
		return nil, fmt.Errorf("authentication required: connect this MCP server on this backend")
	}
	var g grant
	if json.Unmarshal(raw, &g) != nil || g.URL != v.URL || g.Token == nil {
		return nil, fmt.Errorf("authentication required for this server URL")
	}
	return &oauth2.Transport{Source: &savedSource{ctx: ctx, root: root, server: v, generation: g.Generation}}, nil
}
func secureURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.User == nil && u.Host != "" && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")))
}

var authHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

func authJSON(ctx context.Context, method, endpoint string, body any, out any) error {
	if !secureURL(endpoint) {
		return fmt.Errorf("OAuth endpoint requires HTTPS")
	}
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := authHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("OAuth discovery request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("OAuth endpoint returned HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
func StartAuth(ctx context.Context, v mcpconfig.Server, redirect string) (map[string]any, error) {
	if !v.OAuth || v.Transport != "http" || !secureURL(redirect) {
		return nil, fmt.Errorf("OAuth requires an HTTP server and an HTTPS client callback")
	}
	u, _ := url.Parse(v.URL)
	resource := *u
	resource.Path = "/.well-known/oauth-protected-resource" + strings.TrimRight(u.Path, "/")
	var meta struct {
		Resource string   `json:"resource"`
		Issuers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	if err := authJSON(ctx, "GET", resource.String(), nil, &meta); err != nil {
		resource.Path = "/.well-known/oauth-protected-resource"
		if err = authJSON(ctx, "GET", resource.String(), nil, &meta); err != nil {
			return nil, err
		}
	}
	if meta.Resource != v.URL || len(meta.Issuers) == 0 {
		return nil, fmt.Errorf("resource metadata must match the MCP URL and advertise an issuer")
	}
	issuer := meta.Issuers[0]
	if !secureURL(issuer) {
		return nil, fmt.Errorf("invalid OAuth issuer")
	}
	iu, _ := url.Parse(issuer)
	endpoint := *iu
	endpoint.Path = "/.well-known/oauth-authorization-server" + strings.TrimRight(iu.Path, "/")
	var a struct {
		Issuer        string   `json:"issuer"`
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		Registration  string   `json:"registration_endpoint"`
		Challenges    []string `json:"code_challenge_methods_supported"`
	}
	if err := authJSON(ctx, "GET", endpoint.String(), nil, &a); err != nil {
		return nil, err
	}
	if a.Issuer != issuer || !secureURL(a.Authorization) || !secureURL(a.Token) {
		return nil, fmt.Errorf("authorization metadata issuer or endpoints are invalid")
	}
	supported := false
	for _, m := range a.Challenges {
		if m == "S256" {
			supported = true
		}
	}
	if !supported {
		return nil, fmt.Errorf("OAuth server must support PKCE S256")
	}
	clientID := v.OAuthClientID
	if clientID == "" {
		var registration struct {
			ID     string `json:"client_id"`
			Secret string `json:"client_secret"`
		}
		if a.Registration == "" {
			return nil, fmt.Errorf("configure the provider’s registered OAuth client ID")
		}
		if err := authJSON(ctx, "POST", a.Registration, map[string]any{"client_name": "Threavia", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"}, &registration); err != nil {
			return nil, err
		}
		if registration.ID == "" || registration.Secret != "" {
			return nil, fmt.Errorf("provider must support a public OAuth client")
		}
		clientID = registration.ID
	}
	config := oauth2.Config{ClientID: clientID, RedirectURL: redirect, Scopes: meta.Scopes, Endpoint: oauth2.Endpoint{AuthURL: a.Authorization, TokenURL: a.Token, AuthStyle: oauth2.AuthStyleInParams}}
	state := random()
	verifier := oauth2.GenerateVerifier()
	authMu.Lock()
	for key, f := range flows {
		if time.Now().After(f.expiry) {
			delete(flows, key)
		}
	}
	flows[state] = authFlow{server: v, issuer: issuer, config: config, verifier: verifier, expiry: time.Now().Add(10 * time.Minute)}
	authMu.Unlock()
	return map[string]any{"state": state, "authorizationUrl": config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", v.URL)), "expiresIn": 600}, nil
}
func FinishAuth(ctx context.Context, root string, v mcpconfig.Server, state, response string) (map[string]any, error) {
	u, err := url.Parse(response)
	if err != nil {
		return nil, fmt.Errorf("paste the complete callback URL")
	}
	q := u.Query()
	authMu.Lock()
	f, ok := flows[state]
	if ok {
		delete(flows, state)
	}
	authMu.Unlock()
	if !ok || !v.OAuth || time.Now().After(f.expiry) || f.server.ID != v.ID || f.server.URL != v.URL || f.server.OAuthClientID != v.OAuthClientID || q.Get("state") != state || q.Get("code") == "" {
		return nil, fmt.Errorf("authorization response is expired or does not match this connection")
	}
	expected, _ := url.Parse(f.config.RedirectURL)
	if u.Scheme != expected.Scheme || u.Host != expected.Host || u.Path != expected.Path {
		return nil, fmt.Errorf("OAuth callback does not match")
	}
	if iss := q.Get("iss"); iss != "" && iss != f.issuer {
		return nil, fmt.Errorf("OAuth issuer mismatch")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, authHTTP)
	token, err := f.config.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.verifier), oauth2.SetAuthURLParam("resource", v.URL))
	if err != nil {
		return nil, fmt.Errorf("OAuth token exchange failed; start a new connection")
	}
	lock, _ := refreshLocks.LoadOrStore(tokenPath(root, v.ID), &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	if err = writeGrant(root, v, grant{Generation: random(), URL: v.URL, Issuer: f.issuer, Config: f.config, Token: token}); err != nil {
		return nil, fmt.Errorf("could not save credentials on this backend")
	}
	return map[string]any{"state": "AUTHENTICATED"}, nil
}
func Logout(root string, v mcpconfig.Server) error {
	lock, _ := refreshLocks.LoadOrStore(tokenPath(root, v.ID), &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	credentialMu.Lock()
	defer credentialMu.Unlock()
	err := os.Remove(tokenPath(root, v.ID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
