# Core metrics

Set `THREAVIA_METRICS_ENABLED=true` to serve `GET /metrics` on a dedicated
listener. `THREAVIA_METRICS_ADDR` defaults to `127.0.0.1:9091`. This management
server has no user authentication and serves no client API or UI. Bind a trusted
interface and restrict network access to the collector. It is separate from
the client OpenAPI contract.

The Helm chart creates a separate ClusterIP Service, even if the API Service
uses a LoadBalancer. Neither Ingress exposes metrics. Prometheus Operator's
ServiceMonitor CRD must already exist; Prometheus must select the monitor's
labels and namespace.

```yaml
metrics:
  enabled: true
  port: 9091
  serviceMonitor:
    enabled: true
    labels:
      release: monitoring # match your Prometheus serviceMonitorSelector
    interval: 30s
    scrapeTimeout: 5s
```

## Metric catalog

Application metrics have the prefix `threavia_core_`. Standard `go_*` and
`process_*` collectors expose runtime, GC, memory, CPU and process start time.

| Family (without prefix) | Type | Labels |
| --- | --- | --- |
| `build_info` | Gauge, always 1 | `version` |
| `http_requests_total` | Counter | `method`, `route`, `status_code` |
| `http_request_duration_seconds` | Histogram | `method`, `route` |
| `http_requests_in_flight` | Gauge | `method` |
| `postgres_connections` | Gauge | `state`: acquired, idle, constructing |
| `postgres_connections_max` | Gauge | — |
| `postgres_acquire_waits_total` | Counter | — |
| `postgres_acquire_wait_seconds_total` | Counter | — |
| `postgres_acquire_cancellations_total` | Counter | — |
| `backend_connections` | Gauge | — |
| `backend_connected` | Gauge, 0 or 1 | `backend_instance_id` |
| `backend_disconnections_total` | Counter | `backend_instance_id`, `reason`: released, superseded, stale, shutdown |
| `backend_heartbeat_timeouts_total` | Counter | `backend_instance_id` |
| `backend_send_failures_total` | Counter | `backend_instance_id`, `reason`: closed, queue_full |
| `sse_connections` | Gauge | — |
| `sse_buffer_overflows_total` | Counter | — |
| `sse_replays_total` | Counter | — |
| `sse_replay_errors_total` | Counter | — |
| `jobs` | Gauge | Job dimensions + `status` |
| `job_oldest_state_age_seconds` | Gauge | Job dimensions + `status` |
| `jobs_finished_total` | Counter | Job dimensions + `outcome` |
| `job_state_duration_seconds_total` | Counter | Job dimensions + `state` |
| `job_state_exits_total` | Counter | Job dimensions + `state` |
| `project_info` | Gauge, always 1 | `project_id`, `name` |
| `backend_info` | Gauge, always 1 | `backend_instance_id`, `name` |
| `known_directory_info` | Gauge, always 1 | `known_directory_id`, `name` |
| `session_info` | Gauge, always 1 | `session_id`, `name` (its title) |
| `job_tokens_total` | Counter | Usage dimensions + `type`: input, output, cache_read, cache_write |
| `job_cost_usd_total` | Counter | Usage dimensions |
| `job_usage_jobs_total` | Counter | Usage dimensions + `outcome` + `usage`: reported, unreported |
| `job_backend_waits_total` | Counter | `project_id`, `backend_instance_id`, `reason` |
| `jobs_waiting_backend` | Gauge | `project_id`, `backend_instance_id`, `reason` |
| `backend_quota_available` | Gauge, 0 or 1 | `backend_instance_id` |
| `backend_quota_observed_at_seconds` | Gauge | `backend_instance_id` |
| `backend_quota_info` | Gauge, always 1 | Quota dimensions + `label`, `unit` |
| `backend_quota_used_percent` | Gauge | Quota dimensions |
| `backend_quota_used`, `backend_quota_limit`, `backend_quota_remaining` | Gauge | Quota dimensions |
| `backend_quota_window_seconds` | Gauge | Quota dimensions |
| `backend_quota_resets_at_seconds` | Gauge | Quota dimensions |
| `metrics_database_collection_success` | Gauge | — |

Job dimensions are **`project_id`, `backend_instance_id`, `known_directory_id`**:
UUIDs, with an empty directory label when none is configured. No job
identifiers, physical paths, prompts or user identifiers are exported. Only
observed combinations are exported, not a Cartesian product.

Usage dimensions are **`project_id`, `session_id`, `backend_instance_id`,
`model`, `effort`**. Consumption is the one place a Session identifier is
exported: which Session spends a quota is the question these metrics answer.
An empty `model` or `effort` is the backend's default. Quota dimensions are
**`backend_instance_id`, `limit_id`**, the provider's identifier of a limit.

HTTP routes are matched patterns such as `/api/v1/jobs/{jobId}`, never raw
paths. Unknown routes use `unmatched`; static UI paths use `web`. Unsupported
methods use `OTHER`. SSE has separate metrics: its connection lifetime does
not contribute to ordinary API latency histograms. Control gRPC streams are
represented by backend connection and failure metrics.

An SSE overflow counts one event not queued for one subscriber. A replay counts
one catch-up attempt, initial or after overflow, regardless of database pages.
A replay error is a failed history read.

## Job persistence and attribution

Migration 23 adds a state-entry timestamp, a directory snapshot and durable
aggregate counters. A database trigger records status changes in the same
transaction as the job. Rollbacks and duplicate backend events add nothing.
Counters survive restarts and job/project deletion; info metrics describe only
currently existing objects. Counting continues when the listener is disabled.

Queued jobs follow the session's configured directory. Attribution is refreshed
until first execution, then frozen: later session changes do not rewrite the
execution's historical labels. The directory is the configured working
directory, not every folder visited by an agent.

`jobs` covers unfinished jobs. `job_oldest_state_age_seconds` measures time since
entry into the current state, not creation. Completed state intervals are
accumulated separately for `QUEUED`, `RUNNING`, `WAITING_INPUT`,
`WAITING_VALIDATION`, `WAITING_BACKEND` and `CANCELLING`. `RUNNING` is wall time
in that logical state, not backend CPU time. Open intervals are represented by
the age gauge and enter the counters when the state ends. Divide duration
counter rates by state-exit counter rates to obtain mean completed intervals.

Existing jobs start observation at migration time, with the directory currently
configured on their session. Earlier durations and completed outcomes are not
reconstructed.

Database reads have a three-second deadline and a consistent transaction. A
failed collection returns HTTP 500, making Prometheus `up` zero, instead of
serving stale or invented zero job metrics. Missing groups mean no current jobs
or no observed aggregate; use `or vector(0)` for a global zero when appropriate.

## Consumption and quotas

Migration 38 counts what each ended Job consumed, as its backend reported it,
from the event that ends it: completed, failed or cancelled. A database trigger
updates the counters in the transaction that appends the event; a replayed
backend event is not appended, so it is not counted twice. Unlike durations,
this history is in the events: the migration rebuilds it from them, except for
Sessions already deleted. Counters then survive Session deletion.

Usage is counted once the Job ends, with every provider attempt it took: a Job
waiting on a quota has consumed tokens that appear when it ends. Tokens and cost
add up all outcomes; `job_usage_jobs_total` says how many Jobs ended with an
unknown usage (`usage="unreported"`), which are not free Jobs. Cancelled Jobs
report their usage from backends that include this change; earlier backends
send none. Claude Code's cost is the provider's API-price estimate, not a share
of a subscription quota; Codex reports no cost.

`job_backend_waits_total` counts every stop to wait for a backend by the
provider's reason (`QUOTA_EXCEEDED`, `RATE_LIMITED`,
`AUTHENTICATION_REQUIRED`, …). `jobs_waiting_backend` also covers Jobs parked
because their backend disconnected, as `BACKEND_DISCONNECTED`.

Quota gauges read the latest report of each backend. Values the provider did not
report are absent, never zero. Prometheus keeps the history Core does not.

Top Sessions by tokens over the last week, with their title:

```promql
topk(10, sum by (session_id) (
  max by (project_id, session_id, backend_instance_id, model, effort, type) (
    increase(threavia_core_job_tokens_total[7d]))))
  * on (session_id) group_left(name) max by (session_id, name) (threavia_core_session_info)
```

Output tokens by model and effort over a day:

```promql
sum by (model, effort) (
  max by (project_id, session_id, backend_instance_id, model, effort) (
    increase(threavia_core_job_tokens_total{type="output"}[1d])))
```

Weekly quota use, per backend and limit:

```promql
max by (backend_instance_id, limit_id) (threavia_core_backend_quota_used_percent)
  * on (backend_instance_id, limit_id) group_left(label)
    max by (backend_instance_id, limit_id, label) (threavia_core_backend_quota_info)
```

The same ranking by Session is available to clients as `GET /api/v1/usage`, by
UTC day, for Sessions that still exist.

## Replicas and display names

Runtime, HTTP, pool, SSE and backend connections belong to each Core process.
Job metrics and names are database-wide and identical across replicas.
Deduplicate those with `max by (...)` before summing. For example:

```promql
sum(max by (project_id, backend_instance_id, known_directory_id, status) (
  threavia_core_jobs
))
```

Join a deduplicated info metric to display a name without putting mutable names
on execution counters:

```promql
max by (project_id, backend_instance_id, known_directory_id, status) (threavia_core_jobs)
  * on (project_id) group_left(name)
    max by (project_id, name) (threavia_core_project_info)
```

Prometheus supplies target labels such as `instance` and, depending on discovery,
`namespace` and `pod`. Names are visible to anyone reaching the listener.

Dashboards and alert rules are not installed by this change.
