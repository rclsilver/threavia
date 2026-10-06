// Package api exposes the Core client surface: HTTP/JSON for commands, queries,
// snapshots and history, and SSE for Core -> client realtime events
// (THREAVIA_SPEC_V1.md section 5).
//
// No GraphQL and no WebSocket: the specification excludes them from V1 unless a
// demonstrated requirement appears.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
)

// Pinger is the readiness dependency of Core: PostgreSQL.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options wires the router dependencies.
type Options struct {
	Authenticator auth.Authenticator
	Database      Pinger
	Version       string
	Logger        *slog.Logger
}

// NewRouter builds the Core HTTP handler.
//
// The routed surface is currently limited to liveness, readiness and version.
// Every /api/v1 route answers 501 until the corresponding Core service lands, so
// a client never mistakes a missing endpoint for an empty result.
func NewRouter(opts Options) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if opts.Database == nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "database is not configured")
			return
		}
		if err := opts.Database.Ping(r.Context()); err != nil {
			opts.Logger.Warn("readiness probe failed", slog.String("error", err.Error()))
			writeError(w, http.StatusServiceUnavailable, "not_ready", "database is unreachable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": opts.Version})
	})

	api := http.NewServeMux()
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "not_implemented",
			"this endpoint is part of the V1 client API but is not implemented yet")
	})
	mux.Handle("/api/v1/", authenticated(opts.Authenticator, api))

	return requestLogger(opts.Logger, mux)
}

// errorBody is the single error shape returned by the Core client API.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, statusCode, body)
}
