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

The extension is TypeScript bundled by esbuild into `dist/extension.js`. Its
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
and are covered by `test/`. The rest turns them into views and commands.
