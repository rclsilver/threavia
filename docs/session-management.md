# Managing delegated sessions

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
| `session_create` | Start delegated work with a self-contained `message` and optional `title` and `idempotencyKey`. Returns `session`, `run` and `job`. |
| `session_list` | Find this conversation's directly managed sessions, across runs. `includeArchived` includes finished sessions put aside. |
| `session_read` | Read `session`, up to 50 recent Jobs (unfinished first), pending `attention`, and recent `events`. `limit` is 1–100 (default 50); `before` pages backwards using `beforeSequence`. |
| `session_send` | Send feedback. `QUEUE` creates a later Job, `NEXT` reaches the next step, `NOW` interrupts work and abandons pending questions and permissions. NEXT/NOW require backend support. |
| `session_answer` | Answer a pending information request using `sessionId`, `requestId` and `value`. Closed choices are enforced. It resumes the worker; it cannot approve permissions. |
| `session_resolve_validation` | Relay the exact worker permission for approval in the manager conversation, then forward the decision. Requires `sessionId`, `requestId`, `approved`, original `payloadSha256` and `requestPayload`, plus optional `note`. |
| `session_wait` | Wait for events, attention or completion. `afterSequence` pages forwards; `timeoutSeconds` is 0–30 (default 20). Returns state, events, a session event `cursor` and `timedOut`. A timeout is not completion. |
| `session_cancel` | Stop a specific `jobId` in a managed `sessionId`, with an optional `reason`. History and files stay available. |
| `session_archive` | Archive or restore using `archived`. Jobs must finish or be cancelled before archival. |

The manager stays responsible for checking results and completing the original
task. Assign distinct files to parallel workers: sessions inherit the same
checkout; these tools do not create worktrees or merge changes automatically.
These tools do not impose a parallelism limit; take the backend and provider's
actual limits into account when delegating.

## Scope and persistence

`managerSessionId` belongs to the Session, so a later manager Job can find its
workers after a resume or reconnect. Only directly delegated children may be
read or changed through these tools; an unrelated session, sibling or parent
cannot be targeted even when it shares the owner or project. Nested delegation
uses the same rule at each level.

Creation is atomic. A worker inherits the manager's project, backend, logical
working directory and effective execution policy before its first dispatch.
The policy is a snapshot taken at delegation; later policy changes to the
manager do not silently rewrite a worker's policy. Project policy rules still
apply. The tools cannot select another backend, directory or wider policy.

Optional idempotency keys are scoped to the manager and operation (and worker
for messages). Reusing a creation key resumes the existing delegation rather
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

Build and restart Core, apply migration `000015_managed_sessions`, then start
a new Job to receive the tool catalogue. No backend protocol change is needed.
Existing Jobs retain the tools they received when they started.

Web and VS Code use the same existing Session, message, input and validation
routes and events; their Session contracts include the optional manager field,
and messages carrying actorJobId are attributed to Agent instead of You.
There are no new UI routes or controls, so no new demo route handler or editor
command is required. The demo includes an ingress worker and its manager's
reviewed callback, with a tour step showing the Agent attribution.

The integration suite in `internal/core/api/sessiontools_test.go` uses a
throwaway PostgreSQL schema to exercise real Core/backend messages, including
same-connection waits, scope boundaries, receipt integrity and manager wakeup.
