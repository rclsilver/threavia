package externalmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthRemoteCallbackPKCEAndBackendOnlyTokens(t *testing.T) {
	var host *httptest.Server
	challenge := ""
	var exchanges atomic.Int64
	host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": host.URL + "/mcp", "authorization_servers": []string{host.URL}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": host.URL, "authorization_endpoint": host.URL + "/authorize", "token_endpoint": host.URL + "/token", "code_challenge_methods_supported": []string{"S256"}})
		case "/token":
			_ = r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("resource") != host.URL+"/mcp" {
				t.Error("PKCE or resource binding missing")
			}
			exchanges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			if r.Header.Get("Authorization") != "Bearer fixture-access" {
				w.WriteHeader(401)
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
		}
	}))
	defer host.Close()
	root := t.TempDir()
	v := mcpconfig.Server{ID: "fixture", Name: "fixture", Transport: "http", URL: host.URL + "/mcp", OAuth: true, OAuthClientID: "public-client"}
	redirect := "http://127.0.0.1:9999/settings/mcp"
	started, err := StartAuth(context.Background(), v, redirect)
	if err != nil {
		t.Fatal(err)
	}
	authURL, _ := url.Parse(started["authorizationUrl"].(string))
	challenge = authURL.Query().Get("code_challenge")
	state := started["state"].(string)
	response := redirect + "?code=fixture-code&state=" + state + "&iss=" + url.QueryEscape(host.URL)
	result, err := FinishAuth(context.Background(), root, v, state, response)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if string(raw) != `{"state":"AUTHENTICATED"}` {
		t.Fatalf("unexpected public result %s", raw)
	}
	info, err := os.Stat(tokenPath(root, v.ID))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("token permissions %v %v", info, err)
	}
	if _, err = FinishAuth(context.Background(), root, v, state, response); err == nil {
		t.Fatal("OAuth callback replay accepted")
	}
	if exchanges.Load() != 1 {
		t.Fatal("code was exchanged more than once")
	}
	tr, err := tokenTransport(context.Background(), root, v)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", host.URL+"/protected", nil)
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("stored token was not used")
	}
	stored, err := os.ReadFile(tokenPath(root, v.ID))
	if err != nil {
		t.Fatal(err)
	}
	var changed grant
	if json.Unmarshal(stored, &changed) != nil {
		t.Fatal("invalid stored grant")
	}
	changed.Generation = "new-account"
	changed.Token.AccessToken = "different-account-token"
	if err = writeGrant(root, v, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = tr.RoundTrip(req); err == nil {
		t.Fatal("existing connection silently switched OAuth accounts")
	}
	if err := Logout(root, v); err != nil {
		t.Fatal(err)
	}
	if _, err = tokenTransport(context.Background(), root, v); err == nil {
		t.Fatal("logout retained local credential")
	}
}

func TestConcurrentJobsShareRefreshWithoutReusingRotatedToken(t *testing.T) {
	var refreshes atomic.Int64
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer host.Close()
	root := t.TempDir()
	v := mcpconfig.Server{ID: "fixture", Name: "fixture", URL: host.URL, OAuth: true}
	old := &oauth2.Token{AccessToken: "expired", RefreshToken: "old-refresh", Expiry: time.Now().Add(-time.Hour)}
	if err := writeGrant(root, v, grant{URL: v.URL, Token: old, Config: oauth2.Config{ClientID: "public", Endpoint: oauth2.Endpoint{TokenURL: host.URL, AuthStyle: oauth2.AuthStyleInParams}}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			source := &savedSource{ctx: context.Background(), root: root, server: v}
			token, err := source.Token()
			if err != nil || token.AccessToken != "new-access" {
				t.Errorf("refresh=%v err=%v", token, err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refresh count %d, want one", refreshes.Load())
	}
}

func TestRegressionRefreshIncludesResource(t *testing.T) {
	var host *httptest.Server
	host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("resource") != host.URL {
			http.Error(w, "resource required", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"access_token\":\"new\",\"token_type\":\"Bearer\",\"expires_in\":3600}"))
	}))
	defer host.Close()
	root := t.TempDir()
	v := mcpconfig.Server{ID: "review", Name: "review", URL: host.URL, OAuth: true}
	g := grant{URL: v.URL, Token: &oauth2.Token{AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}, Config: oauth2.Config{ClientID: "public", Endpoint: oauth2.Endpoint{TokenURL: host.URL, AuthStyle: oauth2.AuthStyleInParams}}}
	if err := writeGrant(root, v, g); err != nil {
		t.Fatal(err)
	}
	source := &savedSource{ctx: context.Background(), root: root, server: v}
	if _, err := source.Token(); err != nil {
		t.Fatalf("resource-bound refresh failed: %v", err)
	}
}
