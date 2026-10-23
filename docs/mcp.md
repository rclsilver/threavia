# Existing MCP servers

Threavia manages existing MCP servers; it does not reimplement their tools.
Register a local **stdio** executable or a remote **Streamable HTTP** endpoint
under **Settings → MCP servers**. The registry belongs to your account on Core.
Enable declarations in individual projects, then optionally restrict a session
under **Session settings → MCP servers**. A session cannot select a server its
project has not enabled.

Declarations are captured durably when a Job first dispatches. Configuration
changes, project activation and session restrictions apply to subsequent Jobs;
they do not replace the tools of an active Job or its retry snapshot. Different
backends can require different local credentials and installed executables.

## Add and discover

The configuration editor accepts the portable declaration below. No command is
started when a declaration is saved. **Test connection** runs initialization
and discovery on the selected backend; it may launch the configured local
server. It does not invoke any of that server's tools.

```json
{
  "name": "documentation",
  "transport": "http",
  "url": "https://docs.example.test/mcp",
  "tools": [],
  "readTools": [],
  "resources": false,
  "prompts": false,
  "oauth": false,
  "enabled": false
}
```

This address is illustrative, not a functioning service. Select actual tools
after testing your server. New tools are never enabled automatically. A
server's `readOnlyHint` is not sufficient: the owner explicitly classifies
selected read tools in `readTools`. Unclassified calls require human approval;
session policies and explicit refusals still apply. Resources and prompts are
separate opt-in read surfaces, exposed through capability-aware wrapper tools.
Sampling, elicitation and server-initiated automations are not enabled.

A declaration's `revision` prevents stale edits from overwriting a newer
configuration. Reload after a conflict. Deletion removes the declaration and
project activations, not software or backend-local credentials. Existing Jobs
keep their connections and snapshots. Review the affected projects first.

## Local servers and secrets

For stdio, replace `url` with `command` and optional `args`. Install the server
separately on each backend; Threavia does not install dependencies. Do not use
install-on-demand launch commands. The executable runs with the backend's OS
permissions; a declaration and tool filter are not a sandbox.

`env` maps subprocess environment names to environment-variable references on
the backend. `headers` maps HTTP header names to the same kind of reference:

```json
{
  "headers": { "Authorization": "GITHUB_MCP_AUTHORIZATION" }
}
```

Provision `GITHUB_MCP_AUTHORIZATION` on the backend with the entire header value
(including `Bearer ` when needed). Never put literal keys in declarations,
URLs, arguments or conversation messages. Values are not stored in Core or
returned by listing tools. Local processes receive only a small baseline
environment plus the references explicitly configured for that server.

## OAuth from another device

For supported remote servers, set `oauth: true` and choose **Connect account**.
The browser signs in with the provider and returns to the same Core's MCP
settings. Threavia finishes sign-in automatically on the backend where it was
started, then checks the available tools. A dialog names the server and backend
and shows progress, success or a recovery action. No additional completion
button is required. **Review tools** opens discovery without changing tool
permissions; **Check connection again** is also available in the server's actions menu.
After a page reload, HTTP servers configured for OAuth are checked again on the
selected backend. The row shows **Checking connection**, **Connected** with the
available tool count, or **Connection unverified** with a retry action. A failed
check does not imply that credentials were removed. The backend choice survives
reloads in the same browser tab. **View tools** opens the last verified discovery;
reconnecting and disconnecting are available in the server's actions menu.
Opening the page never starts a stdio process.
An interrupted page load preserves the return. A reload between successful
exchange and discovery repeats only discovery. Failed exchanges are not replayed
automatically: use **Connect again** for a new authorization.
The backend checks state, callback, issuer metadata, PKCE and resource binding.
Tokens and refreshed grants are stored in a private `mcp-tokens` directory next
to backend state, with directory mode 0700 and file mode 0600. The sign-in link
expires after ten minutes and the response can only be used once.

The initial implementation supports public OAuth clients, protected-resource
metadata, authorization-server metadata and PKCE S256. Use `oauthClientId` for a
pre-registered public client, otherwise the provider must allow dynamic client
registration. Register the exact callback `<Core origin>/settings/mcp` with the
provider when required. Confidential clients, device authorization and legacy
SSE are not supported in this version. Header references remain an alternative
for existing API-key/token authentication. A sign-in is backend-local; moving
to another backend does not transfer its credentials. Disconnecting credentials takes
effect immediately on that backend. Reconnecting an account does not silently
switch the identity of existing Jobs: they must start a new Job to use it.

## Startup and recovery

A required server that cannot connect or no longer exposes a selected tool
prevents the agent from starting. The Job reports the reason and can be retried
after fixing configuration. Optional servers are omitted with an availability
warning in the Job context. A backend without `EXTERNAL_MCP` cannot run required
servers; optional servers are explicitly reported unavailable.

Calls are bounded and never automatically replayed after a transport error.
An interrupted write can have an uncertain outcome: verify it before repeating
it. A new Job or explicit test creates a new connection. Closing the Job closes
its MCP sessions and local child processes. There is no cross-Job shared
execution connection or hidden background reconnect loop.

## Import and clients

Import from Codex reads the selected backend's `CODEX_HOME/config.toml` (default
`~/.codex/config.toml`); import from Claude reads `~/.claude.json`. Both return
**previews**. They do not alter those files, save declarations, activate servers
or copy OAuth grants. Inline values, arbitrary arguments and credential-bearing
URLs are omitted rather than transferred to Core; review the notes, re-enter
non-secret arguments and configure references before saving. Names must use
lowercase letters, digits and underscores and start with a letter.

The Web client supplies full configuration, import preview, discovery,
authentication and project/session selection. VS Code provides **Threavia:
Manage MCP Servers** with native list, project activation, session selection
and connection diagnostics, plus a link to full Web configuration. Existing
conversation approvals work in both clients. The browser-only demo uses
invented declarations and simulated diagnostics; OAuth is unavailable there.

Agents can manage declarations with `mcp_server_list`, `mcp_server_add`,
`mcp_server_update`, `mcp_server_delete` and `mcp_server_bind`. Mutations require
human validation even in autonomous mode, use the same services as the clients,
and produce attributed audit/events. No tool returns secret values.

## Compatibility and validation

Update Core (migration 26), then the backends. Providers continue seeing only
the private Threavia endpoint: unmanaged host/project MCP configuration stays
disabled. External tools are proxied through this endpoint, with short stable
aliases and policy checks against their original `mcp__server__tool` identity.

Automated tests use local stdio and HTTP fixture servers, a fake OAuth provider,
and a temporary PostgreSQL instance. They do not use GitHub accounts, actual
documentation services or paid model inference. Real-provider OAuth quirks and
visual desktop/mobile/VS Code checks need an installation test; automated
coverage is not evidence that every MCP server is compatible.
