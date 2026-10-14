package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

type snapshotReader struct {
	snapshot postgres.MetricsSnapshot
	err      error
}

func (s snapshotReader) ReadMetrics(context.Context) (postgres.MetricsSnapshot, error) {
	return s.snapshot, s.err
}

func newTestMetrics(reader snapshotReader) (*Metrics, *events.Broker, *backendconn.Registry) {
	broker, registry := events.NewBroker(), backendconn.NewRegistry()
	m := New(nil, reader, broker, registry, "test-version")
	registry.SetObserver(m)
	return m, broker, registry
}

func TestHTTPMetricsBoundLabelsAndPreserveResponses(t *testing.T) {
	m, _, _ := newTestMetrics(snapshotReader{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jobs/{jobId}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush() })
	handler := m.Wrap(mux)
	for _, path := range []string{"/jobs/private-one?token=secret", "/jobs/private-two"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d", rr.Code)
		}
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("ARBITRARY", "/private-three", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/events", nil))
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("scrape: %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, secret := range []string{"private-one", "private-two", "private-three", "secret", "ARBITRARY", "route=\"/api/v1/events\""} {
		if strings.Contains(body, secret) {
			t.Fatalf("unbounded label leaked: %s", secret)
		}
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/jobs/{jobId}", "202")); got != 2 {
		t.Fatalf("request count = %v", got)
	}
	if got := testutil.ToFloat64(m.inflight.WithLabelValues("GET")); got != 0 {
		t.Fatalf("in flight = %v", got)
	}
	for _, path := range []string{"/", "/api/v1/jobs"} {
		rr := httptest.NewRecorder()
		m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("management server serves %s", path)
		}
	}
}

func TestScrapeReportsJobDimensionsAndStreamCounters(t *testing.T) {
	snapshot := postgres.MetricsSnapshot{
		Active:   []postgres.MetricGroup{{ProjectID: "p", BackendInstanceID: "b", KnownDirectoryID: "d", State: "WAITING_INPUT", Count: 2, Seconds: 42}},
		Finished: []postgres.MetricGroup{{ProjectID: "p", BackendInstanceID: "b", KnownDirectoryID: "", State: "COMPLETED", Count: 3}},
		Names:    []postgres.MetricName{{Kind: "project", ID: "p", Name: "Project"}, {Kind: "backend", ID: "b", Name: "Backend"}, {Kind: "known_directory", ID: "d", Name: "Directory"}},
	}
	m, broker, registry := newTestMetrics(snapshotReader{snapshot: snapshot})
	_, cancel := broker.Subscribe(domain.UserID("owner"), "", domain.Client{}, nil)
	defer cancel()
	for range 257 {
		broker.Publish("owner", events.Envelope{})
	}
	broker.NoteReplay()
	broker.NoteReplayError()
	conn, _ := registry.Register("b")
	for range 65 {
		conn.Send(nil)
	}
	registry.CloseAll(backendconn.ReasonStale)
	registry.Release(conn) // a second close must not increment the counter
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("scrape: %s", rr.Body.String())
	}
	for _, want := range []string{
		`threavia_core_jobs{backend_instance_id="b",known_directory_id="d",project_id="p",status="WAITING_INPUT"} 2`,
		`threavia_core_jobs_finished_total{backend_instance_id="b",known_directory_id="",outcome="COMPLETED",project_id="p"} 3`,
		`threavia_core_sse_connections 1`,
		`threavia_core_sse_buffer_overflows_total 1`,
		`threavia_core_sse_replays_total 1`,
		`threavia_core_sse_replay_errors_total 1`,
		`threavia_core_backend_heartbeat_timeouts_total{backend_instance_id="b"} 1`,
		`threavia_core_backend_disconnections_total{backend_instance_id="b",reason="stale"} 1`,
		`threavia_core_backend_send_failures_total{backend_instance_id="b",reason="queue_full"} 1`,
		`threavia_core_backend_connected{backend_instance_id="b"} 0`,
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "session_id" || label.GetName() == "job_id" || label.GetName() == "path" {
					t.Fatalf("unexpected dimension %s", label.GetName())
				}
			}
		}
	}
	// All descriptor declarations agree with their collected labels.
	pedantic := prometheus.NewPedanticRegistry()
	pedantic.MustRegister(newStateCollector(nil, snapshotReader{snapshot: snapshot}, broker, registry))
	if _, err := pedantic.Gather(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedDatabaseCollectionFailsScrape(t *testing.T) {
	m, _, _ := newTestMetrics(snapshotReader{err: errors.New("database unavailable")})
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("failed scrape = %d", rr.Code)
	}
}
