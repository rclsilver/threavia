package telemetry

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

type metricsReader interface {
	ReadMetrics(context.Context) (postgres.MetricsSnapshot, error)
}
type stateCollector struct {
	db       *postgres.DB
	store    metricsReader
	broker   *events.Broker
	backends *backendconn.Registry
	desc     map[string]*prometheus.Desc
}

func newStateCollector(db *postgres.DB, store metricsReader, broker *events.Broker, backends *backendconn.Registry) *stateCollector {
	c := &stateCollector{db: db, store: store, broker: broker, backends: backends, desc: make(map[string]*prometheus.Desc)}
	add := func(name, help string, labels ...string) {
		c.desc[name] = prometheus.NewDesc("threavia_core_"+name, help, labels, nil)
	}
	add("metrics_database_collection_success", "Whether database-backed metrics were collected successfully.")
	add("jobs", "Current unfinished jobs by persisted status.", "project_id", "backend_instance_id", "known_directory_id", "status")
	add("job_oldest_state_age_seconds", "Age of the oldest unfinished job in its current state.", "project_id", "backend_instance_id", "known_directory_id", "status")
	add("jobs_finished_total", "Durable job outcomes observed since metrics migration.", "project_id", "backend_instance_id", "known_directory_id", "outcome")
	add("job_state_duration_seconds_total", "Accumulated completed state intervals since metrics migration.", "project_id", "backend_instance_id", "known_directory_id", "state")
	add("job_state_exits_total", "Completed state intervals since metrics migration.", "project_id", "backend_instance_id", "known_directory_id", "state")
	add("project_info", "Current project display name.", "project_id", "name")
	add("backend_info", "Current backend display name.", "backend_instance_id", "name")
	add("known_directory_info", "Current logical directory display name.", "known_directory_id", "name")
	add("backend_connected", "Whether this process holds a live control connection for the backend.", "backend_instance_id")
	add("backend_connections", "Live backend connections held by this process.")
	add("sse_connections", "Current SSE subscriptions held by this process.")
	add("sse_buffer_overflows_total", "Events not queued because a subscriber buffer was full.")
	add("sse_replays_total", "History catch-up attempts, including initial connections.")
	add("sse_replay_errors_total", "History catch-up attempts that failed to read events.")
	add("postgres_connections", "Pool connections by state.", "state")
	add("postgres_connections_max", "Maximum pool connections.")
	add("postgres_acquire_waits_total", "Acquisitions that waited for an available connection.")
	add("postgres_acquire_wait_seconds_total", "Time spent waiting for an available connection.")
	add("postgres_acquire_cancellations_total", "Cancelled connection acquisitions.")
	return c
}

func (c *stateCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range c.desc {
		ch <- d
	}
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, kind prometheus.ValueType, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(c.desc[name], kind, value, labels...)
	}
	gauge := func(name string, value float64, labels ...string) {
		emit(name, prometheus.GaugeValue, value, labels...)
	}
	counter := func(name string, value float64, labels ...string) {
		emit(name, prometheus.CounterValue, value, labels...)
	}
	if c.db != nil {
		stat := c.db.Pool().Stat()
		gauge("postgres_connections", float64(stat.AcquiredConns()), "acquired")
		gauge("postgres_connections", float64(stat.IdleConns()), "idle")
		gauge("postgres_connections", float64(stat.ConstructingConns()), "constructing")
		gauge("postgres_connections_max", float64(stat.MaxConns()))
		counter("postgres_acquire_waits_total", float64(stat.EmptyAcquireCount()))
		counter("postgres_acquire_wait_seconds_total", stat.EmptyAcquireWaitTime().Seconds())
		counter("postgres_acquire_cancellations_total", float64(stat.CanceledAcquireCount()))
	}
	gauge("sse_connections", float64(c.broker.Subscribers()))
	overflow, replays, errors := c.broker.Metrics()
	counter("sse_buffer_overflows_total", float64(overflow))
	counter("sse_replays_total", float64(replays))
	counter("sse_replay_errors_total", float64(errors))
	connected := make(map[string]bool)
	for _, conn := range c.backends.Connections() {
		select {
		case <-conn.Done():
		default:
			connected[string(conn.BackendInstanceID())] = true
		}
	}
	gauge("backend_connections", float64(len(connected)))
	for id := range connected {
		gauge("backend_connected", 1, id)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshot, err := c.store.ReadMetrics(ctx)
	if err != nil {
		gauge("metrics_database_collection_success", 0)
		ch <- prometheus.NewInvalidMetric(c.desc["jobs"], err)
		return
	}
	gauge("metrics_database_collection_success", 1)
	for _, g := range snapshot.Active {
		labels := []string{g.ProjectID, g.BackendInstanceID, g.KnownDirectoryID, g.State}
		gauge("jobs", g.Count, labels...)
		gauge("job_oldest_state_age_seconds", g.Seconds, labels...)
	}
	for _, g := range snapshot.Finished {
		counter("jobs_finished_total", g.Count, g.ProjectID, g.BackendInstanceID, g.KnownDirectoryID, g.State)
	}
	for _, g := range snapshot.Durations {
		labels := []string{g.ProjectID, g.BackendInstanceID, g.KnownDirectoryID, g.State}
		counter("job_state_duration_seconds_total", g.Seconds, labels...)
		counter("job_state_exits_total", g.Count, labels...)
	}
	for _, n := range snapshot.Names {
		gauge(n.Kind+"_info", 1, n.ID, n.Name)
		if n.Kind == "backend" && !connected[n.ID] {
			gauge("backend_connected", 0, n.ID)
		}
	}
}
