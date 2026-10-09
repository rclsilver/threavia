package api

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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

// securityHeaders sets the headers every response carries, API and web client
// alike.
//
// The web client has buttons that approve what an agent does, so no other site
// may frame it and lure a click onto them: X-Frame-Options for the browsers that
// predate frame-ancestors, the CSP directive for the others. The policy stops
// there on purpose: a script-src or style-src would refuse the inline styles the
// built client relies on. HSTS is left to the ingress that terminates TLS, since
// Core itself only ever speaks plain HTTP.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "frame-ancestors 'none'")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// allowedHosts answers 421 to a request whose Host is not one Core serves.
//
// This is what defeats DNS rebinding: a page on attacker.example that points its
// own name at 127.0.0.1 makes the browser treat Core as same-origin, so neither
// the browser nor the origin check stops it from reading the answers, but the
// Host header still says attacker.example. An empty list checks nothing.
//
// The probes are exempt: a kubelet addresses the pod by its IP, which no list of
// names a person types contains, and they reveal nothing.
func allowedHosts(hosts []string, next http.Handler) http.Handler {
	if len(hosts) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && !hostAllowed(hosts, r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "misdirected_request",
				"this server does not answer for host "+strconv.Quote(r.Host))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether a Host header matches one of the allowed values.
// An entry without a port matches the host on any port; an entry with one
// matches that port only.
func hostAllowed(allowed []string, host string) bool {
	name, port := splitHost(host)
	for _, entry := range allowed {
		entryName, entryPort := splitHost(entry)
		if entryName == name && (entryPort == "" || entryPort == port) {
			return true
		}
	}
	return false
}

// splitHost separates a host[:port] value, lower-cases the name and removes
// the brackets around an IPv6 address, so "[::1]", "[::1]:8080" and "::1" all
// name the same host.
func splitHost(value string) (name, port string) {
	if host, p, err := net.SplitHostPort(value); err == nil {
		return strings.ToLower(host), p
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")), ""
}

// crossSite reports whether a state-changing request was sent by a page of
// another site, which a browser lets any page do without asking: a form, or a
// fetch in no-cors mode, reaches Core with the user's ambient credentials (the
// development user in mode none, cached basic credentials in mode basic) even
// though the page never reads the answer.
//
// Sec-Fetch-Site is set by the browser and cannot be forged by a page, so when
// it is present it decides. Older browsers only send Origin, whose host must
// then be the one the request was sent to. Ports are ignored: the Vite dev
// server proxies from :5173 to :8080 and rewrites Host but not Origin, and a
// page on another port of the same host is a local process, which this check
// does not claim to defend against. A request carrying neither header does not
// come from a browser, so scripts and curl are unaffected.
func crossSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site != "same-origin" && site != "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	// A sandboxed frame or a data: URL sends the literal "null": it belongs to
	// no site, so it is not this one.
	parsed, err := url.Parse(origin)
	if origin == "null" || err != nil || parsed.Hostname() == "" {
		return true
	}
	name, _ := splitHost(r.Host)
	return !strings.EqualFold(parsed.Hostname(), name)
}

// refuseCrossSite answers 403 and reports true when the request is cross-site.
func refuseCrossSite(w http.ResponseWriter, r *http.Request) bool {
	if !crossSite(r) {
		return false
	}
	writeError(w, http.StatusForbidden, "forbidden_origin",
		"a page of another site cannot send this request")
	return true
}
