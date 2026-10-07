# Client API

Clients — Web, Android, VS Code, and the Voice-facing services — use HTTP/JSON
for commands, queries, snapshots and history, and SSE for realtime events
(specification section 5). There is no GraphQL and no WebSocket in V1.

## The contract

[`api/openapi.yaml`](../api/openapi.yaml) is this API, and Core serves it at
`/api/spec.yaml` and `/api/spec.json` without authentication: a generator or a
person looking for the right route should not need a credential to find out
what exists.

It is the contract rather than a description written afterwards.
`TestEveryRouteIsInTheOpenAPIDocument` compares it against the routes the router
actually registers and fails in both directions, and the TypeScript client is
generated from it, so a renamed field breaks a build rather than a screen.

## What exists

The contract lists every route, with its parameters, bodies and responses.
There is deliberately no table of them here: a second list is a second thing to
keep in step, and this one would be the one that drifts.

```sh
curl $CORE/api/spec.yaml          # read it
curl $CORE/api/spec.json | jq     # or query it
```

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
- `oidc` — a bearer token issued by an OIDC provider such as Keycloak. Core
  discovers the issuer at startup, so a misconfiguration fails where an operator
  can see it, and verifies the signature, issuer, audience and expiry of every
  token. Core never runs a login flow: a client obtains its token from the
  provider and presents it here. `THREAVIA_AUTH_OIDC_USER_CLAIM` selects the
  claim that identifies the user, `sub` by default because it is the only claim
  an issuer guarantees to be stable and unique.


## Working directories

A Session may name a KnownDirectory: a logical directory of the Project, such as
`puppet`. Where it actually lives is per-backend, so Core stores a binding of
`(directory, backend) → path`.

A Session whose directory has no binding on the chosen backend is accepted, not
refused. The backend resolves it when the Job starts (section 11):

1. it looks for the directory under its configured discovery roots;
2. finding exactly one, it uses it and records the binding, so the next Job
   costs nothing;
3. otherwise it asks the user, offering a clone when the directory has a git
   remote and is simply not here yet;
4. the agent is never started in a guessed directory.

Discovery roots constrain where the backend looks. They are not a sandbox:
filesystem access is the real OS permissions of the backend account
(sections 11 and 28).


Moving a Session to another backend is explicit and answered on its own: the
timeline stays continuous, a new Run starts there, and the response names the
backend-local Skills the new machine does not have. The native provider session
does not travel — it belongs to the machine holding it — so the next message
starts a fresh one. Core refuses the move while a Job is still running rather
than leaving work behind on a backend nobody is watching.

## Notification relevance

Every command may declare which kind of client sent it, with the
`X-Threavia-Channel` header (`web`, `android`, `vscode`, `voice`, …). The SSE
stream accepts the same value as a `channel` query parameter, because a browser
`EventSource` cannot set a header. An absent or malformed value becomes `api`
and changes nothing else.

Core records that channel on the Job it creates, and each pending attention item
carries it back as `originChannel` together with a `notify` hint. `notify` is
false when the client that started the work still holds a live stream: it is
already showing the event, so an OS notification would only repeat it
(section 6). Core itself pushes nothing; it says what is worth pushing.

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

Frames are **named**, so a client listens per type rather than on the default
`message`:

| Frame | Carries | Persisted |
| --- | --- | --- |
| `event` | One timeline event, with its `id` set to the global sequence | yes |
| `ephemeral` | A liveness signal with no sequence: the agent is still working | no |

An `ephemeral` frame never advances the cursor and never belongs in history. It
says something is happening between two things worth remembering, and a client
should let it expire rather than keep claiming work is in progress.

The Core HTTP server runs with no write timeout, because SSE responses are
long-lived by design.

## What a Job cost

`job.completed` and `job.failed` carry a `usage` object: input and output
tokens, the cache halves kept apart, and the cost the provider reported.

It is absent when the backend reported nothing, and that absence is meaningful:
a Job whose accounting is unknown must not read as a Job that cost nothing, so a
client shows nothing there rather than zeroes.

The shape is provider-neutral. Core stores it and shows it; it prices nothing
itself, and a provider that counts differently fills what it can.

A Job's duration is not in the payload, because the timeline already holds both
ends: `job.started` and the terminal event carry timestamps, and asking the
server for the difference would be slower and no more true.
