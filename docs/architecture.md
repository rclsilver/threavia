# Architecture notes

This document records the state of the implementation and every choice the
specification deliberately left open. The architecture itself is defined in
[`THREAVIA_SPEC_V1.md`](../THREAVIA_SPEC_V1.md) and is not restated here.

## The rule that shaped the code

The repository started as the compilable skeleton of specification section 35,
became the MVP vertical slice of section 30 and now covers the V1 surface. One
rule held throughout, and still does: **the structure is real, the behaviour is
either implemented or explicitly absent, never faked**.

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

## How the pieces work

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

### ExecutionPolicy

Section 17 is explicit that a policy is enforced rather than suggested: one
forbidding a push rejects the push instead of asking the model not to. So the
policy travels with every dispatched Job and is evaluated in the backend
permission gate, before anything reaches the user. A forbidden action is refused
outright, with a reason that tells the agent the refusal is structural rather
than a user who happened to say no.

The three modes differ only in who is asked about what. INTERACTIVE asks about
everything; GUARDED lets reading through and asks about anything that changes
something; AUTONOMOUS stops asking but does not stop refusing — the capability
flags still apply, which is what makes the mode safe to offer at all. An
unclassified tool or an unrecognised shell command counts as mutating, because
guessing the other way is how a guard rail stops being one.

Two limits cannot be seen by any gate, so the runner enforces them: wall-clock
duration and a count of actions. A Run that asks for nothing can still run
forever, and a validation refused on those grounds would never arrive. Core
refuses an AUTONOMOUS policy that sets neither, since that is the one
combination the section exists to prevent.

What this is not is a sandbox. The gate reads a command as written, so an agent
determined to evade it could. That has never been Threavia's model: filesystem
and process access are the real permissions of the account the backend runs as,
and section 28 says in as many words not to mistake Threavia metadata for a
security boundary.

### The audit trail

Validation receipts are already events, but an event belongs to a timeline that
a project deletion takes with it. The `audit_entries` table of section 23 outlives
its subject deliberately: its identifiers are plain text rather than foreign
keys, so the record of a decision survives the deletion of what was decided
about. It records who decided what, through which client, over which canonical
payload hash — and every change to an execution policy, because loosening what
an agent may do is exactly the kind of act such a trail exists for.


### What the schema does differently from section 23

Three tables the specification lists are not tables here, and one it does not
list is.

`users` is absent because Core never stores a user: the identity comes from the
authenticator, and `ownerId` is whatever that mode produces — a configured
development id, a basic username, or an OIDC claim. A table would only duplicate
the issuer.

`project_instructions` is a column on `projects`, because it holds exactly one
value per project and a table of one-row-per-project is a join for nothing.

`project_skills` is absent because `skills` carries `project_id` directly: a
Skill is installed on a Project, and V1 has no sharing of Skills between
Projects for the join table to express.

`backend_registration_tokens` is the addition. One-shot registration tokens have
to be single-use and expiring (section 8), which is state, and state belongs in
a table rather than in a process.

### Skills, and what Core refuses to do with them

Core acquires a Skill, inspects it, repacks it deterministically and stores the
bundle. It never runs anything the source contains — no installer, no build
step — because the only thing that should ever execute a Skill is an agent, on a
backend, under that backend's own permissions. Section 18 says as much; what it
costs is that a Skill needing a build step has to be built before it is handed
to Threavia.

The pack is deterministic — sorted entries, fixed timestamps — so the same
content always yields the same checksum. That is what lets a backend tell a
cached copy from a stale one without asking, and what makes re-acquiring an
unchanged Skill visibly a no-op.

A bundle travels to a backend over the control stream rather than through a
presigned URL, because a backend holds a gRPC credential and nothing else: it
has no user identity with which to call the HTTP API, and giving it one would
make a machine a user. The fetch therefore happens on the goroutine that runs
the Job, never the one reading the stream, or the reply it waits for could not
arrive.

On the backend, the effective Skills of a Run — the Core-managed ones plus the
backend's own — are assembled into a per-Job plugin directory of symlinks and
handed to Claude Code with `--plugin-dir`. It is the one mechanism that adds
Skills for a single run without writing into the user's repository or into
their own Claude Code configuration, and a Run that leaves provider
configuration behind would be a Run that changed the machine.

Instructions take the same route. Section 18 says the adapter maps them to
provider mechanisms such as `CLAUDE.md`; this adapter maps them to the system
prompt instead. Writing a `CLAUDE.md` into someone else's repository is a
filesystem change nobody asked for, and the system prompt reaches the agent
without one.

### What the channel of a request is for

Section 6 asks that work someone is following not also make an unrelated device
ring. Core cannot push anything — external notification delivery is a non-goal
of section 33 — so what it can do is say what is worth pushing.

Every command may declare its client with an `X-Threavia-Channel` header, which
the HTTP layer stamps onto the request context. The channel is recorded on the
Job it creates, and every pending attention item carries it back with a `notify`
hint, false while that client still holds a live stream. It is also what the
audit trail records as "from where".

A context value rather than a parameter on every operation: it is request-scoped
metadata about who is calling, which the audit trail and the relevance hint both
want and which no business operation reasons about.

### Moving a Session between backends

A Run is the binding between a Session and one BackendInstance, which is exactly
what a backend change needs: a new Run, the same Session, the same timeline.

The native provider session does not travel, because it belongs to the machine
holding it. The move is refused while a Job is still running rather than leaving
work behind on a backend nobody is watching, and the answer names the
backend-local Skills the new machine does not have. Core never held the content
of those Skills — that is the point of the split in section 18 — and can still
say that moving the work loses them.

Automatic migration and backend scoring stay out: section 33 defers them, and a
backend change the user did not ask for is the opposite of the explicit
transition section 34 wants.

### The web client, and why it is shaped this way

Three properties of the product decided the stack, not familiarity with it.

**One SSE stream feeds every view.** A snapshot seeds the state, a cursor takes
over, and a validation resolved on another device has to disappear here at
once. That is a shared-cache invalidation problem rather than a data-fetching
one, so TanStack Query holds the cache and the stream is its only other writer:
one subscriber patches the cache and every mounted view follows. The alternative
— each view polling, or each view holding its own copy — is how a multi-client
product ends up showing two different truths at the same time.

**The timeline is append-only and unbounded.** It is virtualised, so a Session
with thousands of events stays a conversation rather than a frozen tab. Agent
output is markdown with code in it, so it is parsed once per message and
memoised, and highlighted by a grammar loaded on demand: a Session with no code
in it pays for no highlighter, and one with Go in it does not also load Wolfram.

**The API contract is generated, not transcribed.** `api/openapi.yaml` is the
source of truth: Core serves it, the TypeScript types come from it, and
`TestEveryRouteIsInTheOpenAPIDocument` fails in both directions — a route the
server serves and the document omits is invisible to every generated client,
and a path the document promises and the server does not serve answers 501. A
field renamed in Go breaks the web build rather than a user's screen.

The development loop was the other requirement. Vite serves the client and
proxies everything else to Core, so editing a component reloads the browser
without rebuilding or restarting the Go binary, and the browser stays on a
single origin: no CORS, and the event stream behaves exactly as it does in
production. In a release the built files are embedded, so a deployment is one
binary with no asset directory to keep in step with it. A binary built without
the client says so on the page, because a blank tab is the worst way to learn
that a build step was skipped.

### Running the client against Core in development

`make dev-web` puts Vite in a container on the host network rather than on a
bridge. Reaching a host service from a bridge network means
`host.docker.internal`, a `host-gateway` mapping and a firewall that allows the
docker interface — three things to get right on every machine, and on a
firewalled Linux host the default is that none of them work. Sharing the host
namespace has nothing to get right: Vite listens on `:5173` and reaches Core at
`localhost:8080` exactly as a process on the host would.

Core stays on the host deliberately. It is what a Go developer needs a terminal,
a debugger and a rebuild loop for, and moving it into compose to simplify the
client's networking would complicate the thing people work on more.

`THREAVIA_CORE_URL` points the proxy elsewhere, and `make web-dev` runs the same
server with no container at all.
