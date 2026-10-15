# Audit history

Core records significant mutations with their authenticated owner/actor, client
channel, scope, subject identifier and compact detail. Account settings include
an Audit view across the owner's projects and global memory, including deleted
projects. Agent Core-tool mutations
also carry the calling Job id; that attribution comes from the authenticated
Job context. The Web project Audit tab groups entries into Projects, Tasks,
Memory, Sessions, Skills, Approvals, Permissions and Backends.

Project creation, updates, archival, restoration and deletion are audited.
Updates name the changed fields and preserve the previous project name on a
rename. Instructions and description contents are omitted. A deletion entry
keeps the last name and textual subject/project ids after cascading deletion.
The owner-scoped `project.deleted` notice survives that cascade too.

Task creation, updates, deletion and dependency changes retain titles, status
changes and dependency metadata, without task descriptions. Repeated dependency
additions/removals and updates that change no significant fields do not add
audit noise. Project mutations serialize graph updates so concurrent dependency
additions cannot commit a cycle.

Memory creation/replacement, pinning, deletion and promotion to global memory
are audited using titles and lineage metadata, without fact content. Deleting
a replacement revives its predecessor only within the same scope, with a
`decision.updated` event and `restored: true`. Deleting a promoted global fact
leaves the earlier project fact historical. Global entries are visible only to
their owner through `/api/v1/me/audit`; a project filter returns that project's
entries, including those whose subjects were deleted.

These mutations, their audit entries and persistent events share a database
transaction. Failure to record either history rolls back the mutation and
publishes nothing. Blob cleanup after project deletion runs after commit, so a
failed deletion cannot remove artifact bytes. The in-process event broker only
publishes committed changes; reconnecting clients read the same durable events.

Web refreshes the audit for project/task/memory notifications and the demo uses
the same event path. VS Code refreshes its project and knowledge views; it has
no native Audit view. No schema migration or provider update is needed for
these Core-side additions.

Backend history is account-scoped: `backend.registered`, `backend.claimed`,
`backend.revoked` and `backend.availability_changed`. Registration via a one-shot
token records the backend as actor (`backend:<id>`, channel `backend`), rather
than attributing its use to the token's owner. A shared-key registration has no
owner: its registration history is attached atomically at claim, with the
original `registeredAt` in detail. Claim and revocation name the authenticated
user and client channel. Names survive removal from the live backend list.
Registration/claim/revocation, audit and the owner-scoped `backend.registered`
or `backend.revoked` event commit together; failures leave tokens and claim
codes usable and credentials valid.

Availability audit records transitions to READY, DEGRADED and OFFLINE. STARTING,
heartbeats, capacity, condition-only changes, repeated reports and superseded
disconnects do not add audit entries. These traces share the status/event
transaction; OFFLINE is attributed to Core, other reports to the backend.
Detail holds only old/new operational status and provider auth
state; no credentials, tokens, claim codes, connection ids or condition text.
Web refreshes account audit on backend notifications and renders these actions
under Backends. VS Code already refreshes backend state and has no Audit view.

User authentication audit remains backlog work. Core verifies bearer tokens;
it does not observe the OIDC login or its failures at the identity provider.
