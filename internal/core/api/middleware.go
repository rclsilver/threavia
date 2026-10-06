package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
)

type contextKey struct{ name string }

var identityContextKey = contextKey{name: "identity"}

// IdentityFrom returns the authenticated caller carried by ctx.
func IdentityFrom(ctx context.Context) (auth.Identity, bool) {
	identity, ok := ctx.Value(identityContextKey).(auth.Identity)
	return identity, ok
}

// authenticated resolves the caller identity and attaches it to the request
// context. Every user-scoped handler then reads it instead of trusting a client
// supplied identifier (spec sections 20 and 28).
func authenticated(authenticator auth.Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := authenticator.Authenticate(r)
		if err != nil {
			if authenticator.Mode() == auth.ModeBasic {
				w.Header().Set("WWW-Authenticate", `Basic realm="threavia", charset="UTF-8"`)
			}
			writeError(w, http.StatusUnauthorized, "unauthenticated", "valid credentials are required")
			return
		}
		ctx := context.WithValue(r.Context(), identityContextKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder captures the response status for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Flush forwards to the wrapped writer so SSE responses keep streaming.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// requestLogger logs one line per request. Credentials are never part of the
// logged fields (spec section 28).
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Debug("http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Duration("duration", time.Since(started)),
		)
	})
}
