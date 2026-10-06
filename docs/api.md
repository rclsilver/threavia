# Client API

Clients — Web, Android, VS Code, and the Voice-facing services — use HTTP/JSON
for commands, queries, snapshots and history, and SSE for realtime events
(specification section 5). There is no GraphQL and no WebSocket in V1.

## Implemented today

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/healthz` | no | Liveness |
| `GET` | `/readyz` | no | Readiness, pings PostgreSQL |
| `GET` | `/version` | no | Core version |
| any | `/api/v1/...` | yes | `501 Not Implemented` |

Every `/api/v1` route is authenticated and will be scoped to the caller
identity. Until the Core services land, they answer an explicit `501` rather
than an empty result, so no client mistakes a missing endpoint for empty data.

## Errors

One shape for every error:

```json
{ "error": { "code": "not_implemented", "message": "..." } }
```

## Authentication

Selected by `THREAVIA_AUTH_MODE`:

- `none` — every request is attributed to `THREAVIA_AUTH_DEV_USER_ID`. Local
  development only; Core logs a warning at startup.
- `basic` — HTTP basic authentication against a configured credential.
- `oidc` — declared and accepted by the contract, not wired yet: Core refuses to
  start rather than silently degrading to no authentication.

## Planned surface

The V1 endpoints of specification section 5, in the implementation order of
section 31:

```text
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{projectId}/sessions
POST   /api/v1/sessions/start
GET    /api/v1/sessions/{sessionId}
PATCH  /api/v1/sessions/{sessionId}
GET    /api/v1/sessions/{sessionId}/events?before=...
POST   /api/v1/sessions/{sessionId}/messages
POST   /api/v1/jobs/{jobId}/cancel
POST   /api/v1/validations/{validationId}/resolve
POST   /api/v1/user-input/{requestId}/resolve
GET    /api/v1/me/attention
GET    /api/v1/events              # SSE
```

Two properties to preserve when building them:

- **Opening a Session returns a snapshot**, a recent window of history, the
  pending attention items and the current cursor. A client never replays the
  whole event log to rebuild state.
- **Pending attention is current state**, not an unread-event counter. Once a
  validation or input request is resolved, it disappears everywhere, and a
  reconnecting client never turns old events into stale notifications.

Commands clients may realistically retry — `sessions/start`, enqueueing a
message, cancelling a Job, resolving a validation or an input request — carry an
idempotency key (section 27).

## SSE

`GET /api/v1/events` is the user-wide stream. Reconnection uses the global event
sequence as cursor, through `Last-Event-ID` or an equivalent query parameter, so
a client holds one cursor rather than one per Session.

The Core HTTP server runs with no write timeout, because SSE responses are
long-lived by design.
