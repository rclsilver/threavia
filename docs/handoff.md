# Moving a session to another backend

The Web session settings show a destination preview before Move: capabilities,
optional features, capacity, model compatibility, local Skills that will be
missing, and the session directory's binding on that backend. An unbound logical
directory must be resolved by the destination before work starts. Without a
logical directory, the destination uses its configured default; Core does not
invent or copy a physical path. Local files must already be available there.

`GET /api/v1/sessions/{sessionId}/handoff?backendInstanceId=…` is read-only and
owner-scoped. `canMove` and `reasons` explain an archived session, pending work,
same destination, revoked/non-CODE target, incompatible model/effort, or full /
draining target. An offline or starting backend remains an explicit choice with
a warning: a message may wait for reconnection/provider readiness. Moving does
not reserve a capacity slot; actual dispatch remains authoritative.

The PATCH move accepts `expectedRunId` from the preview. It locks the session,
revalidates the destination, refuses all pending Jobs (including QUEUED), and
creates a fresh Run. Move, audit and persistent `run.created` notice commit
together. Concurrent messages share the session lock: they either queue on the
current Run or receive a conflict to retry, never silently land on the Run left
behind. A move cannot be combined with other settings in the same request.

The native provider session stays on its backend. Until the new Run binds its
own native session, Core appends bounded recent reference data to the existing
ProjectContext instructions: user/agent messages, completion/failure summaries
and changed-file names from before this Run. No tool output, diff, binary data,
local Skill content or provider session identifier is copied. The current user
prompt remains separate. History is labelled as reference, not a new instruction
or permission. Existing project instructions, important decisions, tasks, Skills
and destination directory metadata continue to use the existing context fields.

Limits: at most 16 recent selected events; 1,500 characters per text; 20 file
paths per change (200 characters each); at most 24,000 encoded JSON bytes. Newest
items take priority, then appear in chronological order. This is an excerpt of
Core history, not a generated semantic summary or native transcript. Older data
remains searchable through Core tools. A failed history read leaves work queued.
After native binding, later messages resume normally without re-injecting it.

Web and VS Code render the move notice, including its destination name and the
fresh-session/workspace boundary. VS Code has no existing move action; the Web
and API own this preview/confirmation flow. No backend or protocol update is
required. Recovery after a native resume failure remains separate backlog work
(`dc0288ef`); this change only covers explicit moves.
