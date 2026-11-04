package api_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const oauthRedirect = "https://chatgpt.com/connector_platform_oauth_redirect"

// oauthClient is what an MCP client such as ChatGPT does by itself, without
// the person: discover, register and exchange.
type oauthClient struct {
	t        *testing.T
	c        *core
	http     *http.Client
	id       string
	verifier string
}

func newOAuthClient(t *testing.T, c *core) *oauthClient {
	t.Helper()
	client := &oauthClient{t: t, c: c, verifier: strings.Repeat("v", 50),
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	client.json(http.MethodPost, "/oauth/register", `{"client_name":"ChatGPT","redirect_uris":["`+oauthRedirect+`"],"token_endpoint_auth_method":"none"}`, http.StatusCreated, &registered)
	client.id = registered.ClientID
	return client
}

func (o *oauthClient) json(method, path, body string, want int, out any) {
	o.t.Helper()
	request, _ := http.NewRequest(method, o.c.http.URL+path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := o.http.Do(request)
	if err != nil {
		o.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		o.t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			o.t.Fatal(err)
		}
	}
}

func (o *oauthClient) challenge() string {
	sum := sha256.Sum256([]byte(o.verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (o *oauthClient) token(form url.Values, want int) map[string]any {
	o.t.Helper()
	form.Set("client_id", o.id)
	response, err := o.http.PostForm(o.c.http.URL+"/oauth/token", form)
	if err != nil {
		o.t.Fatal(err)
	}
	defer response.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(response.Body).Decode(&out)
	if response.StatusCode != want {
		o.t.Fatalf("token = %d, want %d: %v", response.StatusCode, want, out)
	}
	return out
}

// consent is what the Web consent page sends for the signed-in person.
func (o *oauthClient) consent(scopes []string, approved bool) url.Values {
	o.t.Helper()
	var answer struct {
		RedirectTo string `json:"redirectTo"`
	}
	o.c.mustDo(http.MethodPost, "/api/v1/oauth/authorize", map[string]any{
		"clientId": o.id, "redirectUri": oauthRedirect, "state": "xyz", "codeChallenge": o.challenge(),
		"codeChallengeMethod": "S256", "resource": o.c.http.URL + "/mcp", "scopes": scopes, "approved": approved,
	}, &answer, http.StatusOK)
	back, err := url.Parse(answer.RedirectTo)
	if err != nil || !strings.HasPrefix(answer.RedirectTo, oauthRedirect+"?") {
		o.t.Fatalf("redirect = %q, want the registered redirect_uri", answer.RedirectTo)
	}
	return back.Query()
}

func TestOAuthDiscoveryLeadsAnMCPClientToSignIn(t *testing.T) {
	c := newCore(t)
	o := &oauthClient{t: t, c: c, http: http.DefaultClient}

	response, err := http.Post(c.http.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if challenge := response.Header.Get("WWW-Authenticate"); response.StatusCode != 401 || !strings.Contains(challenge, `resource_metadata="`+c.http.URL+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("401 challenge = %q, want the resource metadata", challenge)
	}
	var resource struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	o.json(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", 200, &resource)
	if resource.Resource != c.http.URL+"/mcp" || len(resource.AuthorizationServers) != 1 || resource.AuthorizationServers[0] != c.http.URL {
		t.Fatalf("resource metadata = %+v", resource)
	}
	var server map[string]any
	o.json(http.MethodGet, "/.well-known/oauth-authorization-server", "", 200, &server)
	for key, want := range map[string]string{"issuer": c.http.URL, "authorization_endpoint": c.http.URL + "/oauth/authorize", "token_endpoint": c.http.URL + "/oauth/token", "registration_endpoint": c.http.URL + "/oauth/register"} {
		if server[key] != want {
			t.Errorf("%s = %v, want %s", key, server[key], want)
		}
	}
}

func TestOAuthConsentGrantsAControlAccessUntilRevoked(t *testing.T) {
	c := newCore(t)
	o := newOAuthClient(t, c)

	// The authorization endpoint hands a valid request to the consent page.
	query := url.Values{"response_type": {"code"}, "client_id": {o.id}, "redirect_uri": {oauthRedirect}, "state": {"xyz"},
		"code_challenge": {o.challenge()}, "code_challenge_method": {"S256"}, "scope": {"read manage approve"}, "resource": {c.http.URL + "/mcp"}}
	response, err := o.http.Get(c.http.URL + "/oauth/authorize?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if location := response.Header.Get("Location"); response.StatusCode != http.StatusFound || !strings.HasPrefix(location, "/oauth/consent?") {
		t.Fatalf("authorize = %d to %q, want the consent page", response.StatusCode, location)
	}
	// Never a redirect to a URI the client did not register.
	query.Set("redirect_uri", "https://attacker.example/cb")
	response, _ = o.http.Get(c.http.URL + "/oauth/authorize?" + query.Encode())
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unregistered redirect = %d, want 400", response.StatusCode)
	}

	// Refusing sends the client back with access_denied and no code.
	if back := o.consent([]string{"read"}, false); back.Get("error") != "access_denied" || back.Get("code") != "" || back.Get("state") != "xyz" {
		t.Fatalf("refusal = %v", back)
	}

	// The person grants less than asked: read only.
	back := o.consent([]string{"read"}, true)
	code := back.Get("code")
	if code == "" || back.Get("state") != "xyz" || back.Get("iss") != c.http.URL {
		t.Fatalf("approval = %v", back)
	}
	// A wrong PKCE verifier is refused, on a code of its own since codes are
	// single use.
	wrong := o.consent([]string{"read"}, true).Get("code")
	o.token(url.Values{"grant_type": {"authorization_code"}, "code": {wrong}, "redirect_uri": {oauthRedirect}, "code_verifier": {strings.Repeat("w", 50)}}, 400)

	tokens := o.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthRedirect}, "code_verifier": {o.verifier}, "resource": {c.http.URL + "/mcp"}}, 200)
	if tokens["scope"] != "read" || tokens["token_type"] != "Bearer" {
		t.Fatalf("tokens = %v", tokens)
	}
	// A code works once.
	o.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthRedirect}, "code_verifier": {o.verifier}}, 400)

	// The access token opens /mcp with the granted scope only.
	conn := connectControl(t, c, tokens["access_token"].(string))
	info := controlCall(t, conn, "instance_info", map[string]any{})
	if scopes, _ := info["scopes"].([]any); len(scopes) != 1 || scopes[0] != "read" {
		t.Fatalf("scopes = %v, want read only", info["scopes"])
	}
	controlRefused(t, conn, "project_create", map[string]any{"name": "not allowed"})

	// Settings lists the grant under the client's name.
	var accesses struct {
		Items []domain.ControlAccess `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/control-accesses", nil, &accesses, 200)
	if len(accesses.Items) != 1 || accesses.Items[0].OAuthClient != "ChatGPT" || accesses.Items[0].Label != "ChatGPT" {
		t.Fatalf("accesses = %+v", accesses.Items)
	}

	// A refresh rotates both tokens; the old refresh token works once.
	refreshed := o.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}}, 200)
	if refreshed["access_token"] == tokens["access_token"] || refreshed["refresh_token"] == tokens["refresh_token"] {
		t.Fatal("refresh did not rotate the tokens")
	}
	o.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}}, 400)

	// Revoking in Settings ends the connection: no call, no refresh.
	c.mustDo(http.MethodDelete, "/api/v1/control-accesses/"+accesses.Items[0].ID, nil, nil, http.StatusNoContent)
	request, _ := http.NewRequest(http.MethodPost, c.http.URL+"/mcp", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+refreshed["access_token"].(string))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked access token = %d, want 401", response.StatusCode)
	}
	o.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshed["refresh_token"].(string)}}, 400)
}

func TestOAuthRegistrationRefusesUnsafeRedirects(t *testing.T) {
	c := newCore(t)
	o := &oauthClient{t: t, c: c, http: http.DefaultClient}
	for _, uri := range []string{"http://example.com/cb", "javascript:alert(1)", "https://example.com/cb#frag", "/relative"} {
		o.json(http.MethodPost, "/oauth/register", `{"redirect_uris":["`+uri+`"]}`, http.StatusBadRequest, nil)
	}
	o.json(http.MethodPost, "/oauth/register", `{"redirect_uris":["https://x.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`, http.StatusBadRequest, nil)
	o.json(http.MethodPost, "/oauth/register", `{"redirect_uris":["http://127.0.0.1:33418/cb"]}`, http.StatusCreated, nil)
}
