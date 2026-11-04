package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

// consentPath is the Web client page where the person approves a client.
const consentPath = "/oauth/consent"

// registerControlOAuth makes Core the OAuth 2.1 authorization server of its
// own MCP endpoint, as MCP clients such as ChatGPT expect: they discover it
// from a 401 (RFC 9728), register themselves (RFC 7591), send the person to
// a consent page and exchange the code with PKCE. The grant is a control
// access, so Settings lists and revokes it like a token made there.
func (h *handler) registerControlOAuth(mux *http.ServeMux) {
	resource := func(w http.ResponseWriter, r *http.Request) {
		base := h.publicURL(r)
		openCORS(w)
		w.Header().Set("Cache-Control", "max-age=300")
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 base + "/mcp",
			"resource_name":            "Threavia",
			"authorization_servers":    []string{base},
			"scopes_supported":         domain.ControlScopes,
			"bearer_methods_supported": []string{"header"},
		})
	}
	h.open(mux, "GET /.well-known/oauth-protected-resource", resource)
	h.open(mux, "GET /.well-known/oauth-protected-resource/mcp", resource)

	h.open(mux, "GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := h.publicURL(r)
		openCORS(w)
		w.Header().Set("Cache-Control", "max-age=300")
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/oauth/authorize",
			"token_endpoint":                        base + "/oauth/token",
			"registration_endpoint":                 base + "/oauth/register",
			"scopes_supported":                      domain.ControlScopes,
			"response_types_supported":              []string{"code"},
			"response_modes_supported":              []string{"query"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"token_endpoint_auth_methods_supported": []string{"none"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})

	h.open(mux, "OPTIONS /oauth/register", preflight)
	h.open(mux, "POST /oauth/register", func(w http.ResponseWriter, r *http.Request) {
		openCORS(w)
		var in struct {
			ClientName   string   `json:"client_name"`
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
			GrantTypes   []string `json:"grant_types"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			oauthFail(w, &service.OAuthError{Code: "invalid_client_metadata", Description: "the body must be a JSON client registration"})
			return
		}
		// Only public clients: a secret Core handed out would be one more
		// credential to store, and PKCE already binds the code to its client.
		if in.AuthMethod != "" && in.AuthMethod != "none" {
			oauthFail(w, &service.OAuthError{Code: "invalid_client_metadata", Description: "only token_endpoint_auth_method none is supported"})
			return
		}
		for _, grant := range in.GrantTypes {
			if grant != "authorization_code" && grant != "refresh_token" {
				oauthFail(w, &service.OAuthError{Code: "invalid_client_metadata", Description: "unsupported grant type " + grant})
				return
			}
		}
		client, err := h.svc.RegisterOAuthClient(r.Context(), in.ClientName, in.RedirectURIs)
		if err != nil {
			oauthFail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{
			"client_id":                  client.ID,
			"client_id_issued_at":        client.CreatedAt.Unix(),
			"client_name":                client.Name,
			"redirect_uris":              client.RedirectURIs,
			"token_endpoint_auth_method": "none",
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
		})
	})

	// The authorization endpoint only checks the request, then hands it to
	// the Web client, which signs the person in as it always does and asks
	// for consent. Core has no login page of its own to show here.
	h.open(mux, "GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		in := authorizationFrom(q)
		if q.Get("response_type") != "code" {
			in.CodeChallengeMethod = "invalid"
		}
		_, err := h.svc.CheckOAuthAuthorization(r.Context(), in)
		var oauth *service.OAuthError
		switch {
		case errors.As(err, &oauth):
			redirectOAuth(w, r, in.RedirectURI, url.Values{"error": {oauth.Code}, "error_description": {oauth.Description}, "state": {q.Get("state")}})
			return
		case err != nil:
			// Never send anyone to a redirect URI the client did not register.
			writeError(w, http.StatusBadRequest, "invalid_request", strings.TrimPrefix(err.Error(), "invalid: "))
			return
		}
		if !h.validResource(r, in.Resource) {
			redirectOAuth(w, r, in.RedirectURI, url.Values{"error": {"invalid_target"}, "error_description": {"resource must be this Core's /mcp endpoint"}, "state": {q.Get("state")}})
			return
		}
		http.Redirect(w, r, consentPath+"?"+q.Encode(), http.StatusFound)
	})

	h.open(mux, "OPTIONS /oauth/token", preflight)
	h.open(mux, "POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		openCORS(w)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			oauthFail(w, &service.OAuthError{Code: "invalid_request", Description: "the body must be form encoded"})
			return
		}
		form := r.PostForm
		if form.Get("client_secret") != "" {
			oauthFail(w, &service.OAuthError{Code: "invalid_client", Description: "clients are public and have no secret"})
			return
		}
		var tokens service.OAuthTokens
		var err error
		switch form.Get("grant_type") {
		case "authorization_code":
			if !h.validResource(r, form.Get("resource")) {
				oauthFail(w, &service.OAuthError{Code: "invalid_target", Description: "resource must be this Core's /mcp endpoint"})
				return
			}
			tokens, err = h.svc.ExchangeOAuthCode(r.Context(), form.Get("code"), form.Get("client_id"), form.Get("redirect_uri"), form.Get("code_verifier"), form.Get("resource"))
			if err == nil {
				go h.purgeOAuth()
			}
		case "refresh_token":
			tokens, err = h.svc.RefreshOAuth(r.Context(), form.Get("refresh_token"), form.Get("client_id"))
		default:
			err = &service.OAuthError{Code: "unsupported_grant_type", Description: "use authorization_code or refresh_token"}
		}
		if err != nil {
			oauthFail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token":  tokens.AccessToken,
			"token_type":    tokens.TokenType,
			"expires_in":    tokens.ExpiresIn,
			"refresh_token": tokens.RefreshToken,
			"scope":         strings.Join(tokens.Scopes, " "),
		})
	})

	// What the consent page reads and sends, as the signed-in person.
	h.handle(mux, "GET /api/v1/oauth/clients/{clientId}", func(w http.ResponseWriter, r *http.Request, _ auth.Identity) {
		client, err := h.svc.OAuthClient(r.Context(), r.PathValue("clientId"))
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, client)
	})
	h.handle(mux, "POST /api/v1/oauth/authorize", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		in, ok := decode[struct {
			ClientID            string   `json:"clientId"`
			RedirectURI         string   `json:"redirectUri"`
			State               string   `json:"state"`
			CodeChallenge       string   `json:"codeChallenge"`
			CodeChallengeMethod string   `json:"codeChallengeMethod"`
			Resource            string   `json:"resource"`
			Scopes              []string `json:"scopes"`
			Approved            bool     `json:"approved"`
		}](w, r)
		if !ok {
			return
		}
		request := service.OAuthAuthorization{ClientID: in.ClientID, RedirectURI: in.RedirectURI, CodeChallenge: in.CodeChallenge, CodeChallengeMethod: in.CodeChallengeMethod, Scopes: in.Scopes, Resource: in.Resource}
		if _, err := h.svc.CheckOAuthAuthorization(r.Context(), request); err != nil {
			h.fail(w, oauthAsInvalid(err))
			return
		}
		if !h.validResource(r, in.Resource) {
			writeError(w, http.StatusBadRequest, "invalid", "resource must be this Core's /mcp endpoint")
			return
		}
		back := url.Values{}
		if in.State != "" {
			back.Set("state", in.State)
		}
		if !in.Approved {
			back.Set("error", "access_denied")
			back.Set("error_description", "the person refused the connection")
			writeJSON(w, http.StatusOK, map[string]string{"redirectTo": withQuery(in.RedirectURI, back)})
			return
		}
		code, err := h.svc.AuthorizeOAuth(r.Context(), i, request)
		if err != nil {
			h.fail(w, err)
			return
		}
		back.Set("code", code)
		back.Set("iss", h.publicURL(r))
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"redirectTo": withQuery(in.RedirectURI, back)})
	})
}

// publicURL is the address clients reach Core at: the configured one, or the
// one this request came through, behind a proxy that says it terminated TLS.
func (h *handler) publicURL(r *http.Request) string {
	if h.publicBase != "" {
		return h.publicBase
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// validResource accepts no resource, or this Core's MCP endpoint (RFC 8707):
// a token is never minted for another server's audience.
func (h *handler) validResource(r *http.Request, resource string) bool {
	return resource == "" || strings.TrimSuffix(resource, "/") == h.publicURL(r)+"/mcp"
}

func (h *handler) purgeOAuth() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.svc.PurgeOAuth(ctx); err != nil {
		h.logger.Warn("cannot purge expired OAuth codes and tokens", "error", err)
	}
}

func authorizationFrom(q url.Values) service.OAuthAuthorization {
	return service.OAuthAuthorization{
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		Scopes:              strings.Fields(q.Get("scope")),
		Resource:            q.Get("resource"),
	}
}

func withQuery(raw string, values url.Values) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for key, list := range values {
		for _, value := range list {
			if value != "" {
				q.Add(key, value)
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func redirectOAuth(w http.ResponseWriter, r *http.Request, redirectURI string, values url.Values) {
	http.Redirect(w, r, withQuery(redirectURI, values), http.StatusFound)
}

// oauthFail answers the registration and token endpoints in their own error
// format (RFC 6749 section 5.2, RFC 7591 section 3.2.2).
func oauthFail(w http.ResponseWriter, err error) {
	var oauth *service.OAuthError
	if !errors.As(err, &oauth) {
		oauth = &service.OAuthError{Code: "server_error", Description: "the request could not be completed"}
		if errors.Is(err, service.ErrInvalid) {
			oauth = &service.OAuthError{Code: "invalid_request", Description: strings.TrimPrefix(err.Error(), "invalid: ")}
		}
	}
	status := http.StatusBadRequest
	switch oauth.Code {
	case "invalid_client":
		status = http.StatusUnauthorized
	case "server_error":
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]string{"error": oauth.Code, "error_description": oauth.Description})
}

func oauthAsInvalid(err error) error {
	var oauth *service.OAuthError
	if errors.As(err, &oauth) {
		return errors.Join(service.ErrInvalid, errors.New(oauth.Description))
	}
	return err
}

// openCORS lets browser-based MCP clients read the discovery documents and
// call the token endpoint. Nothing here relies on cookies.
func openCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	openCORS(w)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}
