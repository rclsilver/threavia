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
	usage := []string{"project_id", "session_id", "backend_instance_id", "model", "effort"}
	add("job_tokens_total", "Tokens ended Jobs consumed, as their backend reported them, by type.", append(usage, "type")...)
	add("job_cost_usd_total", "What the provider said ended Jobs cost, in US dollars. Not every provider says.", usage...)
	add("job_usage_jobs_total", "Ended Jobs by outcome, and whether their backend reported their usage.", append(usage, "outcome", "usage")...)
	add("job_backend_waits_total", "Times a Job stopped to wait for its backend, by reason.", "project_id", "backend_instance_id", "reason")
	add("jobs_waiting_backend", "Jobs waiting for their backend now, by reason.", "project_id", "backend_instance_id", "reason")
	add("backend_quota_available", "Whether the backend's latest quota report could be read from the provider.", "backend_instance_id")
	add("backend_quota_observed_at_seconds", "When the backend last read its quotas, as a Unix time.", "backend_instance_id")
	limit := []string{"backend_instance_id", "limit_id"}
	add("backend_quota_info", "Display label and unit of a provider limit.", append(limit, "label", "unit")...)
	add("backend_quota_used_percent", "Share of a provider limit already used.", limit...)
	add("backend_quota_used", "Amount of a provider limit already used, in its unit.", limit...)
	add("backend_quota_limit", "Size of a provider limit, in its unit.", limit...)
	add("backend_quota_remaining", "Amount of a provider limit still available, in its unit.", limit...)
	add("backend_quota_window_seconds", "Length of the window a provider limit applies to.", limit...)
	add("backend_quota_resets_at_seconds", "When a provider limit resets, as a Unix time.", limit...)
	add("project_info", "Current project display name.", "project_id", "name")
	add("session_info", "Current session title.", "session_id", "name")
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
	c.collectUsage(snapshot, gauge, counter)
	for _, n := range snapshot.Names {
		gauge(n.Kind+"_info", 1, n.ID, n.Name)
		if n.Kind == "backend" && !connected[n.ID] {
			gauge("backend_connected", 0, n.ID)
		}
	}
}

type emitFunc func(name string, value float64, labels ...string)

// collectUsage exports consumption and quotas. Outcomes are summed for tokens
// and cost: what a cancelled Job consumed counts as much as a completed one's.
func (c *stateCollector) collectUsage(snapshot postgres.MetricsSnapshot, gauge, counter emitFunc) {
	type key struct{ project, session, backend, model, effort string }
	type spent struct{ input, output, cacheRead, cacheWrite, cost float64 }
	totals := make(map[key]*spent)
	for _, g := range snapshot.Usage {
		k := key{g.ProjectID, g.SessionID, g.BackendInstanceID, g.Model, g.Effort}
		labels := []string{k.project, k.session, k.backend, k.model, k.effort, g.Outcome}
		counter("job_usage_jobs_total", g.ReportedJobs, append(labels, "reported")...)
		counter("job_usage_jobs_total", g.Jobs-g.ReportedJobs, append(labels, "unreported")...)
		s := totals[k]
		if s == nil {
			s = &spent{}
			totals[k] = s
		}
		s.input += g.InputTokens
		s.output += g.OutputTokens
		s.cacheRead += g.CacheReadTokens
		s.cacheWrite += g.CacheWriteTokens
		s.cost += g.CostUSD
	}
	for k, s := range totals {
		labels := []string{k.project, k.session, k.backend, k.model, k.effort}
		counter("job_tokens_total", s.input, append(labels, "input")...)
		counter("job_tokens_total", s.output, append(labels, "output")...)
		counter("job_tokens_total", s.cacheRead, append(labels, "cache_read")...)
		counter("job_tokens_total", s.cacheWrite, append(labels, "cache_write")...)
		counter("job_cost_usd_total", s.cost, labels...)
	}
	for _, g := range snapshot.Waits {
		counter("job_backend_waits_total", g.Count, g.ProjectID, g.BackendInstanceID, g.Reason)
	}
	for _, g := range snapshot.Waiting {
		gauge("jobs_waiting_backend", g.Count, g.ProjectID, g.BackendInstanceID, g.Reason)
	}
	for _, q := range snapshot.Quotas {
		if q.Status.Availability == "" {
			continue
		}
		id := q.BackendInstanceID
		available := 0.0
		if q.Status.Availability == "AVAILABLE" {
			available = 1
		}
		gauge("backend_quota_available", available, id)
		if !q.Status.ObservedAt.IsZero() {
			gauge("backend_quota_observed_at_seconds", float64(q.Status.ObservedAt.Unix()), id)
		}
		seen := make(map[string]bool)
		for _, l := range q.Status.Limits {
			if l.ID == "" || seen[l.ID] {
				continue
			}
			seen[l.ID] = true
			gauge("backend_quota_info", 1, id, l.ID, l.Label, l.Unit)
			// Absent values stay absent: a limit the provider did not size is
			// not a limit of zero.
			optional := func(name string, value *float64) {
				if value != nil {
					gauge(name, *value, id, l.ID)
				}
			}
			optional("backend_quota_used_percent", l.PercentUsed)
			optional("backend_quota_used", l.Used)
			optional("backend_quota_limit", l.Limit)
			optional("backend_quota_remaining", l.Remaining)
			if l.WindowSeconds > 0 {
				gauge("backend_quota_window_seconds", float64(l.WindowSeconds), id, l.ID)
			}
			if l.ResetsAt != nil {
				gauge("backend_quota_resets_at_seconds", float64(l.ResetsAt.Unix()), id, l.ID)
			}
		}
	}
}
