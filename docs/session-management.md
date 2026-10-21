# Managing sessions across projects

An agent can list, read and message sessions owned by the signed-in user,
including sessions in other projects. It can also create a session in a chosen
project. The four tools use the same ownership checks as the HTTP API; another
user's projects and sessions are inaccessible.

A person can give a development or bugfix task to one manager conversation.
That agent creates worker sessions, sends assignments and feedback, answers
their questions from the person's instructions, and gathers and checks their
results. Only missing information or decisions reach the person in the manager
conversation. Permissions are also relayed there and still require human
approval.

## Tools

These Core Tools are exposed automatically by both Codex and Claude backends
as `mcp__threavia__<name>` when a Job starts:

| Tool | Purpose |
| --- | --- |
| `session_create` | Start a session with a self-contained `message` and optional `title`, `projectId`, `workingDirectoryId` and `idempotencyKey`. The default is the current project. Returns `session`, `run` and `job`. The created session is a direct delegate. |
| `session_list` | `scope: project` (default) lists the current project, or `projectId`; `all` lists sessions across the user's projects; `managed` lists this conversation's direct delegates across projects, optionally filtered by `projectId`. `includeArchived` includes archived sessions and projects. `limit` is 1–100 (default 50); pass `nextOffset` as `offset` for the next page. Results are ordered newest-created first, so activity does not shift existing sessions between pages. |
| `session_read` | Read any owned session's `session`, up to 50 recent Jobs (unfinished first), pending `attention`, and recent `events`, including user and agent messages. `limit` is 1–100 (default 50); `before` pages backwards using `beforeSequence`. |
| `session_send` | Send a message to any owned session. `QUEUE` creates a later Job, `NEXT` reaches the next step, `NOW` interrupts work and abandons pending questions and permissions. NEXT/NOW require backend support. Messages are attributed to the sending agent; they do not adopt the recipient as a delegate or change its policy. |
| `session_answer` | Answer a pending information request using `sessionId`, `requestId` and `value`. Closed choices are enforced. It resumes the worker; it cannot approve permissions. |
| `session_resolve_validation` | Relay the exact worker permission for approval in the manager conversation, then forward the decision. Requires `sessionId`, `requestId`, `approved`, original `payloadSha256` and `requestPayload`, plus optional `note`. |
| `session_wait` | Wait for events, attention or completion. `afterSequence` pages forwards; `timeoutSeconds` is 0–30 (default 20). Returns state, events, a session event `cursor` and `timedOut`. A timeout is not completion. |
| `session_cancel` | Stop a specific `jobId` in a managed `sessionId`, with an optional `reason`. History and files stay available. |
| `session_archive` | Archive or restore using `archived`. Jobs must finish or be cancelled before archival. |

`session_list` also returns a `projects` directory with each relevant project's
`id`, `name` and `status`, including projects without sessions. This lets the
agent discover a target for creation without knowing its identifier in advance.
For example, call `session_list({scope: "all"})`, then
`session_create({projectId: "<id>", message: "<assignment>"})`.
Do not combine `scope: all` with `projectId`; use `scope: project` for one project.

The manager stays responsible for checking results and completing the original
task. Assign distinct files to parallel workers: sessions inherit the same
checkout; these tools do not create worktrees or merge changes automatically.
These tools do not impose a parallelism limit; take the backend and provider's
actual limits into account when delegating.

## Scope and persistence

`managerSessionId` belongs to the Session, so a later manager Job can find its
workers after a resume or reconnect. Reading and sending messages require
ownership, regardless of delegation or project. Answering questions, relaying
permissions, waiting, cancelling and archiving remain limited to directly
delegated children. Nested delegation uses that rule at each level. Reading
another conversation does not make its contents new user instructions or
authorization.

Creation is atomic. A worker uses the target project's instructions and
inherits the manager's backend and effective execution policy before its first
dispatch. In the same project it also inherits the logical working directory,
unless `workingDirectoryId` selects another directory or an empty string clears
it. In another project no directory is inherited: an explicit directory must
belong to that target project or be shared globally.
The policy is a snapshot taken at delegation; later policy changes to the
manager do not silently rewrite a worker's policy. Project policy rules still
apply. Creation cannot select another backend or a wider policy.

Optional idempotency keys are scoped to the manager, operation and target
project (or target session for messages). Reusing a creation key resumes the existing delegation rather
than creating duplicate work. Use a new key for a new assignment.

A callback is queued when a worker asks a question, requests permission or ends
a Job. An idle manager resumes immediately; a busy one receives the callback
after its current turn and can also observe those states with `session_wait`.
Worker questions and completion notifications do not separately notify the
person while their manager is active; the manager's own questions, approvals
and final report do. Requests remain visible through the existing API for
manual recovery. Archiving the manager restores ordinary worker notifications;
deleting it detaches workers and keeps their sessions and files.

## Permission receipts and attribution

Workers' output never grants new user authorization. `session_answer` records
channel `agent` and the manager's `actorJobId`. Messages and relayed permission
receipts carry the originating agent Job too.

`session_resolve_validation` is always gated, including in AUTONOMOUS mode.
Copy the worker's original payload and hash unchanged. The mandatory gate
shows that operation as part of a permission request in the manager Job. Core
also requires an approved human receipt for that exact tool input in the
current manager Job: changing the target, payload, hash, decision or note
invalidates the receipt. A declined tool invocation leaves the worker request
pending; a denial can be explicitly relayed with `approved=false` after its own
confirmation. Resolving directly from the worker's existing API is still
possible for recovery. First resolution wins.

## Activation and clients

Session creation, archival, restoration and deletion record the actor, channel,
scope and compact details in the audit, atomically with the mutation and
persistent event. Agent-managed actions identify the originating manager Job;
an idempotent creation replay does not create another audit entry. Titles remain
in the audit after the Session is deleted. The `session.deleted` notice is
project-scoped with `{sessionId, title, artifactsDeleted}`, so reconnecting
clients can replay it after the Session history has gone. Clients also accept
older deletion notices with a Session id in the envelope.

Deleting artifacts along with a Session removes their metadata and stages their
notifications in that same transaction. Stored bytes are cleaned up only after
commit; storage cleanup failures are logged as for ordinary artifact deletion.
The Web audit follows these changes and offers Sessions and Skills filters.
VS Code follows session deletion from either event shape and exposes skill
changes on its event bus; it has no native Skills or audit view to refresh.

Build and restart Core, apply migration `000015_managed_sessions`, then start
a new Job to receive the tool catalogue. No backend protocol change is needed.
Existing Jobs retain the tools they received when they started.
The cross-project extension requires only a Core rebuild and restart, followed
by a new Job to receive the updated descriptions and schemas. It adds no
migration, HTTP route or backend protocol field.

Web and VS Code use the same existing Session, message, input and validation
routes and events; their Session contracts include the optional manager field,
and messages carrying actorJobId are attributed to Agent instead of You.
There are no new UI routes or controls, so no new demo route handler or editor
command is required. The demo includes an ingress worker and its manager's
reviewed callback, with a tour step showing the Agent attribution.

The integration suite in `internal/core/api/sessiontools_test.go` uses a
throwaway PostgreSQL schema to exercise real Core/backend messages, including
same-connection waits, scope boundaries, receipt integrity and manager wakeup.
`sessionaccounttools_test.go` covers project discovery, pagination, reading,
message attribution and idempotency, and creation across projects. Both clients
use the existing events and Agent attribution; no UI, demo route or editor
command is added for this Core Tool extension.
