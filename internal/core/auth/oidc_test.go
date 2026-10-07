package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// issuer is a minimal OIDC provider: the discovery document and a key set, which
// is all Core needs to verify a token. Tests run against this rather than a
// mocked verifier, so what is pinned is real signature and claim checking.
type issuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the signing key: %v", err)
	}

	iss := &issuer{key: key, keyID: "test-key"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                iss.url(),
			"authorization_endpoint":                iss.url() + "/auth",
			"token_endpoint":                        iss.url() + "/token",
			"jwks_uri":                              iss.url() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{
			Keys: []jose.JSONWebKey{{
				Key:       key.Public(),
				KeyID:     iss.keyID,
				Algorithm: string(jose.RS256),
				Use:       "sig",
			}},
		})
	})

	iss.server = httptest.NewServer(mux)
	t.Cleanup(iss.server.Close)
	return iss
}

func (i *issuer) url() string {
	if i.server == nil {
		return ""
	}
	return i.server.URL
}

// token mints a signed token with the given claims.
func (i *issuer) token(t *testing.T, claims map[string]any, signWith *rsa.PrivateKey) string {
	t.Helper()

	if signWith == nil {
		signWith = i.key
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: signWith},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", i.keyID))
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}

	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("signing the token: %v", err)
	}
	return raw
}

// standardClaims is a token the configured authenticator should accept.
func (i *issuer) standardClaims() map[string]any {
	return map[string]any{
		"iss":                i.url(),
		"aud":                "threavia",
		"sub":                "3f8c1b2e-0000-4000-8000-000000000001",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"email":              "thomas@example.invalid",
		"preferred_username": "thomas",
		"name":               "Thomas",
	}
}

func newOIDC(t *testing.T, iss *issuer, userClaim string) Authenticator {
	t.Helper()

	authenticator, err := New(Config{
		Mode:          ModeOIDC,
		OIDCIssuer:    iss.url(),
		OIDCClientID:  "threavia",
		OIDCUserClaim: userClaim,
	})
	if err != nil {
		t.Fatalf("building the oidc authenticator: %v", err)
	}
	return authenticator
}

func request(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestOIDCAcceptsAValidToken(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	authenticator := newOIDC(t, iss, "")

	identity, err := authenticator.Authenticate(request(iss.token(t, iss.standardClaims(), nil)))
	if err != nil {
		t.Fatalf("authenticating a valid token: %v", err)
	}
	// The subject by default: it is the only claim an issuer guarantees to be
	// stable and unique.
	if string(identity.UserID) != "3f8c1b2e-0000-4000-8000-000000000001" {
		t.Fatalf("userID = %q, want the subject", identity.UserID)
	}
	if identity.Email != "thomas@example.invalid" || identity.Name != "Thomas" {
		t.Fatalf("identity = %+v, want the token claims", identity)
	}
	if authenticator.Mode() != ModeOIDC {
		t.Fatalf("mode = %q, want oidc", authenticator.Mode())
	}
}

// TestOIDCHonoursTheConfiguredUserClaim pins that a deployment can choose a
// readable identifier instead of an opaque subject.
func TestOIDCHonoursTheConfiguredUserClaim(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	authenticator := newOIDC(t, iss, "preferred_username")

	identity, err := authenticator.Authenticate(request(iss.token(t, iss.standardClaims(), nil)))
	if err != nil {
		t.Fatalf("authenticating a valid token: %v", err)
	}
	if string(identity.UserID) != "thomas" {
		t.Fatalf("userID = %q, want the preferred username", identity.UserID)
	}
}

func TestOIDCRefusesBadTokens(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	authenticator := newOIDC(t, iss, "")

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating another key: %v", err)
	}

	expired := iss.standardClaims()
	expired["exp"] = time.Now().Add(-time.Hour).Unix()

	wrongAudience := iss.standardClaims()
	wrongAudience["aud"] = "someone-else"

	wrongIssuer := iss.standardClaims()
	wrongIssuer["iss"] = "https://evil.example.invalid"

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no token", ""},
		{"not a token", "not-a-jwt"},
		{"signed by another key", iss.token(t, iss.standardClaims(), other)},
		{"expired", iss.token(t, expired, nil)},
		{"another audience", iss.token(t, wrongAudience, nil)},
		{"another issuer", iss.token(t, wrongIssuer, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := authenticator.Authenticate(request(tc.token)); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("got %v, want ErrUnauthenticated", err)
			}
		})
	}
}

// TestOIDCRefusesANonBearerScheme pins that only a bearer token is read: basic
// credentials must not be accepted by an issuer-backed deployment.
func TestOIDCRefusesANonBearerScheme(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	authenticator := newOIDC(t, iss, "")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	r.SetBasicAuth("thomas", "hunter2")
	if _, err := authenticator.Authenticate(r); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("got %v, want ErrUnauthenticated", err)
	}
}

// TestOIDCFailsOnAnUnreachableIssuer pins that a misconfiguration is a startup
// error an operator can see, not a surprise on the first request.
func TestOIDCFailsOnAnUnreachableIssuer(t *testing.T) {
	t.Parallel()

	_, err := New(Config{
		Mode:         ModeOIDC,
		OIDCIssuer:   "http://127.0.0.1:1/realms/threavia",
		OIDCClientID: "threavia",
	})
	if !errors.Is(err, ErrOIDCUnavailable) {
		t.Fatalf("got %v, want ErrOIDCUnavailable", err)
	}
}
