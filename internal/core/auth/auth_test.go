package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModeNoneAttributesASingleUser(t *testing.T) {
	t.Parallel()

	authenticator, err := New(Config{Mode: ModeNone, DevUserID: "dev"})
	if err != nil {
		t.Fatalf("building the none authenticator: %v", err)
	}

	identity, err := authenticator.Authenticate(httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if err != nil {
		t.Fatalf("mode none must accept every request: %v", err)
	}
	if identity.UserID != "dev" {
		t.Errorf("identity.UserID = %q, want %q", identity.UserID, "dev")
	}
}

func TestModeBasic(t *testing.T) {
	t.Parallel()

	authenticator, err := New(Config{Mode: ModeBasic, BasicUsername: "thomas", BasicPassword: "s3cr3t"})
	if err != nil {
		t.Fatalf("building the basic authenticator: %v", err)
	}

	cases := []struct {
		name     string
		username string
		password string
		set      bool
		wantErr  bool
	}{
		{name: "valid credentials", username: "thomas", password: "s3cr3t", set: true},
		{name: "wrong password", username: "thomas", password: "nope", set: true, wantErr: true},
		{name: "wrong user", username: "someone", password: "s3cr3t", set: true, wantErr: true},
		{name: "no credentials", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
			if tc.set {
				req.SetBasicAuth(tc.username, tc.password)
			}
			identity, err := authenticator.Authenticate(req)
			if tc.wantErr {
				if !errors.Is(err, ErrUnauthenticated) {
					t.Fatalf("got %v, want ErrUnauthenticated", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if identity.UserID != "thomas" {
				t.Errorf("identity.UserID = %q, want %q", identity.UserID, "thomas")
			}
		})
	}
}

// TestModeOIDCIsDeclaredButNotWired pins that the configured mode is accepted by
// the contract and fails explicitly rather than silently degrading to no auth.
func TestModeOIDCIsDeclaredButNotWired(t *testing.T) {
	t.Parallel()

	_, err := New(Config{Mode: ModeOIDC, OIDCIssuer: "https://keycloak.example/realms/threavia", OIDCClientID: "threavia"})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("got %v, want ErrNotImplemented", err)
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"none", Config{Mode: ModeNone, DevUserID: "dev"}, false},
		{"none without user", Config{Mode: ModeNone}, true},
		{"basic", Config{Mode: ModeBasic, BasicUsername: "u", BasicPassword: "p"}, false},
		{"basic without password", Config{Mode: ModeBasic, BasicUsername: "u"}, true},
		{"oidc", Config{Mode: ModeOIDC, OIDCIssuer: "https://issuer", OIDCClientID: "id"}, false},
		{"oidc without issuer", Config{Mode: ModeOIDC, OIDCClientID: "id"}, true},
		{"unknown mode", Config{Mode: Mode("ldap")}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.cfg.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
