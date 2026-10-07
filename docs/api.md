# Client API

Clients — Web, Android, VS Code, and the Voice-facing services — use HTTP/JSON
for commands, queries, snapshots and history, and SSE for realtime events
(specification section 5). There is no GraphQL and no WebSocket in V1.

## Implemented

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/healthz` | no | Liveness |
| `GET` | `/readyz` | no | Readiness, pings PostgreSQL |
| `GET` | `/version` | no | Core version |
| `POST` | `/api/v1/backends/register` | credential | A backend registers itself |
| `GET` | `/api/v1/projects` | yes | List Projects |
| `POST` | `/api/v1/projects` | yes | Create a Project |
| `GET` | `/api/v1/projects/{id}` | yes | One Project |
| `POST` | `/api/v1/projects/{id}/archive` | yes | Archive |
| `POST` | `/api/v1/projects/{id}/restore` | yes | Restore |
| `PATCH` | `/api/v1/projects/{id}` | yes | Rename, redescribe, set ProjectInstructions |
| `DELETE` | `/api/v1/projects/{id}` | yes | Permanent deletion |
| `GET` | `/api/v1/projects/{id}/sessions` | yes | Sessions of a Project |
| `GET` | `/api/v1/projects/{id}/directories` | yes | KnownDirectories |
| `POST` | `/api/v1/projects/{id}/directories` | yes | Create a KnownDirectory |
| `GET` | `/api/v1/directories/{id}/bindings` | yes | Backend bindings |
| `POST` | `/api/v1/directories/{id}/bindings` | yes | Bind to a backend path |
| `GET` | `/api/v1/backends` | yes | BackendInstances of the user |
| `GET` | `/api/v1/backends/{id}` | yes | One BackendInstance |
| `POST` | `/api/v1/backends/{id}/revoke` | yes | Revoke its credential |
| `POST` | `/api/v1/backends/claim` | yes | Claim with a one-time code |
| `POST` | `/api/v1/backend-tokens` | yes | Issue a one-shot registration token |
| `POST` | `/api/v1/sessions/start` | yes | Atomic first send |
| `GET` | `/api/v1/sessions/{id}` | yes | Snapshot: state, history, attention, cursor |
| `PATCH` | `/api/v1/sessions/{id}` | yes | Rename, or change the working directory |
| `GET` | `/api/v1/sessions/{id}/events` | yes | Paged history |
| `POST` | `/api/v1/sessions/{id}/messages` | yes | Append a message, creating a Job |
| `POST` | `/api/v1/sessions/{id}/archive` | yes | Archive |
| `POST` | `/api/v1/sessions/{id}/restore` | yes | Restore |
| `POST` | `/api/v1/jobs/{id}/cancel` | yes | Cancel |
| `DELETE` | `/api/v1/jobs/{id}` | yes | Delete a queued Job |
| `GET` | `/api/v1/me/attention` | yes | Everything waiting for the user |
| `POST` | `/api/v1/validations/{id}/resolve` | yes | Approve or deny |
| `POST` | `/api/v1/user-input/{id}/resolve` | yes | Answer a question |
| `GET` | `/api/v1/projects/{id}/tasks` | yes | Tasks of a Project |
| `POST` | `/api/v1/projects/{id}/tasks` | yes | Create a Task |
| `GET` | `/api/v1/projects/{id}/tasks/ready` | yes | Tasks whose dependencies are done |
| `PATCH` | `/api/v1/tasks/{id}` | yes | Retitle, or change the status |
| `GET` | `/api/v1/projects/{id}/decisions` | yes | Decisions of a Project |
| `POST` | `/api/v1/projects/{id}/decisions` | yes | Record a Decision |
| `GET` | `/api/v1/projects/{id}/search` | yes | Search the project history |
| `GET` | `/api/v1/projects/{id}/artifacts` | yes | Artifacts of a Project |
| `POST` | `/api/v1/projects/{id}/artifacts` | yes | Upload an Artifact |
| `GET` | `/api/v1/artifacts/{id}` | yes | Artifact metadata |
| `GET` | `/api/v1/artifacts/{id}/content` | yes | Artifact bytes, always as an attachment |
| `DELETE` | `/api/v1/artifacts/{id}` | yes | Remove an Artifact and its bytes |
| `GET` | `/api/v1/projects/{id}/skills` | yes | Core-managed Project Skills |
| `POST` | `/api/v1/projects/{id}/skills` | yes | Install a Skill from git, an archive URL or an upload |
| `DELETE` | `/api/v1/skills/{id}` | yes | Uninstall a Skill |
| `GET` | `/api/v1/backends/{id}/skills` | yes | Skills that exist only on that backend |
| `GET` | `/api/v1/sessions/{id}/policy` | yes | Effective ExecutionPolicy |
| `PUT` | `/api/v1/sessions/{id}/policy` | yes | Set the Session ExecutionPolicy |
| `GET` | `/api/v1/me/audit` | yes | Audit trail of policy changes and decisions |
| `GET` | `/api/v1/events` | yes | SSE, the global event stream |

Any other `/api/v1` route answers `501 Not Implemented` rather than an empty
result, so no client mistakes a missing endpoint for empty data.

`POST /api/v1/backends/register` is the one route a backend calls before it has
any user credential. It is gated by a one-shot registration token or by the
shared registration key, never by user authentication, and returns the
persistent credential once.

Commands clients may realistically retry — `sessions/start` and
`sessions/{id}/messages` — accept an `Idempotency-Key` header, so a retry returns
the Session or Job the first attempt created (section 27).

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

## Invariants to preserve

Two properties shape every endpoint above:

- **Opening a Session returns a snapshot**, a recent window of history, the
  pending attention items and the current cursor. A client never replays the
  whole event log to rebuild state.
- **Pending attention is current state**, not an unread-event counter. Once a
  validation or input request is resolved, it disappears everywhere, and a
  reconnecting client never turns old events into stale notifications.

## SSE

`GET /api/v1/events` is the user-wide stream. Reconnection uses the global event
sequence as cursor, through `Last-Event-ID` or an equivalent query parameter, so
a client holds one cursor rather than one per Session.

The Core HTTP server runs with no write timeout, because SSE responses are
long-lived by design.
