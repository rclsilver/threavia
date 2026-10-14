package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
)

func TestMetricsScrapesRealDatabaseAndPool(t *testing.T) {
	store, db := pgtest.New(t)
	m := New(db, store, events.NewBroker(), backendconn.NewRegistry(), "integration-test")
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("scrape = %d: %s", rr.Code, rr.Body.String())
	}
	for _, name := range []string{"threavia_core_metrics_database_collection_success 1", "threavia_core_postgres_connections_max 8", "threavia_core_postgres_acquire_wait_seconds_total", "go_goroutines", "process_start_time_seconds"} {
		if !strings.Contains(rr.Body.String(), name) {
			t.Errorf("missing %s", name)
		}
	}
}
