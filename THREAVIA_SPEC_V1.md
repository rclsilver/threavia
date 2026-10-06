# Threavia — V1 Architecture & MVP Specification

> **Status:** initial implementation specification  
> **Purpose:** handoff document for Codex / Claude Code / human contributors.  
> **Project name:** **Threavia** — *thread + via*: a continuous work thread across agents, devices and backends.

## 1. Product goal

Threavia is a self-hosted, Kubernetes-friendly control plane for coding agents. It owns durable projects, sessions, jobs, events and project knowledge while execution remains on autonomous **BackendInstances** such as a laptop running Claude Code or a Kubernetes pod running Codex.

The same logical Session must be usable from Web, Android, VS Code and Voice/SIP. Clients are stateless views/controllers of Core state; a client disconnect must not stop agent execution.

Typical flow:

1. User starts work from Web/VS Code against a backend on a laptop.
2. The backend runs the provider-native coding agent with local credentials/filesystem/tools.
3. Core persists normalized state/events and broadcasts them to every relevant client.
4. User can later resume the same Threavia Session from Android/Voice/another client.
5. A future explicit backend change creates a new Run while keeping the same user-visible Session/timeline.

## 2. V1 principles

- **Core owns platform state; Backend owns execution.**
- Provider credentials never live in Core.
- Session is provider/backend independent.
- Run is the binding between a Session and one BackendInstance/native provider session.
- Run is mostly an infrastructure detail and should be nearly invisible in normal UX.
- Project memory is project-scoped; no global memory in V1.
- No automatic backend scheduling, migration or failover in V1.
- Explicit behavior over magic.
- PostgreSQL stores current state, metadata and persistent events.
- S3-compatible object storage stores blobs/Artifacts.
- This is **not** a full event-sourced architecture: current state is stored directly in relational tables.

## 3. Domain hierarchy

```text
Project
└── Session
    └── Run
        └── Job
            └── Event
```

Additional project-level objects include Decisions, Tasks, KnownDirectories, Skills and ProjectInstructions.

### 3.1 Project

A logical body of work, e.g. `homelab` or `ink8s-operator`.

Properties/behavior:

- belongs to one user in V1 (`ownerId`)
- groups Sessions
- owns Decisions, Tasks, KnownDirectories, Core-managed Skills and ProjectInstructions
- may span multiple Git repositories/directories
- ACTIVE or ARCHIVED
- explicit permanent deletion is distinct from archive

### 3.2 Session

The long-lived user-visible conversation/work context.

Properties/behavior:

- belongs to a Project
- provider/backend independent
- does **not** contain a provider native session ID
- title is automatically generated but user-renamable; title has no technical meaning
- ACTIVE or ARCHIVED
- no automatic TTL in V1
- has an optional logical `workingDirectory` (preferably a KnownDirectory)
- `workingDirectory` is only the initial/main cwd, **not** a filesystem boundary
- an agent may work in any other directory permitted by the backend filesystem
- changing Session `workingDirectory` is explicit; a temporary `cd` does not mutate it
- multiple clients can observe/control the same Session concurrently

### 3.3 Session creation semantics

Opening “New session” in a client creates only a **local draft**. Nothing is persisted in Core until the first message is sent.

Example draft:

```yaml
projectId: homelab
backendInstanceId: claude-personal
workingDirectory: kd-puppet # optional
message: "Analyse ce projet"
```

The first send should be one logical/atomic Core operation (working name: `StartSession`) that creates:

1. Session
2. first Run
3. first Job
4. first user message/event

This avoids empty/ghost Sessions and partially-created state.

### 3.4 Run

An execution incarnation of part of a Session on one BackendInstance/provider native session.

Properties:

```yaml
id: ...
sessionId: ...
backendInstanceId: ...
nativeSessionId: ... # nullable until known
resumeStatus: UNKNOWN | AVAILABLE | UNAVAILABLE
resumeReason: ... # optional
```

Rules:

- backend choice lives on Run, not Session
- first Run is created on first message
- changing backend/provider is explicit and creates a new Run
- no automatic migration/failover
- a Run remains reusable after a Job finishes
- native session existence is validated by Backend at actual resume time
- native session disappearance does not delete Core history
- if unavailable, continuation uses a new Run plus Core-owned handoff context
- a Run has at most one active Job and may have multiple queued Jobs

### 3.5 Job

A complete unit of agent work from user input until completion/error/cancellation/waiting interactions.

Statuses:

```text
QUEUED
RUNNING
WAITING_INPUT
WAITING_VALIDATION
WAITING_BACKEND
CANCELLING
COMPLETED
FAILED
CANCELLED
```

Rules:

- Job continues if initiating client disconnects
- no timeout for WAITING_INPUT / WAITING_VALIDATION
- one active Job per Run; queued Jobs FIFO by default
- queued Jobs can be deleted; reorder may be supported
- no priority/dependency scheduler for Jobs in V1
- cancel transitions RUNNING → CANCELLING → CANCELLED only after backend confirms stop
- if backend is offline while cancelling, remain CANCELLING and reconcile on reconnect
- no Pause in V1

Optional CODE backend features:

- `job.input.now` — immediate interruption/reorientation
- `job.input.next` — inject instruction as soon as practical after current action

Later work is a new queued Job; no `later` mode.

### 3.6 Event

Events form the observable timeline and realtime synchronization mechanism.

Messages are Events too (`user.message`, `agent.message`); do not create a competing message-history system.

Representative persistent event types:

```text
session.created
session.renamed
session.archived
session.restored
run.created
job.created
job.started
user.message
agent.message
tool.started
tool.completed
tool.failed
validation.requested
validation.resolved
user_input.requested
user_input.resolved
task.created
task.updated
decision.created
decision.superseded
working_directory.changed
job.completed
job.failed
job.cancelled
backend.registered
backend.revoked
```

Ephemeral events such as heartbeat, temporary progress or “agent working” may be streamed without permanent retention.

## 4. Event identity, ordering and delivery

### Core event envelope

Conceptual JSON representation exposed to clients:

```json
{
  "id": "evt_...",
  "sequence": 1843,
  "timestamp": "2026-10-06T20:07:00Z",
  "type": "validation.requested",
  "projectId": "...",
  "sessionId": "...",
  "runId": "...",
  "jobId": "...",
  "payload": {}
}
```

IDs are nullable where the event scope does not include that object.

### Global sequence

Core assigns a monotonically increasing **global sequence** to persistent observable events. This is the cursor for the user's global SSE stream.

Example:

```text
1841 session-A agent.message
1842 session-B job.completed
1843 session-A validation.requested
```

A client can reconnect after sequence `1842` and catch up without maintaining a cursor per Session.

### Backend event identity

Backend-generated events have a stable backend-generated ID:

```text
(backendInstanceId, backendEventId) UNIQUE
```

This makes backend delivery at-least-once while Core observation is deduplicated.

### Backend Job sequence

Backend events also carry a monotonic sequence **per Job**. It is used to detect missing replay segments without serializing unrelated concurrent Jobs.

```text
backendEventId   → deduplication
backendSequence  → ordering/replay within a Job
globalSequence   → Core-wide client cursor/order
```

## 5. Client API: HTTP/JSON + SSE

Clients (Web, Android, VS Code, Voice-facing services) use:

- **HTTP/JSON** for commands, queries, snapshots and history
- **SSE** for Core → client realtime events

Do not add GraphQL or WebSocket in V1 unless a demonstrated requirement appears.

Representative HTTP endpoints (exact paths are not frozen):

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

SSE reconnect uses the global sequence (`Last-Event-ID` semantics or equivalent cursor).

### Client initial load

A client opening a Session receives:

1. current snapshot/state
2. a recent window of significant history
3. pending attention items
4. current global/event cursor
5. live SSE events thereafter

Older history is loaded by scrolling/pagination. A client never needs to replay the entire Event log to reconstruct current state.

## 6. Multi-client semantics and attention

Multiple clients may simultaneously observe/manipulate a Session. Core arbitrates mutations atomically; e.g. first valid response to a pending ValidationRequest wins.

Historical Events never disappear, but **pending attention is current state**, not an unread-event counter.

Persistent actionable objects include at least:

- ValidationRequest
- UserInputRequest

They have states such as `PENDING` / `RESOLVED`. Once resolved on one client, every other connected client removes the pending UI/notification. A reconnecting Android client asks Core for current pending attention and must not recreate notifications from hundreds of old Events.

### Notification relevance

Do not push every interesting Event to every device.

Default principle:

- remember which client initiated work
- an active client receives live Events rather than redundant OS notifications
- if Android initiated work and is now background/inactive, completion/input/validation push can be relevant
- Voice can use Android as a companion client for visual/complex decisions
- work initiated and actively followed on Web/VS Code should not make an unrelated Android device ring

Full user-facing client/device management is post-V1 (list/revoke registered Web/Android/VS Code/Voice clients).

## 7. BackendInstance

A BackendInstance is an autonomous remote execution participant.

Examples:

```text
claude-personal-laptop
claude-work-laptop
codex-k8s
```

BackendInstance belongs to a **User**, not a Project.

It owns:

- provider credentials
- provider native sessions
- filesystem
- Git/tools
- local/private skills and instructions
- local durable recovery state

Core never holds provider credentials.

### Operational status

```text
STARTING
READY
DEGRADED
OFFLINE   # inferred by Core from missing heartbeat/connection
```

Do not use BUSY. Capacity is separate:

```yaml
capacity:
  maxConcurrentRuns: 2
  activeRuns: 1
```

`maxConcurrentRuns: 0` can be used to drain.

Kubernetes-style conditions may explain DEGRADED, e.g. provider authentication expired.

### Capabilities

Extensible capability set:

- `CODE` — V1
- `INTERACTION` — V1/early, used notably for conversational/Voice adaptation
- `REVIEW` — post-V1

A backend advertising `CODE` must support the semantic contract:

- persistent/resumable native coding session where provider supports it
- ValidationRequest
- UserInputRequest
- Core Tools integration

These are not optional CODE feature flags.

Optional CODE feature flags in V1:

- `job.input.now`
- `job.input.next`

### Provider authentication

Entirely backend-owned. Core sees only coarse state such as:

```text
AUTHENTICATED
AUTHENTICATION_REQUIRED
```

V1 login is manual/local, e.g. `claude /login` on laptop or via `kubectl exec` in a pod. Generic remote auth challenges can come later.

## 8. Backend registration and ownership

### Shared registration key

If enabled:

1. backend registers with shared key
2. BackendInstance is created UNCLAIMED
3. backend generates/displays a one-time claim code locally
4. authenticated user enters claim code once
5. BackendInstance is permanently associated with that user
6. claim code becomes invalid

### User-generated one-shot registration token

1. authenticated user creates one-shot token
2. backend registers with it
3. BackendInstance is immediately owned by token owner
4. no claim step

After registration, backend stores persistent identity/credential locally. Registration credentials are not required at each reboot. Core issues short-lived runtime credentials/tokens as needed.

### Helm shared key

If shared registration is enabled and no `existingSecret` is supplied, Helm should generate a 32-byte / 256-bit CSPRNG secret on first install and preserve it across upgrades using `lookup`. Kubernetes Secret is the source of truth.

### Revocation

- OFFLINE is connectivity state, not deletion
- REVOKE explicitly invalidates persistent backend credentials
- keep BackendInstance record as REVOKED for historical references
- returning revoked backend must register again as a new BackendInstance

## 9. Backend ↔ Core protocol

Use **protobuf/gRPC** and keep it provider/language independent.

Version advertisement concept:

```yaml
protocol:
  version: 1
sdk:
  name: go
  version: ...
backend:
  name: claude
  version: ...
```

Protocol version is the contractual compatibility boundary. Preserve protobuf compatibility; avoid elaborate negotiation in V1.

### Connection direction

Backend always initiates outbound TLS/gRPC connection to Core. Core must not require inbound connectivity to laptops/backends.

After auth, backend opens one long-lived **bidirectional gRPC control stream**.

Backend → Core:

- events
- status/conditions/capacity
- heartbeat
- reconciliation state
- command results
- Core Tool requests

Core → Backend:

- start/resume Job
- cancel Job
- validation resolution
- user input resolution
- mid-job input
- Core Tool responses
- reconciliation instructions

### Connection lease

Only one connection is active for a BackendInstance. A newly authenticated connection receives a new `connectionId`/lease and supersedes the previous connection for new commands. Old valid/idempotent events may still be accepted, but old connections receive no new work.

### Reconciliation

Backend has durable local execution/recovery state and reports active Runs/Jobs plus last backend sequence on reconnect.

Rule:

> Core is source of truth for desired logical state; Backend is source of truth for what actually happened in local execution.

Examples:

- Core says CANCELLING, backend says RUNNING → Core reissues cancel.
- Core says RUNNING through sequence 120, backend says COMPLETED through 128 → backend replays 121–128; Core converges to COMPLETED.

## 10. Backend local durable state

Every BackendInstance has local durable state. Go SDK may implement it using SQLite in V1, but SQLite is not part of the wire protocol contract.

State includes, as needed:

- persistent BackendInstance identity/credentials
- known Runs/native session IDs
- active Jobs
- backend Job sequences
- unacknowledged Events
- last Core ACK/reconciliation data

Laptop example: `~/.threavia/backend.db`  
Kubernetes backend: small persistent PVC.

PostgreSQL remains global platform truth; local SQLite is only execution/recovery truth.

If Core crashes, backend can continue provider work and buffer unacknowledged events. No Kafka/NATS dependency is required in V1.

## 11. KnownDirectory and working directory

Do not model Git repositories/worktrees as Core objects in V1.

### KnownDirectory

A project-level logical directory:

```yaml
KnownDirectory:
  id: kd-puppet
  projectId: homelab
  name: puppet
  description: "Configuration Puppet du homelab"
  gitRemote: optional
```

### Backend binding

```yaml
KnownDirectoryBinding:
  knownDirectoryId: kd-puppet
  backendInstanceId: laptop-personal
  path: /home/thomas/git/puppet
```

Same logical directory can have different physical paths on different backends.

### DiscoveryRoot

Backend configuration may define roots such as:

```yaml
discoveryRoots:
  - /home/thomas/git
  - /home/thomas/dev
```

They only constrain **automatic discovery/search**, not filesystem access/security. Explicit paths may exist outside them subject to actual OS permissions.

When a KnownDirectory binding is missing:

1. backend tries deterministic resolution
2. auto-search only configured DiscoveryRoots
3. if exact path is known, bind it
4. if unresolved, create UserInputRequest asking user
5. if absent and `gitRemote` exists, backend may offer clone then bind

Agent can explicitly register a durable directory using a Core Tool such as `known_directory_register`.

Do **not** automatically promote every directory touched by an agent into KnownDirectory.

### No Workspace object in V1

The earlier Workspace abstraction was deliberately removed. A Session has one optional logical `workingDirectory`/cwd. The agent may still traverse/work in many other known or unknown directories.

## 12. Project knowledge and Core Tools

Core provides provider-independent tools to agents. Backend SDK adapts them to provider-specific mechanisms.

Candidate tools:

```text
task_create
task_search
task_update
task_complete
task_ready
decision_create
project_history_search
known_directory_register
known_directory_bind
working_directory_set
```

Do not inject giant project histories into provider prompts.

At new Run, Core builds a compact structured ProjectContext containing:

- Project identity/description
- Session workingDirectory and resolved path when applicable
- active IMPORTANT Decisions
- compact summary of relevant open Tasks
- instructions describing available Core Tools

Do not inject all Jobs/Events/files/completed Tasks/NORMAL Decisions. Everything else is searchable on demand.

## 13. Decisions

```yaml
Decision:
  id: ...
  projectId: ...
  title: ...
  content: ...
  importance: IMPORTANT | NORMAL
  status: ACTIVE | SUPERSEDED
  supersedes: decisionId? # optional
  createdAt: ...
  updatedAt: ...
```

Rules:

- NORMAL is default
- active IMPORTANT decisions are automatically included in new Run context
- NORMAL decisions are searchable
- a new Decision may supersede an older Decision
- superseded Decisions remain historical but are not current/injected by default

## 14. Tasks

```yaml
Task:
  id: ...
  projectId: ...
  title: ...
  description: ...
  status: TODO | IN_PROGRESS | DONE
  createdAt: ...
  updatedAt: ...
```

Dependencies are graph edges, not hierarchy:

```yaml
TaskDependency:
  taskId: task-c
  dependsOnTaskId: task-b
```

Rules:

- multiple dependencies supported
- Core rejects dependency cycles
- no stored BLOCKED status; blocked is derived from incomplete dependencies
- `task_ready` returns TODO tasks whose dependencies are DONE
- optional many-to-many Job ↔ Task relation is useful
- no priority/deadline/subtask/assignment system required initially

## 15. Project memory/history

Two layers:

1. explicit structured knowledge: Decisions + Tasks
2. historical/episodic knowledge derived from Jobs/Events

V1 historical search uses PostgreSQL full-text search. Do not require embeddings/vector DB in V1.

No separate Episode object is needed.

## 16. Validation and user input

These are persistent Core objects, not transient-only Events.

### ValidationRequest

Provider-independent representation of a permission/approval request. It should contain enough structured/raw detail for a technical client while allowing an INTERACTION backend to humanize it for Voice.

Core sets Job to WAITING_VALIDATION while pending. First valid atomic resolution wins; all clients receive `validation.resolved`.

### UserInputRequest

Distinct from validation:

- ValidationRequest = permission/approval
- UserInputRequest = answer/choice/information needed

Core sets Job to WAITING_INPUT while pending.

### Validation receipt

V1 audit receipt:

- validationId / jobId
- decision (approved/rejected)
- actor user ID
- client/channel
- timestamp
- SHA-256 of canonical technical request payload

Optional humanized presentation can be stored for audit convenience, but the canonical technical request/hash is the security reference.

## 17. ExecutionPolicy

Simple and extensible in V1. Session default with optional Job override.

Modes:

```text
INTERACTIVE
GUARDED
AUTONOMOUS
```

Autonomous execution is constrained by explicit permissions/limits such as duration, maximum actions/jobs, filesystem writes, git commit/push, network/deployment access.

Policies must be enforced by Core/backend where possible, not only by prompt. Example: `gitPush=false` rejects push rather than merely asking the model not to do it.

## 18. Skills and instructions

### Core-managed Project Skills

Canonical skill format should follow Agent Skills-style directories (`SKILL.md` + scripts/references/assets) where feasible. Exact cross-provider compatibility with Claude Code/Codex/Gemini must be verified before freezing the contract.

V1 Skill Manager should support direct safe acquisition from:

- Git URL (+ optional path/ref/tag/commit)
- `.zip` / `.tar.gz` archive URL (+ checksum ideally)
- uploaded archive

Do not blindly execute arbitrary third-party installer commands in Core.

Store immutable/versioned artefact and provenance:

```yaml
source:
  type: git
  url: ...
  path: ...
  revision: ...
installedRevision: immutable hash/commit
installedAt: ...
```

Core distributes required immutable skill artefacts to Backend via protocol; backend caches them locally.

### Backend-local Skills

Backend may have private local Skills unknown to Core content-wise (e.g. corporate Git only reachable from work laptop). Core may know metadata such as ID/name/description/availability, but must not require fetching their content.

Effective Run skills:

```text
PROJECT skills (Core-managed)
+
LOCAL skills (Backend-managed)
```

Handoff must be able to report that a local skill is unavailable on another backend.

### Instructions

Same split:

- Core ProjectInstructions: provider-independent project rules
- Backend local instructions: private/machine/provider-specific rules

Backend adapter maps effective instructions to provider mechanisms (`CLAUDE.md`, `AGENTS.md`, etc.). Core does not model those provider files directly.

## 19. Artifacts and object storage

Core V1 infrastructure requires:

- PostgreSQL
- S3-compatible object storage

Artifact metadata lives in PostgreSQL; bytes live in S3-compatible storage.

```yaml
Artifact:
  id: ...
  ownerId: ...
  projectId: ...
  filename: screenshot.png
  mimeType: image/png
  size: ...
  sha256: ...
  objectKey: ...
```

Events/messages reference Artifact IDs instead of embedding large blobs.

Use cases include uploads, screenshots/images, large logs, generated files/reports and centrally-managed Skill artefacts.

Helm configuration must support generic S3-compatible endpoints; do not hard-code MinIO.

## 20. Authentication and tenancy

Core auth modes:

```text
none
basic
oidc
```

OIDC target includes Keycloak.

V1 is multi-tenant aware with simple per-user isolation:

- Project.ownerId
- BackendInstance.ownerId
- Sessions inherit access through Project
- client actions are scoped to authenticated user

No full project sharing/RBAC in V1. Future: members/roles/shared backends/team projects.

## 21. Archive/delete

Normal lifecycle uses archive:

```text
Session ACTIVE → ARCHIVED → ACTIVE (restore)
Project ACTIVE → ARCHIVED → ACTIVE (restore)
```

Permanent Delete is explicit and separate, with appropriate cascading of project-owned data. BackendInstances are user-owned and are not deleted with a Project.

## 22. Workspace/file change visibility

Filesystem remains backend-owned. Backend emits structured change summaries such as:

```yaml
type: workspace.changed
knownDirectoryId: kd-puppet # optional
files:
  - path: roles/foo/tasks/main.yml
    state: MODIFIED
summary:
  additions: 42
  deletions: 8
```

Detailed diff is fetched on demand from Backend; Core does not need to persist every diff as source of truth. Event history may retain the list of files touched.

Future review/ChangeSet/Crit integration should not force V1 into a GitHub-PR model.

## 23. PostgreSQL model — initial tables

Start with explicit relational state plus append-only event history. Suggested initial tables (names may evolve):

```text
users                         # depending on auth implementation
projects
sessions
runs
jobs
events
validation_requests
user_input_requests
backend_instances
backend_conditions
known_directories
known_directory_bindings
decisions
tasks
task_dependencies
job_tasks
artifacts
project_instructions
skills
project_skills
audit_entries
```

Do not rebuild current state by replaying Events.

Important DB constraints include:

- `(backend_instance_id, backend_event_id)` unique for backend event deduplication
- task dependency cycle prevention in application/domain layer (plus useful DB constraints)
- atomic pending → resolved transition for validation/input requests
- ownership/access checks on every user-scoped query/mutation

Use migrations from the beginning.

## 24. S3 model

Use an S3-compatible abstraction configured by endpoint/region/bucket/credentials/TLS/path-style options as needed.

Recommended keying should be opaque and immutable, e.g. by Artifact UUID/hash rather than trusting filenames. Filename remains metadata.

Uploads should be bounded/configurable; checksum should be verified.

## 25. Monorepo proposal

Go module/package naming can be adjusted once the final repository host/path is chosen.

```text
threavia/
├── README.md
├── LICENSE
├── go.mod
├── go.sum
├── Makefile
├── buf.yaml
├── buf.gen.yaml
├── api/
│   └── proto/
│       └── threavia/
│           └── backend/
│               └── v1/
│                   ├── backend.proto
│                   ├── events.proto
│                   ├── commands.proto
│                   └── common.proto
├── cmd/
│   ├── threavia-core/
│   │   └── main.go
│   └── threavia-backend-claude/
│       └── main.go
├── internal/
│   ├── core/
│   │   ├── api/             # HTTP handlers + SSE
│   │   ├── auth/
│   │   ├── backendconn/     # gRPC backend control connections
│   │   ├── domain/
│   │   ├── events/
│   │   ├── service/
│   │   ├── storage/
│   │   │   ├── postgres/
│   │   │   └── s3/
│   │   └── tools/           # Core Tools implementation
│   └── backends/
│       └── claude/
│           ├── adapter/
│           └── runner/
├── pkg/
│   └── backend-sdk/         # reusable Go SDK for BackendInstances
│       ├── client/
│       ├── state/
│       ├── events/
│       └── tools/
├── migrations/
├── web/                     # frontend (technology to select separately)
├── deploy/
│   └── helm/
│       └── threavia/
├── docs/
│   ├── architecture.md
│   ├── protocol.md
│   └── api.md
└── examples/
    └── backend-example/
```

Guidelines:

- keep provider-independent backend machinery in `pkg/backend-sdk`
- keep Claude-specific translation/runner logic isolated under backend adapter code
- Core domain should not import Claude/Codex-specific concepts
- generated protobuf code location should be deterministic and tooling-driven (Buf recommended)
- avoid premature microservices; Core starts as one deployable Go service

## 26. Initial protobuf shape

Exact protobuf is to be implemented/reviewed, but V1 should center on a bidirectional control service rather than provider-specific RPCs.

Conceptual shape:

```proto
service BackendControl {
  rpc Connect(stream BackendToCore) returns (stream CoreToBackend);
}
```

`BackendToCore` oneof candidates:

```text
hello / registration-auth result context
heartbeat
status_update
reconcile_state
job_event
command_result
core_tool_request
```

`CoreToBackend` oneof candidates:

```text
welcome / connection lease
start_job
cancel_job
validation_resolution
user_input_resolution
job_input_now
job_input_next
reconcile_instruction
core_tool_response
```

Common identifiers should be opaque strings/UUIDs. Avoid encoding provider assumptions into messages.

## 27. API transaction semantics

Important commands should be idempotent where retries are realistic.

Especially:

- StartSession first-send operation
- enqueue message/Job
- cancel Job
- resolve ValidationRequest
- resolve UserInputRequest
- backend registration/claim

Use request/idempotency IDs where appropriate rather than relying on clients never retrying.

## 28. Security baseline

V1 should establish these boundaries from day one:

- TLS for remote Core access
- authenticated BackendInstance identity after registration
- short-lived runtime credentials where appropriate
- provider credentials never leave BackendInstance
- ownership checks in Core
- one-shot registration/claim tokens are expiring/single-use
- canonical validation payload hash for receipts
- Artifact checksums and safe object keys
- secrets only through Kubernetes Secrets/configured secret mechanisms
- logs must avoid credentials/tokens/provider secrets

Do not treat DiscoveryRoots as a security sandbox.

## 29. Deployment / Helm

Core chart must allow configuration of:

- Core image/service/ingress
- auth mode (`none`, `basic`, `oidc`)
- PostgreSQL connection
- S3-compatible object storage
- backend registration mode/shared secret/existingSecret
- TLS/trusted proxy settings as needed

Do not require Redis, NATS or Kafka for V1.

Kubernetes BackendInstances are separate deployments and need a small persistent volume for local backend state/native provider data as required.

## 30. MVP implementation slice

The **first executable vertical slice** should deliberately exclude much of the final V1 surface while proving the hard foundations.

Target user story:

> From Web, select a Project + BackendInstance + optional KnownDirectory, send “analyse ce projet”, watch Claude Code work in realtime, answer validation/input, receive completion, then send a second message and confirm it continues in the same native Claude session.

MVP components:

```text
Web
 ↓ HTTP/JSON + SSE
Threavia Core (Go)
 ↓ gRPC/protobuf
Claude Backend (Go)
 ↓ local provider integration
Claude Code
```

MVP includes:

- PostgreSQL
- minimal auth suitable for development (but interfaces must not preclude configured auth modes)
- Project
- KnownDirectory + Backend binding
- Backend registration/connection
- Session/Run/Job
- StartSession atomic first-send flow
- normalized persistent Events
- global event sequence + SSE
- backend event deduplication + per-Job sequence
- backend durable local state/replay
- user/agent messages
- validation requests/responses
- user input requests/responses
- Job completion/failure/cancel
- second Job in same Run/native Claude session
- reconnect/reconciliation sufficient to survive Core restart/disconnect

MVP may defer implementation (while preserving architecture) of:

- Android
- Voice/SIP
- S3/Artifact UI flows (S3 remains a V1 infrastructure requirement)
- Skills
- Tasks/Decisions UI/tools
- sophisticated notification delivery
- OIDC polish
- ExecutionPolicy breadth
- cross-backend handoff

## 31. Suggested implementation order

1. Bootstrap Go monorepo, lint/test/Buf/migrations/dev compose.
2. Define domain IDs/enums and initial PostgreSQL schema.
3. Define protobuf V1 and generate Go bindings.
4. Implement backend registration identity and `Connect` bidirectional stream.
5. Implement Go Backend SDK durable state, heartbeat, ACK/replay and reconciliation primitives.
6. Implement minimal Claude backend adapter capable of start/resume and normalized output.
7. Implement Core Project/KnownDirectory/Backend APIs.
8. Implement atomic `StartSession` → Session + Run + Job.
9. Persist backend Events with dedupe/per-Job ordering/global sequence.
10. Implement SSE global event stream + reconnect cursor.
11. Build minimal Web Session UI with recent history + live stream.
12. Implement ValidationRequest and UserInputRequest end-to-end.
13. Implement cancellation and backend reconnect/reconciliation tests.
14. Verify second Job resumes same native Claude session.
15. Only then add Tasks/Decisions, S3 Artifacts, Skills and richer clients.

## 32. Acceptance criteria for first milestone

A milestone is successful when all of the following can be demonstrated locally:

1. Core starts with PostgreSQL and migrations.
2. Claude backend registers/connects outbound to Core and becomes READY.
3. Web shows that backend and a Project/KnownDirectory.
4. First message atomically creates Session + Run + Job.
5. Backend starts Claude in resolved cwd.
6. Agent output streams Backend → Core → PostgreSQL → SSE → Web.
7. Duplicate backend event replay does not duplicate user-visible Events.
8. A validation/input request can wait indefinitely and be answered from Web.
9. Client reload shows current snapshot + recent history and continues live.
10. Core restart while backend continues does not lose acknowledged history; missing unacked events replay after reconnect.
11. Job can be cancelled with CANCELLING semantics.
12. A second user message becomes a second Job on the same Run and resumes the same native Claude session.

## 33. Explicit non-goals / post-V1 backlog

- automatic backend scheduler/scoring/failover
- transparent automatic migration between backends
- REVIEW capability and CODE → REVIEW → findings loops
- Crit/ChangeSet integration
- vector/embedding memory search
- full Project sharing/RBAC/team model
- rich external notifications (Webex/Slack/email/etc.)
- user-facing registered client/device management and revocation
- generic remote provider authentication challenges
- automatic native-session TTL management by Core
- full diff/file source-of-truth storage in Core
- Kafka/NATS/event-bus infrastructure unless future scale demonstrates need

## 34. Important UX invariants

Implementations should preserve these even if internal details change:

- User thinks in **Projects and Sessions**, not Runs.
- A Run/backend transition should be nearly invisible except when user needs to choose/debug it.
- Session timeline is continuous across Runs.
- Android/Web/VS Code see the same Core-owned history regardless of which client created it.
- Resolved validation/input must disappear from pending attention everywhere.
- Reconnecting clients must not turn historical Events into hundreds of stale notifications.
- New Session UI is a local draft until first send.
- Backend changes are explicit.
- Filesystem access is real backend filesystem behavior; KnownDirectory is portability/discovery metadata, not a sandbox.

## 35. First coding-agent task

A coding agent receiving this document should **not redesign the architecture before starting**. Begin by producing a minimal compilable repository skeleton matching section 25, plus:

- Go module
- Core and Claude-backend binaries that start and shut down cleanly
- protobuf `BackendControl.Connect` skeleton
- generated Go protobuf code
- PostgreSQL migration framework with the minimal Project/BackendInstance/Session/Run/Job/Event tables
- configuration structs/environment loading for PostgreSQL, S3 and auth mode
- unit tests for core domain enums/state transitions where already defined
- README with local development commands

Do not implement Android/Voice/review/vector search or invent additional infrastructure. If an implementation detail is ambiguous, prefer the simplest design consistent with this specification and document the choice.
