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
these Core-side additions. Backend registration/claim/revocation and user
authentication audit remain separate backlog work.
