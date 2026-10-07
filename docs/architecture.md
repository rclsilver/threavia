# Architecture notes

This document records the state of the implementation and every choice the
specification deliberately left open. The architecture itself is defined in
[`THREAVIA_SPEC_V1.md`](../THREAVIA_SPEC_V1.md) and is not restated here.

## The rule that shaped the code

The repository started as the compilable skeleton of specification section 35
and grew into the MVP vertical slice of section 30. One rule held throughout,
and still does: **the structure is real, the behaviour is either implemented or
explicitly absent, never faked**.

What that looks like in practice:

- a `/api/v1` route that does not exist yet answers `501 Not Implemented` rather
  than an empty collection, so a client never mistakes absence for emptiness;
- Core acknowledges a backend event only once it is durably persisted, so an
  event it could not record stays buffered on the backend instead of vanishing;
- a Core Tool invocation is answered with an explicit error rather than dropped,
  so an agent never waits on a reply that will not come;
- a command a backend cannot honour is rejected, so Core never believes work
  started when it did not.

## Choices made where the specification was open

### Repository and module

| Choice | Value | Why |
| --- | --- | --- |
| Go module | `github.com/rclsilver/threavia` | Section 25 leaves the module path to the chosen repository host. |
| Go version | 1.26 | The lowest version the pinned dependencies support, driven by modernc.org/sqlite. |
| License | Apache-2.0 | Permissive with an explicit patent grant, the norm for Kubernetes-adjacent infrastructure. |

### Additions to the proposed tree

Section 25 proposes a layout; four additions were needed.

- **`gen/`** holds the generated Go bindings. Section 25 asks for a
  deterministic, tooling-driven location; generating into `api/proto` alongside
  the sources would make `buf generate --clean` destroy the `.proto` files.
- **`internal/core/config`** owns environment loading for Core. It composes the
  configuration structs the storage and auth packages define themselves, so no
  package depends on the configuration loader.
- **`internal/envutil`** holds the environment parsing helpers shared by the
  Core and backend configuration loaders, which would otherwise be duplicated.
- **`internal/logging`** holds the structured logger builder, so the Claude
  backend does not have to import a Core package to get a logger.
- **`web/`** serves the client as embedded assets, so Core ships as one binary.

### Dependencies

Six direct dependencies: `grpc`, `protobuf`, `pgx/v5`, `golang-migrate`, `uuid`
and `x/sync`. HTTP routing uses the standard library `net/http` method patterns,
and configuration parsing is hand-written: neither warranted a dependency.

`golang-migrate` was chosen over `goose` because its dependency tree is far
smaller: `goose` pulls ClickHouse, SQL Server, Vertica, YDB and SQLite drivers
Threavia does not use.

### Database schema

The first migration creates exactly the tables section 35 asks for: `projects`,
`backend_instances`, `sessions`, `runs`, `jobs` and `events`.

- **No `users` table.** `owner_id` is an opaque identifier produced by the
  configured authentication mode. Modelling user storage before the
  authentication implementation lands would commit to a design prematurely.
- **`sessions.working_directory_id` has no foreign key yet.** The column exists
  because a Session has an optional logical working directory, but
  `known_directories` is not modelled in this slice; the constraint is added
  with that table.
- **`runs.backend_instance_id` is `ON DELETE RESTRICT`.** BackendInstances are
  user-owned and are not deleted with a Project (section 21).
- **One active Job per Run is a partial unique index**, over the statuses
  `JobStatus.Active` reports. The invariant of section 3.4 is enforced by the
  database, not by application discipline.
- **Backend event deduplication is `UNIQUE (backend_instance_id,
  backend_event_id)`.** PostgreSQL treats `NULL`s as distinct, so
  Core-generated events, which have neither, are unaffected.
- **`events.global_sequence` is an identity column.** It is monotonic, as
  section 4 requires. Two consequences are worth knowing before the SSE stream
  is built: a rolled-back insert consumes a value, so the sequence has gaps, and
  under concurrency a transaction holding a lower sequence can commit after a
  higher one. The event ingestion path has to account for both; a cursor-based
  catch-up must not assume contiguity.

### Backend authentication

Section 8 defines two registration flows and both are implemented. A backend
presents a persistent credential on `Connect`, and Core stores only its SHA-256:
it can verify what a backend presents but never reproduce it.

A one-shot user token, from `POST /api/v1/backend-tokens`, owns the instance
immediately. The shared registration key instead creates an UNCLAIMED instance
plus a one-time claim code that the backend prints locally and a user enters
once. A revoked instance never resolves again, so a returning backend has to
register anew.

A backend stores its identity locally once registered, which is why registration
material is never needed at a later start.

### Job state machine

The transitions of section 3.5 leave a few cases implicit; the implemented table
resolves them like this:

- a QUEUED Job has nothing running on a backend to confirm a stop, so it is
  cancelled directly, without passing through CANCELLING;
- CANCELLING may still resolve to COMPLETED or FAILED, because the backend is
  the source of truth for what actually happened locally (section 9);
- WAITING_BACKEND may resolve to any outcome, for the same reason: the backend
  may have finished the work while it was disconnected.

Each of these is pinned by a test in `internal/core/domain`.

### Shutdown

gRPC's graceful stop waits for in-flight RPCs, and the backend control stream is
by design long-lived, so Core first ends every control connection and only then
stops the server. A backend treats it as a normal disconnection and reconnects.
Without this, every Core shutdown would take the full shutdown timeout.

## Layering rules

- `internal/core/domain` imports nothing from Threavia and knows no provider.
- Core never imports a provider-specific package; the Claude translation is
  confined to `internal/backends/claude`.
- `pkg/backend-sdk` is provider-independent: a backend learns the Core Tools it
  may call from the `ProjectContext` sent at Job start, and never hardcodes
  their names.
- The configuration structs live with the component they configure;
  `internal/core/config` only loads them from the environment.

### Backend durable state

The Go SDK stores its local execution state in SQLite, as specification
section 10 suggests, through `modernc.org/sqlite`: a pure Go driver, so a
backend binary needs no C toolchain and cross-compiles like any other Go
program. It is what sets the Go 1.26 floor.

The schema holds the persistent identity, the known Runs and their native
sessions, the per-Job sequences and the unacknowledged events. Nothing in it is
part of the wire protocol contract: a backend written in another language stores
its state however it likes, as long as it honours the same guarantees — a
monotonic per-Job sequence that survives a restart, and events kept until Core
acknowledges them.

## The MVP slice

### Job dispatch

A Job is created QUEUED and dispatched separately, so enqueueing never depends on
backend availability: a message sent to an offline backend waits in the queue
rather than failing.

Dispatch reserves the single active slot of the Run by transitioning the Job to
RUNNING *before* sending the command. That is what keeps a second Job of the same
Run from being dispatched concurrently, and the partial unique index in the
schema makes the reservation atomic. If the send then fails, the Job is parked
in WAITING_BACKEND rather than lost.

Core emits no event for its own optimistic transition: the backend's
`job.started` is the one that reaches the timeline, because it carries the
backend identity and sequence that make it deduplicable.

### Reconnection, and the rule that prevents running work twice

Core parks every live Job as WAITING_BACKEND when a control stream drops,
including during its own clean shutdown. The tempting next step — releasing those
Jobs when the backend comes back — is wrong, and a real run through a Core
restart proved it: a backend that had finished the work during the outage ran it
a second time.

Section 9 settles it. Core owns the desired logical state, the backend owns what
actually happened locally. So:

- connecting releases only Jobs that were never dispatched anywhere;
- a Job the backend reports, in any state, converges through its replayed
  events, never by being dispatched again;
- only a Job the backend has no memory of is sent out a second time, because
  then nothing is running anywhere.

### Attention objects

A ValidationRequest and a UserInputRequest are rows, not messages in flight. They
are created in the same transaction that persists the event announcing them, they
have no timeout, and resolving one is a single conditional UPDATE so the first
valid response wins and a second client is told it is already resolved. A Job
that ends with requests still pending has them resolved as abandoned, so no ghost
prompt survives it.

The canonical technical payload is hashed with SHA-256 over a deterministic JSON
encoding — a Go map through `encoding/json`, which sorts object keys — so the
receipt of section 16 references a stable byte sequence.

### Claude Code integration

Claude Code runs in print mode with `stream-json` output, which the runner
normalises into the Threavia vocabulary. Three decisions are worth recording:

- **The backend mints the provider session id** with `--session-id` instead of
  discovering it, so a Run is resumable from its very first message; a second Job
  passes `--resume`.
- **Permission prompts travel over MCP.** The backend hosts a loopback MCP server
  and points Claude Code at it with `--permission-prompt-tool`, so a prompt that
  would block a terminal becomes a ValidationRequest and waits in Core. The same
  server exposes an `ask_user` tool for questions. Each Job gets its own
  unguessable endpoint path, and a request that cannot be answered is denied, not
  approved.
- **The provider runs in its own process group**, so cancelling stops the
  children it spawned, and a cancelled Job reports CANCELLED rather than whatever
  the dying process said on its way out.

### Web client

Section 25 leaves the frontend technology open. The client is a single page of
plain HTML, CSS and JavaScript embedded in the Core binary: no build step, no
bundler, no framework, and therefore no second toolchain to install or keep
current. It holds no state of its own — a snapshot plus the global event stream
is the whole model — which is exactly what the specification asks a client to
be, and it keeps Core deployable as one binary.

### Backend capabilities

Capabilities are declared at registration, not only in the Hello frame, and every
Hello refreshes them. Without that, Core could not queue work for a backend that
is currently offline, since a backend that never connected would advertise
nothing.

### Local object storage

`make dev-up` starts only PostgreSQL, because the first vertical slice runs
without object storage. `make dev-up-storage` adds Garage behind a compose
profile and configures it: a Garage node ships unconfigured and stores nothing
until a cluster layout is applied, so the init script assigns the layout, creates
the bucket and the key, and prints the credentials. It is idempotent.

Garage is a local convenience, not a dependency: Core talks to a generic
S3-compatible endpoint, and the Helm chart deploys no storage at all.

### Inferring OFFLINE

Section 7 says Core infers OFFLINE "from missing heartbeat/connection", and
three different failures hide behind that phrase.

A clean disconnection ends the stream, and Core notices immediately. A dead
peer — host gone, network black hole, frozen process — stops acknowledging the
gRPC keepalive pings, and the transport tears the connection down by itself;
measured against a backend frozen with SIGSTOP, this is what fires, and faster
than any application deadline. The third case is the one that needs code: a
backend whose transport is healthy and answering pings while the application
behind it has stopped, through a wedged event loop or an implementation that
simply never heartbeats. Nothing below the application layer can see it, and
without a deadline the instance would stay READY forever while Core kept
dispatching work into a hole.

So a watcher sweeps the live connections at the heartbeat interval and closes
any that has said nothing for `THREAVIA_BACKEND_OFFLINE_AFTER`. Closing is all
it does: the ordinary disconnection path then marks the instance OFFLINE and
parks its Jobs, and the backend reconnects and reconciles. A connection has one
way to end rather than two, and the backend is told to come back rather than
that something went wrong.

A connection that has only just opened is given the same grace as one that has
been heartbeating, so a backend is never killed before it has had a chance to
speak.

### Core Tools

Section 12 declares ten provider-independent tools. Core owns them; the backend
adapts them to whatever tool mechanism its provider has, and hardcodes none of
their names: it receives their specifications in the ProjectContext at Job start
and forwards them to the local MCP endpoint untouched. Core can therefore add a
tool without a backend release.

A tool call arrives scoped to a Job, and that is what gives it an identity. The
Project it may touch and the user it acts for are read from that Job, never from
the request, and every tool taking an identifier re-checks that the target
belongs to that Project. The MCP endpoint refuses any name that was not declared
for the Job, so it never becomes a general proxy into Core.

Two choices are worth recording. Completing a task answers with what it
unblocked, because the dependency graph exists to answer exactly that and making
the agent ask again wastes a turn. And the tool descriptions are written for the
agent that reads them: they say when to reach for a tool, not what it does
mechanically, because that is the only instruction an agent gets.

### What a Run context carries

Section 12 asks for a compact structured context, so the budget is spent
deliberately: active IMPORTANT decisions only, a bounded summary of what is open
and actionable, and the tool specifications. A superseded or NORMAL decision is
searchable but never injected — injecting the first would assert something no
longer true, and the second would spend context on something nobody marked
important.

Everything else is found on demand, which is why the search tools exist at all.
