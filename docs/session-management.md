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
| `backend_list` | List usable backends with status and model catalogs (models, efforts, defaults). |
| `session_update` | Change a managed session's `title`, `workingDirectoryId` (empty detaches it), `backendInstanceId`, `model` and/or `effort` for its next jobs. A backend move needs no running or queued job; pass `model`/`effort` when the current ones do not exist on the destination. |
| `session_move` | After human validation, re-file any owned session under another active project (`sessionId`, `projectId`), with its history and files. A working directory of the old project is detached; a global one is kept. The tasks and decisions its agents created follow it unless `moveKnowledge` is false, except those linked to ones that stay; the result lists both. Refused while the session has unfinished jobs. Announced by `session.project_moved`. |
| `session_detach` | Stop managing a delegate: it becomes an ordinary unmanaged session. |
| `session_adopt` | After human validation, start managing an unmanaged session (e.g. a manual one). Refused if archived, managed by another conversation, or if it would create a cycle. |
| `session_create` | Start a session with a self-contained `message` and optional `title`, `projectId`, `workingDirectoryId`, `backendInstanceId` (default: the creator's), `model`, `effort` and `idempotencyKey`. The default is the current project. Returns `session`, `run` and `job`. The created session is a direct delegate. |
| `session_list` | `scope: project` (default) lists the current project, or `projectId`; `all` lists sessions across the user's projects; `managed` lists this conversation's direct delegates across projects, optionally filtered by `projectId`. `includeArchived` includes archived sessions and projects. `limit` is 1–100 (default 50); pass `nextOffset` as `offset` for the next page. Results are ordered newest-created first, so activity does not shift existing sessions between pages. |
| `session_read` | Read any owned session's `session`, up to 50 recent Jobs (unfinished first), pending `attention`, and recent `events`, including user and agent messages. `limit` is 1–100 (default 50); `before` pages backwards using `beforeSequence`. |
| `session_send` | Send a message to any owned session. `QUEUE` creates a later Job, `NEXT` reaches the next step, `NOW` interrupts work and abandons pending questions and permissions. NEXT/NOW require backend support. Messages are attributed to the sending agent; they do not adopt the recipient as a delegate or change its policy. A `QUEUE` message starts a Job that calls the sender back (see below). |
| `session_answer` | Answer a pending information request of a delegate, or of a Job started by this conversation's `session_send`, using `sessionId`, `requestId` and `value`. Closed choices are enforced. It resumes the worker; it cannot approve permissions. |
| `session_resolve_validation` | Relay the exact permission of a delegate, or of a Job started by this conversation's `session_send`, for approval in this conversation, then forward the decision. Requires `sessionId`, `requestId`, `approved`, original `payloadSha256` and `requestPayload`, plus optional `note`. |
| `session_wait` | Wait for events, attention or completion in any owned session. `afterSequence` pages forwards; `timeoutSeconds` is 0–30 (default 20). `jobId` narrows attention and completion to one Job of the session (returned as `job`); `until: attention` returns only on a question, a permission or the end of the work rather than at every event (default `event`). Returns state, events, a session event `cursor`, `hasMore`, `finished` and `timedOut`. A timeout is not completion. |
| `session_cancel` | Stop a specific `jobId` in a managed `sessionId`, with an optional `reason`. History and files stay available. |
| `session_archive` | Archive or restore using `archived`. Jobs must finish or be cancelled before archival. |
| `session_control` | After human validation, act on **any** owned session, managed or not: `action` is `cancel` (`jobId`, `reason`), `archive`, `restore`, `update` (the `session_update` fields), `answer` (`requestId`, `value`), `pin` (`pinned`), `snooze` (`until`, `untilChange` or `clear`), `mark_read` (`sequence`) or `fetch` (repository state after a remote fetch). For this conversation and its direct delegates, the unvalidated tools remain the normal path. |
| `session_delete` | After human validation, delete an owned session and its history. Refused while a job is unfinished and for the calling conversation. Published files stay with the project unless `deleteArtifacts` is true. |
| `job_delete` | After human validation, withdraw a queued (not yet started) job; the timeline shows it cancelled, as when a person takes a queued message back. |
| `session_files` | List files of an owned session's working directory matching `query`, as its online backend sees them. |
| `session_diff` | Read one file's change recorded by the file-change event at `eventSequence`, for `path`. |
| `session_repository` | Read the git state of an owned session's working directory. `fetch` refreshes from the remote first, only for this conversation or a direct delegate; elsewhere use `session_control` `fetch`. |
| `session_pin` | Pin or unpin this conversation or a direct delegate on every client. |
| `session_snooze` | Snooze this conversation or a direct delegate until an RFC 3339 `until`, or `untilChange`, or bring it back with `clear`. |
| `session_mark_read` | Mark this conversation or a direct delegate read up to `sequence` (default: its latest event). |
| `attention_list` | List questions and permissions waiting for the person, across sessions or for one `sessionId`. |

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
permissions, waiting, cancelling and archiving without validation remain limited
to directly delegated children; `session_control` does the same on any owned
session once the person approves the exact call. Nested delegation uses that rule at each level. Reading
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
and final report do. Core marks what it keeps quiet so clients stay quiet too:
the worker's pending requests carry `delegated: true` and its `job.completed`
and `job.failed` events carry `delegated: true`, which the Web and VS Code
clients do not announce. Requests remain visible through the existing API for
manual recovery. Archiving the manager restores ordinary worker notifications;
deleting it detaches workers and keeps their sessions and files.

### Callbacks to a sender

A conversation that messages a session it does not manage is called back the
same way for the Job its message made (decided 2026-11-07). A `QUEUE` message
records the sending session on the new Job (`callbackSessionId`); a `NOW` with
files, or one replacing a Job that waits for its backend, makes a Job too and
records it the same way. When that Job asks a question, requests permission or
ends, the sender receives a queued message naming the session and the Job, and
may answer with `session_answer` or relay with `session_resolve_validation`,
for that Job only. The relay still needs the person's approval in the sender's
own Job, as for a manager. The person can still answer from their clients; the
first answer wins.

There is no callback when:
- the message joins a running Job (`NEXT`, or `NOW` without files): it does not
  take that Job over; use `session_wait` with its `jobId`;
- the sender manages the target: it is told as the manager;
- the sender replies to the session whose message started its own Job: that
  session hears when this turn ends anyway, and two agents answering each
  other's callbacks would never stop.

A client that cannot be called back, such as a control client
([control-mcp.md](control-mcp.md)), calls `session_wait` with the `jobId` that
`session_send` returned and `until: attention`.

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
