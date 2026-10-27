# Capture learnings from a resolution

When a session's latest Job has completed successfully and no work is pending,
**Capture learnings** is available in the Web session header (inside session
details on a phone) and in the VS Code conversation. Archived sessions must be
restored first. Failed or cancelled Jobs are not presented as verified resolutions.

Confirm **Propose learnings** to start a normal turn on that session's backend,
with its current model, permissions and subscription quota. The composer and its
attachments stay untouched. There is no separate inference service, automatic
end-of-Job trigger or background curation task.

The shared request asks the agent to use the existing evidence, search project
memory for duplicates and contradictions, inspect relevant skills when available,
and propose at most three useful learnings. Completion alone is not proof of a
claim: an agent should distinguish verified observations from assumptions and
say when there is nothing useful to retain.

Each proposal identifies its exact text, reason, source session/Job, destination
and scope. Facts and decisions belong in project memory; reusable procedures in
skills; durable rules in project instructions. Memory defaults to NORMAL project
scope. Replacing an existing decision names the decision being superseded;
global scope and IMPORTANT require their own explicit agreement. Credentials
must never be retained, only a location and purpose when relevant.

The agent presents proposals in the conversation and asks a Threavia question,
one proposal at a time: **Save**, **Edit** or **Ignore**. Edit asks for changes
and presents the revised proposal for approval again. No answer or an answer to
another proposal is not consent. The initial turn requests proposals only;
changes follow explicit acceptance of each exact proposal. The existing question
and message history retain the proposal and the person's answer across clients.

For a skill or project rule, the agent must show the exact diff and recheck the
target before applying it. If the current tools cannot install a managed skill
or update project settings, the proposal remains unapplied and the agent explains
the existing **Manage Skills** or **Project settings** action. It must not invent
an installation tool or bypass the client with credentials. Accepting a learning
does not authorize Git publication or deployment. Existing memory supersession,
skill management and project-edit permissions continue to apply.

This first lot is a guided conversation, not a new structured proposal store or
a model-independent approval engine. The request instructs the agent; Core does
not parse its prose or turn an arbitrary “Save” into a write. The normal tools and
permission system still control what it can do. Automated suggestion quality,
provider-specific skill mutation, general skill version history/rollback and
dependency pinning are separate work, not claimed by this launch action.

## Concurrent use and retries

The clients re-read the session after confirmation. Their normal message request
also carries `afterJobId`: under the session lock, Core requires the referenced
Job to be the latest, successfully completed one, with no unfinished Jobs. A
concurrent new message therefore causes a 409 rather than queuing a stale
capture. Normal messages without this optional field keep their existing
behaviour.

Both clients use the same Idempotency-Key for a given source Job. Repeated clicks,
retrying an uncertain response and simultaneous Web/VS Code requests reuse the
accepted Job. A replay does not dispatch it again. Source Job IDs are immutable
and project/owner boundaries remain those of the session's message endpoint.

## Validation

The browser-only demo plays a clearly labelled fictitious memory proposal. Try
Save, Edit and Ignore: editing requires confirmation again; only Save changes
demo memory. Nothing calls a model or changes a real project.

Automated tests cover idle/completed eligibility, source changes, request
idempotency, Web confirmation/cancellation/errors, VS Code draft preservation,
the playable edit/save/ignore flow and real PostgreSQL/API concurrency. They do
not establish that every model follows the request or replace installation
tests in a real browser, phone and VS Code host.
