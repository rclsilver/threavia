// Package telemetry exposes Core metrics through a dedicated management server.
package telemetry

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

type Metrics struct {
	registry                                     *prometheus.Registry
	requests                                     *prometheus.CounterVec
	duration                                     *prometheus.HistogramVec
	inflight                                     *prometheus.GaugeVec
	disconnects, sendFailures, heartbeatTimeouts *prometheus.CounterVec
}

func New(db *postgres.DB, store metricsReader, broker *events.Broker, backends *backendconn.Registry, version string) *Metrics {
	m := &Metrics{
		registry:          prometheus.NewRegistry(),
		requests:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "threavia_core_http_requests_total", Help: "Completed non-streaming HTTP requests."}, []string{"method", "route", "status_code"}),
		duration:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "threavia_core_http_request_duration_seconds", Help: "Non-streaming HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"method", "route"}),
		inflight:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "threavia_core_http_requests_in_flight", Help: "Non-streaming requests currently being served."}, []string{"method"}),
		disconnects:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "threavia_core_backend_disconnections_total", Help: "Control connections closed by reason."}, []string{"backend_instance_id", "reason"}),
		sendFailures:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "threavia_core_backend_send_failures_total", Help: "Commands not queued for a control connection."}, []string{"backend_instance_id", "reason"}),
		heartbeatTimeouts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "threavia_core_backend_heartbeat_timeouts_total", Help: "Control connections closed after missing heartbeats."}, []string{"backend_instance_id"}),
	}
	m.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.duration, m.inflight, m.disconnects, m.sendFailures, m.heartbeatTimeouts)
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "threavia_core_build_info", Help: "Core build information."}, []string{"version"})
	build.WithLabelValues(version).Set(1)
	m.registry.MustRegister(build, newStateCollector(db, store, broker, backends))
	return m
}

// Handler serves only management metrics, without the UI or the user API.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2}))
	return mux
}

func (m *Metrics) ConnectionClosed(id domain.BackendInstanceID, reason backendconn.CloseReason) {
	m.disconnects.WithLabelValues(string(id), string(reason)).Inc()
	if reason == backendconn.ReasonStale {
		m.heartbeatTimeouts.WithLabelValues(string(id)).Inc()
	}
}
func (m *Metrics) SendFailed(id domain.BackendInstanceID, reason string) {
	m.sendFailures.WithLabelValues(string(id), reason).Inc()
}

// Wrap keeps streaming lifetimes out of ordinary API latency histograms.
func (m *Metrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/events" {
			next.ServeHTTP(w, r)
			return
		}
		method := r.Method
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			method = "OTHER"
		}
		gauge := m.inflight.WithLabelValues(method)
		gauge.Inc()
		defer gauge.Dec()
		started := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			if route == "/" {
				route = "web"
			}
			if _, suffix, ok := strings.Cut(route, " "); ok {
				route = suffix
			}
			m.requests.WithLabelValues(method, route, strconv.Itoa(rw.status)).Inc()
			m.duration.WithLabelValues(method, route).Observe(time.Since(started).Seconds())
		}()
		next.ServeHTTP(rw, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *responseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
