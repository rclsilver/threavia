# Human code review

Open **Review** from a session in the Web client, or **Threavia: Review Changes**
from its VS Code conversation or session menu. Changes remain available for
browsing without imposing a review on every answer.

The review groups declared repositories and worktrees under separate roots.
The default comparison starts at the last approved state. Choose an earlier
revision, or **Since tracking began**, to widen the comparison. The file list
is recomputed for that comparison, including changes that were later reverted.

On Android, **Files** opens a picker over the code. Previous/next controls stay
beside the file name. Revision and comparison controls are in **Review options**.
Tap a line number to select it, optionally choose **Range** and tap the end line,
then choose **Comment**. Desktop also supports Shift-click to extend a selection.
Selecting lines does not open a keyboard or move the code. The comment dialog
shows the selected excerpt and returns to the reading position when closed;
start/end fields remain available for adjustments. The desktop Web client offers a
side-by-side diff. Both provide the full before/after file for context. VS Code
uses its native tree, diff editor and Comments panel with real source ranges.

## Drafts and correction turns

Web comments save automatically to Core, with an explicit saved/error state.
In VS Code, **Save Draft** saves the native comment input to Core. Saved drafts
are available across clients. An unsaved native editor input remains local to
VS Code until Save Draft is pressed.

Comments may target a line, range, file or whole review. They keep their original
revision, repository, side, line numbers and source excerpt. They are never
silently moved to a different line after an edit. Open an earlier discussion
to read its original version, then resolve or reopen it yourself.

**Request corrections** publishes the complete draft batch and creates one
correction Job in the same session, atomically. Published messages are immutable;
add a reply to clarify them. A batch identifier makes retries safe. The agent
can read published discussions and reply with what it changed; drafts stay
private until sent. An agent cannot resolve or approve the human review.

After a turn finishes, Threavia captures a new revision. **Viewed** is tied to
the captured tree; a changed version is not treated as already read.

## Mandatory validation

Choose **Require validation**, or ask the agent to request a required review.
Core blocks subsequent Jobs, interruptions and unrelated queued work. The
updated backends also check before the next tool action, independently of
permission allow rules. A command already executing is allowed to reach its
next action boundary; review does not undo an operation already performed.
The backend acknowledges the live checkpoint before the request succeeds.
An acknowledged mandatory review remains latched through a Core disconnection;
unrestricted work retains its normal ability to continue while Core is offline.

Sending corrections permits only the dedicated correction turn. If a previous
turn is paused, it stops at its checkpoint so the correction turn can take its
place. A question or permission prompt is already a stopping point. Other queued
work stays blocked. After corrections, validation is required again.

The native hook and Core checkpoint waits are bounded so a provider's hook
timeout cannot turn missing validation into permission. A still-pending review
produces an explicit refusal to continue; the durable Core gate remains closed.

Resolve discussions and send or discard drafts before **Approve review**.
Approval checks the latest captured code against the actual repositories and
refuses a stale version. It permits continuation; merging and publishing remain
separate actions under the project's rules.

## Repository capture and availability

The current Job's repository is captured before its provider starts. Declare
additional repositories and worktrees with `review_repository_register` before
editing them. Ignored files stay ignored. A copied Git index records staged,
unstaged and untracked content without changing the real index. Actual tree
comparisons include changes committed during the Job and edits to files that
were already dirty at its start.

A late declaration starts at its declaration time. Existing sessions gain
coverage from their first capture after the updated backend is installed;
Threavia does not invent a missing earlier baseline. Concurrent edits by a human
or another process are changes observed in the interval, not proven authorship.

Core stores review metadata, comments, drafts and revision identities. Files
remain on the backend, served from immutable Git objects. Explicit refs under
`refs/threavia/reviews/` protect captured trees from Git collection. The common
Git directory allows an earlier revision to be read after a linked worktree is
removed. Refreshing current files still requires that worktree to exist.
Snapshot refs are retained; automatic retention cleanup is not implemented yet.

If a backend is offline, discussions remain available and code loading explains
the unavailability. Reconnect it and retry. Text previews are bounded to 512 KiB
per side; binary files, submodules and larger files can receive file comments.
No repository filters or external diff commands are executed. Symlinks are
displayed as their stored target, never followed into the host filesystem.

New backends advertise `HUMAN_REVIEW`. Older backends keep their existing
execution behavior and refuse mandatory review until updated. The feature is
independent of the future automated REVIEW/Findings capability.

## Local validation before commit

This isolated stack has its own PostgreSQL and synthetic repositories; it does
not use production data, credentials or provider inference. Run from the
worktree, with the project's development shell available:

```sh
CGO_ENABLED=0 go build -o bin/review-fixture ./cmd/threavia-review-fixture
docker compose -p threavia-review-3ed0b8ef -f deploy/dev/review.compose.yaml up -d --build
```

Core listens on loopback port 18081, gRPC on 19091, PostgreSQL on 55442. The
fixture creates **Local code review validation**, then a session waiting for
mandatory review across two synthetic repositories. The fixture applies a
deterministic correction (`replicaCount: 3`) and labels its replies as simulated.
It does not interpret arbitrary comments or prove real-model correction quality.

Check the Web client on desktop and Android: browse both roots, read full-file
context, annotate a range, switch files, recover saved drafts from another
client, send one batch, inspect the corrected revision, resolve/reopen a thread,
then approve. Install the local VSIX in a test editor and repeat through the
native tree and Comments panel. Verify queued work remains blocked until
approval, and that approval performs no merge.

Keep the stack and worktree while validation is pending. After validation and a
requested merge, remove only this stack and its data:

```sh
docker compose -p threavia-review-3ed0b8ef -f deploy/dev/review.compose.yaml down -v
```

Then remove this worktree under the project's normal workflow.
