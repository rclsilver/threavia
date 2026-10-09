# Threavia for VS Code

Follow and answer your Threavia agents without leaving the editor: what waits
for you, your pinned sessions, and every Project's sessions with what each one
is doing, kept live from Core's event stream.

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
- **Status bar.** `$(inbox) N` while something waits; click it to see what.
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

1. Open the Threavia view and choose **Set Core URL**, such as
   `https://threavia.example.com`, or `http://localhost:8080` for a local Core.
2. Choose **Sign In**. The extension asks Core how it authenticates:
   - **OIDC**: your browser opens on your provider's sign-in page, then hands
     you back to the editor. The provider's client must allow the redirect
     `vscode://rclsilver.threavia/auth/callback` (and
     `vscode-insiders://rclsilver.threavia/auth/callback` for Insiders).
   - **Basic**: the editor asks for your username and password.
   - **None**: there is nothing to sign in to.

Tokens and passwords are kept in the editor's secret storage, one sign-in per
Core URL, and access tokens are renewed before they expire. **Threavia: Sign
Out** forgets them.

## Settings

| Setting | What it does |
| --- | --- |
| `threavia.coreUrl` | Where Core answers. Empty until set. |
| `threavia.project` | The Project this workspace works in, by name or id. It is listed first and expanded, and pinned sessions from other Projects name theirs. |
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
**Threavia: Set Core URL** (`http://localhost:8080`, mode `none`, needs no
sign-in).

The code is split so that what can be tested without an editor is: `src/api`
(HTTP client, SSE stream, event effects), `src/auth/oidc.ts` and `pkce.ts`,
`src/tree/model.ts` and `src/attention/ledger.ts` import nothing from `vscode`
and are covered by `test/`, as are the conversation's rules
(`src/conversation/timeline.ts` and `state.ts`, ported from the web client's
timeline), the diff reconstruction (`src/diff/unified.ts`) and the path
matching (`src/workspace/paths.ts`). The
rest turns them into views and commands; `src/webview` is the page.
