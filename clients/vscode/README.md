# Threavia for VS Code

Follow and answer your Threavia agents without leaving the editor: what waits
for you, your pinned sessions, every Project's sessions with what each one is
doing, and a Project's tasks and decisions, kept live from Core's event stream;
from one Threavia Core or from several at once.

- **Sidebar.** The Threavia view in the activity bar lists what waits for you
  (approvals and questions, across every Project), your pinned sessions, and
  each Project's sessions, waiting ones first. A session's row shows whether it
  is working, queued, or waiting for you; hover or select it to see its branch,
  how far it is from its upstream and what is left to commit. Right-click a
  session to pin, rename, archive, refresh its repository from origin, or open
  it in the browser.
- **Notifications.** An approval or a question pops up once, with Approve and
  Deny, or its choices and Answer…, right there. Work that ends in a session
  you are not looking at says so quietly.
- **Status bar.** `$(inbox) N` while something waits, on every Core together;
  click it to see what.
- **Conversation.** Opening a session shows its conversation in an editor tab,
  in the colours of your theme: what you asked, what the agent said
  (markdown), its tool calls (one line each, click for the input and output),
  the files each job changed, what it published, and how each job ended, with
  its duration, tokens and cost. A finished job folds its steps into one
  "N steps" line. It follows the latest line like a chat until you scroll up;
  the message you are reading the answer to stays pinned at the top, and
  reaching the top loads earlier history. The header says whether the agent
  is working or waiting for you, on which backend, and on which branch.
  - **Answer in place.** Approvals and questions for this session sit above
    the field, with Approve and Deny, the choices, or a free answer; they go
    away as soon as they are answered anywhere.
  - **Write.** Enter sends, Shift+Enter is a newline. While a job runs, choose
    Queue, Next step or Interrupt (as far as the backend allows), and Stop it
    with the button or Escape. What you were writing is kept until sent.
  - **Pin, unpin, open in the browser** from the tab's title bar.
  - **Files.** A path the agent wrote opens in the editor when it is in your
    workspace. In a "files changed" card, each file opens the editor's diff
    view, built from the diff the backend computes: the whole file when your
    workspace has it on either side of the change, the changed regions
    otherwise, and the raw diff when no comparison is possible (a binary file,
    a diff cut short).
  - **Artifacts.** Open shows an image or a page in a tab of its own, and text
    as a read-only document; Save as… downloads it.
  - Panels come back after a reload of the window.
- **New session.** The **+** on a Project in the sidebar, in the view's title
  bar, or **Threavia: New Session** asks for the Project (unless you started
  from one), the backend and the working directory, the likely answer first
  each time, then opens a new session's conversation with the field ready.
  Nothing is created until you send the first message: that message starts
  the session, and the same tab becomes its conversation. Until then what you
  write is kept, one draft per Project, and comes back after a reload with
  its backend and directory; if Core refuses the start, it says why and the
  draft stays.
- **Ask Threavia about this.** Right-click in an editor (or a file in the
  Explorer), or run **Threavia: Ask Threavia About This…**: the selection, or
  the whole file, is sent with its path and lines and what you ask, to a new
  session (choose the Project, the backend, the working directory) or to a
  recent one, whose conversation then opens.
- **Tasks.** The Tasks view lists what a Project still owes, as the web
  client's Tasks page does: In progress, Ready to start, Waiting on other work
  (with what each one waits on), and Done, folded and counted. Each row offers
  its next step inline (Start, Mark done, Reopen); right-click for every
  status, Edit Title…, Edit Details…, Dependencies… (tick every task that
  must come first) and Delete…. **+** files a task: a title, details if you
  want them (Enter with nothing skips), then what it waits on. Clicking a task
  opens it as a read-only markdown preview, which follows the task as it
  changes.
- **Memory.** The Memory view lists a Project's decisions: what travels with
  every Job (the agent reads it before it starts), what is on record, and,
  folded, what was superseded and by what. A decision an agent recorded says
  "agent". The pin on a row makes it travel with every Job, or stops it;
  right-click to Supersede… (record the decision that replaces it) or
  Delete…. **+** records a decision: what was decided, why, and whether it
  travels with every Job. Each answer is one line in the editor's input box:
  the why of a decision is a sentence or two, and a document to write it in
  would be one more tab to find and close. Clicking a decision opens it as a
  markdown preview.
- **Which Project.** Both views show one Project, named next to their title
  (with its Core's name when there are several): the one of the session or
  Project last selected in the Sessions view, or of the conversation in front,
  kept with the workspace across a reload; until then the one a Core's
  `project` names, or your first. **Switch Project…** in their title bar
  chooses another, among the Projects of every Core. Both are kept live from
  the event stream, whoever made the change.

The extension tells Core when the editor has the focus, so your phone does not
ring for what you are already looking at.

## Install

Each [GitHub release](https://github.com/rclsilver/threavia/releases) carries a
`threavia-<version>.vsix`. Install it with **Extensions: Install from VSIX…**
in the command palette, or:

```sh
code --install-extension threavia-<version>.vsix
```

## Sign in

1. Open the Threavia view and choose **Add a Core**: its URL, such as
   `https://threavia.example.com`, or `http://localhost:8080` for a local Core,
   then the name it goes by (its host unless you say otherwise).
2. The extension asks that Core how it authenticates, and signs you in:
   - **OIDC**: your browser opens on your provider's sign-in page, then hands
     you back to the editor. The provider's client must allow the redirect
     `vscode://rclsilver.threavia/auth/callback` (and
     `vscode-insiders://rclsilver.threavia/auth/callback` for Insiders). Each
     Core names its own issuer and client, so this holds for the Keycloak
     client of every Core you add; Threavia's own deployment already allows it.
   - **Basic**: the editor asks for your username and password.
   - **None**: there is nothing to sign in to.

Tokens and passwords are kept in the editor's secret storage, one sign-in per
Core, and access tokens are renewed before they expire. **Threavia: Sign Out**
forgets them; each Core's account also shows in the editor's Accounts menu,
and signs out from there.

## Several Cores

Every Core you add is connected at the same time: a Core at work and one on
your laptop, say. **Threavia: Add Core…** adds one (also in the view's `…`
menu); **Rename Core…**, **Set Core URL…** (its address), **Remove Core**,
**Sign In** and **Sign Out** act on one, from the command palette (which asks
which) or from the Core's row in the sidebar.

- **Sidebar.** With one Core, the sidebar is as described above. With several,
  each Core has a row (its name, its host, and whether it is connected,
  connecting, signed out, or not answering), and its own Waiting, Pinned and
  Projects under it. A Core that cannot be asked says so under its row, with
  Sign In or Try Again, and the others carry on.
- **Status bar.** The count is the sum over every Core; its tooltip says how
  many wait where.
- **Notifications** come from every Core, and start with the Core's name.
- **New Session** and **Ask Threavia About This…** ask for the Core first,
  unless only one can be asked or you started from a Core's row.
- Conversations, drafts, diffs and documents each belong to their Core: two
  Cores can never mix up two sessions that happen to share an id.
- **Remove Core** asks first, then forgets its sign-in and closes its
  conversations. Nothing changes on the Core itself.

An earlier version's `threavia.coreUrl` (and `threavia.project`) becomes the
first entry of `threavia.cores` on the first start, sign-in included, and is
not read after that.

## Settings

| Setting | What it does |
| --- | --- |
| `threavia.cores` | The Cores to connect to, each `{ id, name, url, project? }`. `id` is generated; `project` names the Project of that Core you work in (by name or id): listed first and expanded, pinned sessions from other Projects name theirs, and Tasks and Memory show it until you turn to another. Edited by the commands above, or by hand. |
| `threavia.coreUrl`, `threavia.project` | Deprecated: moved into `threavia.cores` on the first start. |
| `threavia.clientName` | How this editor appears in your list of devices. Defaults to `VS Code — <hostname>`. |
| `threavia.notifications.attention` | Notify when an agent asks for an approval or an answer. |
| `threavia.notifications.jobEnded` | Notify when work ends in a session that is not open. |

## Development

The extension is TypeScript bundled by esbuild into `dist/extension.js`, and
the conversation page into `dist/webview.js` and `dist/webview-style.css` (with
the codicon font). The page is plain DOM, styled only with the editor's
`--vscode-*` theme variables; it renders markdown with markdown-it, raw HTML
off, under a Content-Security-Policy that lets it load only its own files and
run only its own script. It has no network access: everything goes through the
extension's client. Its
API types are generated from [`api/openapi.yaml`](../../api/openapi.yaml), as
the web client's are.

```sh
cd clients/vscode
npm ci
npm run build        # or: npm run watch
npm test             # unit tests, no editor needed
npm run lint
npm run typecheck
npm run generate:api # after a change to the contract
npm run package      # threavia.vsix
```

From the repository root, `make vscode-check` runs the generated-types check,
the type check, lint and tests (it is part of `make verify`), and
`make vscode-package` builds the `.vsix`.

To run it, open `clients/vscode` in VS Code and press **F5**: a second window
starts with the extension loaded, rebuilt first. Point it at a local Core with
**Threavia: Add Core…** (`http://localhost:8080`, mode `none`, needs no
sign-in); add a second one on another port to see the several-Core sidebar.

The code is split so that what can be tested without an editor is: `src/api`
(HTTP client, SSE stream, event effects), `src/auth/oidc.ts` and `pkce.ts`,
`src/tree/model.ts`, `src/attention/ledger.ts` and `src/cores/settings.ts` and
`refs.ts` (the Core list, its migration, and Core-qualified ids and URIs)
import nothing from `vscode`
and are covered by `test/`, as are the conversation's rules
(`src/conversation/timeline.ts` and `state.ts`, ported from the web client's
timeline), a new session's draft and how it becomes the session
(`src/conversation/draft.ts`), the diff reconstruction (`src/diff/unified.ts`), the "Ask" message
(`src/ask/message.ts`), the path matching (`src/workspace/paths.ts`), and how
Tasks and Decisions are grouped, which Project the views show and what they
send (`src/knowledge/model.ts`). The
rest turns them into views and commands; `src/webview` is the page.
