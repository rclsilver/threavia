// Package mcpmock provides a loopback-only, disposable OAuth/MCP recipe server.
// All identities and data are fictional. This is not an identity provider for production.
package mcpmock

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type authorization struct {
	Client, Redirect, State, Challenge, Resource string
	Expires                                      time.Time
}
type credential struct {
	Client, Resource string
	Expires          time.Time
}
type Stats struct {
	Reads         int `json:"reads"`
	Creates       int `json:"creates"`
	Deletes       int `json:"deletes"`
	SlowStarted   int `json:"slowStarted"`
	SlowCompleted int `json:"slowCompleted"`
	SlowCancelled int `json:"slowCancelled"`
	Refreshes     int `json:"refreshes"`
}
type Server struct {
	base    string
	mu      sync.Mutex
	clients map[string][]string
	pending map[string]authorization
	codes   map[string]authorization
	access  map[string]credential
	refresh map[string]credential
	notes   map[string]string
	stats   Stats
	mcp     http.Handler
}

func New(base string) (*Server, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || !loopback(u.Hostname()) || u.RawQuery != "" || u.User != nil || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("mock base URL must be HTTP on loopback, without a path")
	}
	s := &Server{base: strings.TrimRight(base, "/"), clients: map[string][]string{}, pending: map[string]authorization{}, codes: map[string]authorization{}, access: map[string]credential{}, refresh: map[string]credential{}, notes: map[string]string{}}
	m := sdk.NewServer(&sdk.Implementation{Name: "threavia-oauth-mock", Version: "1"}, nil)
	empty := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	m.AddTool(&sdk.Tool{Name: "mock_read", Description: "Read a greeting from the fictional OAuth account. No real service is connected.", InputSchema: empty}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		s.mu.Lock()
		s.stats.Reads++
		s.mu.Unlock()
		return result(map[string]any{"account": "mock-user", "message": "OAuth MCP mock connected"}), nil
	})
	m.AddTool(&sdk.Tool{Name: "mock_list_notes", Description: "List only the temporary notes stored in this mock.", InputSchema: empty}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		notes := map[string]string{}
		for k, v := range s.notes {
			notes[k] = v
		}
		return result(map[string]any{"notes": notes}), nil
	})
	m.AddTool(&sdk.Tool{Name: "mock_create_note", Description: "Create a fictional note in memory. This is a write requiring human validation in Threavia.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}}, "required": []string{"text"}, "additionalProperties": false}}, func(_ context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var in struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(r.Params.Arguments, &in) != nil || len(in.Text) == 0 || len(in.Text) > 256 {
			return nil, fmt.Errorf("text must contain 1 to 256 bytes")
		}
		id := random()
		s.mu.Lock()
		s.notes[id] = in.Text
		s.stats.Creates++
		s.mu.Unlock()
		return result(map[string]any{"id": id, "text": in.Text}), nil
	})
	m.AddTool(&sdk.Tool{Name: "mock_delete_note", Description: "Delete one fictional mock note by its exact ID. Never touches real data.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}, "additionalProperties": false}}, func(_ context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var in struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(r.Params.Arguments, &in) != nil {
			return nil, fmt.Errorf("invalid id")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.notes[in.ID]; !ok {
			return nil, fmt.Errorf("mock note not found")
		}
		delete(s.notes, in.ID)
		s.stats.Deletes++
		return result(map[string]any{"deleted": in.ID}), nil
	})
	m.AddTool(&sdk.Tool{Name: "mock_slow_read", Description: "Wait 30 seconds before returning fictional data. Use Stop while this tool is running to test cancellation. It never writes data.", InputSchema: empty}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		s.mu.Lock()
		s.stats.SlowStarted++
		s.mu.Unlock()
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.stats.SlowCancelled++
			s.mu.Unlock()
			return nil, ctx.Err()
		case <-timer.C:
			s.mu.Lock()
			s.stats.SlowCompleted++
			s.mu.Unlock()
			return result(map[string]any{"message": "slow mock read completed"}), nil
		}
	})
	s.mcp = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return m }, nil)
	return s, nil
}
func loopback(host string) bool {
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func result(v any) *sdk.CallToolResult {
	b, _ := json.Marshal(v)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code})
}
func (s *Server) Snapshot() Stats { s.mu.Lock(); defer s.mu.Unlock(); return s.stats }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/healthz":
		writeJSON(w, 200, map[string]any{"status": "ready", "mock": true})
	case "/stats":
		writeJSON(w, 200, s.Snapshot())
	case "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource":
		writeJSON(w, 200, map[string]any{"resource": s.base + "/mcp", "authorization_servers": []string{s.base}, "scopes_supported": []string{"mock"}})
	case "/.well-known/oauth-authorization-server":
		writeJSON(w, 200, map[string]any{"issuer": s.base, "authorization_endpoint": s.base + "/authorize", "token_endpoint": s.base + "/token", "registration_endpoint": s.base + "/register", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true})
	case "/register":
		s.register(w, r)
	case "/authorize":
		s.authorize(w, r)
	case "/consent":
		s.consent(w, r)
	case "/token":
		s.token(w, r)
	case "/mcp":
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		g, found := s.access[token]
		s.mu.Unlock()
		if !ok || !found || time.Now().After(g.Expires) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=%q", s.base+"/.well-known/oauth-protected-resource/mcp"))
			fail(w, 401, "invalid_token")
			return
		}
		s.mcp.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed")
		return
	}
	var in struct {
		Redirects []string `json:"redirect_uris"`
		Method    string   `json:"token_endpoint_auth_method"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil || len(in.Redirects) == 0 || len(in.Redirects) > 4 || in.Method != "none" {
		fail(w, 400, "invalid_client_metadata")
		return
	}
	for _, raw := range in.Redirects {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "http" || !loopback(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			fail(w, 400, "invalid_redirect_uri")
			return
		}
	}
	id := random()
	s.mu.Lock()
	s.clients[id] = in.Redirects
	s.mu.Unlock()
	writeJSON(w, 201, map[string]any{"client_id": id, "redirect_uris": in.Redirects, "token_endpoint_auth_method": "none"})
}
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, "method_not_allowed")
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	redirects := s.clients[q.Get("client_id")]
	s.mu.Unlock()
	valid := false
	for _, v := range redirects {
		if v == q.Get("redirect_uri") {
			valid = true
		}
	}
	challenge, e := base64.RawURLEncoding.DecodeString(q.Get("code_challenge"))
	if !valid || q.Get("response_type") != "code" || q.Get("resource") != s.base+"/mcp" || q.Get("state") == "" || q.Get("code_challenge_method") != "S256" || e != nil || len(challenge) != 32 {
		fail(w, 400, "invalid_authorization_request")
		return
	}
	flow := random()
	s.mu.Lock()
	s.pending[flow] = authorization{Client: q.Get("client_id"), Redirect: q.Get("redirect_uri"), State: q.Get("state"), Challenge: q.Get("code_challenge"), Resource: q.Get("resource"), Expires: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentPage.Execute(w, map[string]string{"Flow": flow})
}

var consentPage = template.Must(template.New("consent").Parse(`<!doctype html><html lang="fr"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>OAuth fictif — Threavia</title><style>body{font:18px system-ui;max-width:600px;margin:8vh auto;padding:24px;background:#f5f6fa;color:#18202c}main{background:white;border:1px solid #d5dbe4;border-radius:16px;padding:28px}button{font:inherit;padding:12px 18px;margin:8px 8px 0 0;cursor:pointer}small{display:block;margin-top:24px;color:#526070}</style><main><h1>Compte OAuth fictif</h1><p>Threavia demande l'accès au compte <strong>mock-user</strong>.</p><p>Ce bac à sable contient uniquement des notes temporaires. Aucun compte ni service réel n'est connecté.</p><form action="/consent" method="post"><input type="hidden" name="flow" value="{{.Flow}}"><button name="decision" value="deny">Refuser la connexion</button><button name="decision" value="allow">Autoriser le test</button></form><small>Serveur local de recette. Les données et connexions sont effacées au redémarrage du mock.</small></main></html>`))

func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_request")
		return
	}
	s.mu.Lock()
	a, ok := s.pending[r.Form.Get("flow")]
	delete(s.pending, r.Form.Get("flow"))
	s.mu.Unlock()
	if !ok || time.Now().After(a.Expires) {
		fail(w, 400, "expired_consent")
		return
	}
	u, _ := url.Parse(a.Redirect)
	q := u.Query()
	q.Set("state", a.State)
	q.Set("iss", s.base)
	if r.Form.Get("decision") == "allow" {
		code := random()
		a.Expires = time.Now().Add(time.Minute)
		s.mu.Lock()
		s.codes[code] = a
		s.mu.Unlock()
		q.Set("code", code)
	} else {
		q.Set("error", "access_denied")
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	client, resource := r.Form.Get("client_id"), r.Form.Get("resource")
	if _, ok := s.clients[client]; !ok || resource != s.base+"/mcp" {
		fail(w, 400, "invalid_client_or_resource")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		a, ok := s.codes[r.Form.Get("code")]
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || time.Now().After(a.Expires) || a.Client != client || a.Redirect != r.Form.Get("redirect_uri") || a.Resource != resource || a.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
			fail(w, 400, "invalid_grant")
			return
		}
		delete(s.codes, r.Form.Get("code"))
	case "refresh_token":
		old, ok := s.refresh[r.Form.Get("refresh_token")]
		if !ok || old.Client != client || old.Resource != resource || time.Now().After(old.Expires) {
			fail(w, 400, "invalid_grant")
			return
		}
		delete(s.refresh, r.Form.Get("refresh_token"))
		s.stats.Refreshes++
	default:
		fail(w, 400, "unsupported_grant_type")
		return
	}
	access, refresh := random(), random()
	s.access[access] = credential{Client: client, Resource: resource, Expires: time.Now().Add(time.Minute)}
	s.refresh[refresh] = credential{Client: client, Resource: resource, Expires: time.Now().Add(24 * time.Hour)}
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 60, "scope": "mock"})
}
