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
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/service"
)

// maxRequestBytes bounds a JSON request body. Large content belongs in an
// Artifact, never in a command payload.
const maxRequestBytes = 1 << 20

// Pinger is the readiness dependency of Core: PostgreSQL.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options wires the router dependencies.
type Options struct {
	Service       *service.Service
	Authenticator auth.Authenticator
	Database      Pinger
	Version       string
	Logger        *slog.Logger
	// WebUI is served at the root when set.
	WebUI http.Handler
	// AllowedHosts lists the Host values Core answers, as host or host:port.
	// Empty means any.
	AllowedHosts []string
}

type handler struct {
	svc     *service.Service
	auth    auth.Authenticator
	db      Pinger
	version string
	// spec is the contract as this build serves it: the document from the
	// repository with this build's version written in.
	spec *specDocument
	// routes records every pattern registered, so the OpenAPI document can be
	// checked against what the server actually serves rather than against what
	// someone remembered to write down.
	routes []string
	logger *slog.Logger
}

// NewRouter builds the Core HTTP handler.
func NewRouter(opts Options) http.Handler {
	h := &handler{
		svc:     opts.Service,
		auth:    opts.Authenticator,
		db:      opts.Database,
		version: opts.Version,
		spec:    newSpecDocument(opts.Version),
		logger:  opts.Logger,
	}

	mux := http.NewServeMux()
	h.registerOperational(mux)
	h.registerRegistration(mux)
	h.registerProjects(mux)
	h.registerBackends(mux)
	h.registerSessions(mux)
	h.registerJobs(mux)
	h.registerAttention(mux)
	h.registerKnowledge(mux)
	h.registerPolicy(mux)
	h.registerArtifacts(mux)
	h.registerSkills(mux)
	h.registerSchedules(mux)
	h.registerPush(mux)
	h.registerStream(mux)
	h.registerSpec(mux)

	// Anything else under the versioned prefix is a route that does not exist
	// yet; answering 501 keeps a client from reading it as an empty result.
	mux.Handle("/api/v1/", h.secured(func(w http.ResponseWriter, r *http.Request, _ auth.Identity) {
		writeError(w, http.StatusNotImplemented, "not_implemented",
			"this endpoint is part of the V1 client API but is not implemented yet")
	}))

	if opts.WebUI != nil {
		mux.Handle("/", opts.WebUI)
	}
	return requestLogger(opts.Logger, securityHeaders(allowedHosts(opts.AllowedHosts, mux)))
}

func (h *handler) registerOperational(mux *http.ServeMux) {
	h.open(mux, "GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	h.open(mux, "GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if h.db == nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "database is not configured")
			return
		}
		if err := h.db.Ping(r.Context()); err != nil {
			h.logger.Warn("readiness probe failed", slog.String("error", err.Error()))
			writeError(w, http.StatusServiceUnavailable, "not_ready", "database is unreachable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	h.open(mux, "GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
	})
}

// secured resolves the caller identity before running the handler. Every
// user-scoped route goes through here, so no handler ever trusts an identifier
// supplied by the client.
func (h *handler) secured(fn func(http.ResponseWriter, *http.Request, auth.Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuseCrossSite(w, r) {
			return
		}
		identity, err := h.auth.Authenticate(r)
		if err != nil {
			if h.auth.Mode() == auth.ModeBasic {
				w.Header().Set("WWW-Authenticate", `Basic realm="threavia", charset="UTF-8"`)
			}
			writeError(w, http.StatusUnauthorized, "unauthenticated", "valid credentials are required")
			return
		}
		// Every operation below can then say which client it is serving, which is
		// what the audit trail and the notification relevance both read.
		ctx := domain.WithClient(domain.WithChannel(r.Context(), channel(r)), client(r))
		fn(w, r.WithContext(ctx), identity)
	})
}

// open registers a route that needs no user credential, and records it like any
// other so the OpenAPI document is checked against the whole surface.
func (h *handler) open(mux *http.ServeMux, pattern string, fn http.HandlerFunc) {
	h.routes = append(h.routes, pattern)
	mux.HandleFunc(pattern, fn)
}

// Routes returns every pattern this handler serves. It exists for the test that
// holds the OpenAPI document and the server to each other.
func (h *handler) Routes() []string { return slices.Clone(h.routes) }

// handle registers a secured route.
//
// Every path wildcard in the Core API names an entity by UUID, so a value that
// is not one cannot name anything. Rejecting it here answers "not found", which
// is what an unknown identifier deserves, instead of letting PostgreSQL refuse
// the cast and turning a typo into a 500.
func (h *handler) handle(mux *http.ServeMux, pattern string, fn func(http.ResponseWriter, *http.Request, auth.Identity)) {
	h.routes = append(h.routes, pattern)

	wildcards := pathWildcards(pattern)
	mux.Handle(pattern, h.secured(func(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
		for _, name := range wildcards {
			if _, err := uuid.Parse(r.PathValue(name)); err != nil {
				writeError(w, http.StatusNotFound, "not_found", "not found")
				return
			}
		}
		fn(w, r, identity)
	}))
}

// pathWildcards returns the {name} segments of a routing pattern.
func pathWildcards(pattern string) []string {
	var names []string
	for {
		start := strings.Index(pattern, "{")
		end := strings.Index(pattern, "}")
		if start < 0 || end < start {
			return names
		}
		name := strings.TrimSuffix(pattern[start+1:end], "...")
		if name != "" {
			names = append(names, name)
		}
		pattern = pattern[end+1:]
	}
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

// fail maps a service error onto an HTTP status. ErrNotFound covers both "does
// not exist" and "not yours", so an identifier cannot be probed.
func (h *handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, service.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, service.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, service.ErrRegistrationRejected):
		writeError(w, http.StatusForbidden, "registration_rejected", err.Error())
	case errors.Is(err, service.ErrStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", err.Error())
	case errors.Is(err, service.ErrBackendUnavailable):
		writeError(w, http.StatusServiceUnavailable, "backend_unavailable", err.Error())
	default:
		h.logger.Error("request failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// decode reads a bounded JSON body.
//
// The body must be declared as JSON. A browser sends a text/plain or form body
// from any page without asking the server first, so accepting whatever the
// Content-Type says would let another site write here. An empty body with no
// Content-Type stays acceptable, for the routes whose body is optional.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var target T
	declared := r.Header.Get("Content-Type")
	if declared != "" {
		mediaType, _, err := mime.ParseMediaType(declared)
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
				"the request body must be application/json")
			return target, false
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&target)
	if errors.Is(err, io.EOF) {
		return target, true
	}
	if declared == "" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"a request body must be sent as application/json")
		return target, false
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())
		return target, false
	}
	return target, true
}

// list is the envelope every collection response uses, so adding pagination
// later never changes the shape.
type list[T any] struct {
	Items []T `json:"items"`
}

func writeList[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, list[T]{Items: items})
}

// queryInt reads an optional integer query parameter.
func queryInt(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

// querySequence reads an optional global event sequence cursor.
func querySequence(r *http.Request, name string) domain.Sequence {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return domain.Sequence(value)
}

// queryBool reads an optional boolean query parameter.
func queryBool(r *http.Request, name string) bool {
	value, err := strconv.ParseBool(r.URL.Query().Get(name))
	return err == nil && value
}

// client reports which client instance sent the request: a stable id the
// client keeps, and the name its person gave the device. The name travels
// URI-encoded, because a header is Latin-1 and a device name need not be.
func client(r *http.Request) domain.Client {
	id := r.Header.Get("X-Threavia-Client")
	name := r.Header.Get("X-Threavia-Client-Name")
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	return domain.NormaliseClient(id, name)
}

// presence reads the presence a client declared when opening its stream, or
// nil when it said nothing.
func presence(r *http.Request) *events.Presence {
	raw := r.Header.Get("X-Threavia-Active")
	if raw == "" {
		return nil
	}
	active, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &events.Presence{Active: active}
}

// channel reports which kind of client sent the request (spec section 6).
//
// A header rather than a body field, so every route carries it the same way,
// including the ones with no body at all. An absent header is not an error: it
// becomes the generic API channel, and the request works exactly as before.
func channel(r *http.Request) domain.Channel {
	if header := r.Header.Get("X-Threavia-Channel"); header != "" {
		return domain.NormaliseChannel(header)
	}
	// A browser EventSource cannot set a header, so the stream also accepts it
	// as a query parameter.
	return domain.NormaliseChannel(r.URL.Query().Get("channel"))
}
