package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rclsilver/threavia/internal/core/api"
	"github.com/rclsilver/threavia/internal/core/auth"
)

type stubDatabase struct{ err error }

func (s stubDatabase) Ping(context.Context) error { return s.err }

func newRouter(t *testing.T, authCfg auth.Config, db api.Pinger) http.Handler {
	t.Helper()
	authenticator, err := auth.New(authCfg)
	if err != nil {
		t.Fatalf("building the authenticator: %v", err)
	}
	return api.NewRouter(api.Options{
		Authenticator: authenticator,
		Database:      db,
		Version:       "test",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestHealthAndVersion(t *testing.T) {
	t.Parallel()

	router := newRouter(t, auth.Config{Mode: auth.ModeNone, DevUserID: "dev"}, stubDatabase{})

	for _, path := range []string{"/healthz", "/readyz", "/version"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, recorder.Code)
		}
	}
}

func TestReadinessFollowsTheDatabase(t *testing.T) {
	t.Parallel()

	router := newRouter(t, auth.Config{Mode: auth.ModeNone, DevUserID: "dev"}, stubDatabase{err: errors.New("down")})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz with an unreachable database = %d, want 503", recorder.Code)
	}
}

// TestUnimplementedEndpointsAreExplicit keeps a client from mistaking a missing
// endpoint for an empty result.
func TestUnimplementedEndpointsAreExplicit(t *testing.T) {
	t.Parallel()

	router := newRouter(t, auth.Config{Mode: auth.ModeNone, DevUserID: "dev"}, stubDatabase{})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("GET /api/v1/projects = %d, want 501", recorder.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decoding the error body: %v", err)
	}
	if body.Error.Code != "not_implemented" {
		t.Errorf("error code = %q, want %q", body.Error.Code, "not_implemented")
	}
}

// TestClientAPIRequiresAuthentication pins that every /api/v1 route is scoped to
// an authenticated user, while the probes stay open.
func TestClientAPIRequiresAuthentication(t *testing.T) {
	t.Parallel()

	router := newRouter(t, auth.Config{
		Mode:          auth.ModeBasic,
		BasicUsername: "thomas",
		BasicPassword: "s3cr3t",
	}, stubDatabase{})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/projects without credentials = %d, want 401", recorder.Code)
	}
	if challenge := recorder.Header().Get("WWW-Authenticate"); challenge == "" {
		t.Error("a basic-auth Core must send a challenge")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	request.SetBasicAuth("thomas", "s3cr3t")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("GET /api/v1/projects with credentials = %d, want 501", recorder.Code)
	}

	// Probes must stay reachable without credentials.
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", recorder.Code)
	}
}
