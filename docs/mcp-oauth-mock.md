# Local OAuth MCP mock

This disposable fixture exercises Threavia's external MCP integration without an
external account, real provider, paid inference or access to household services.
It is deliberately not an identity provider for production.

Build from this worktree:

```sh
docker build --network host -t threavia-mcp-oauth-mock:dev -f deploy/docker/mcp-oauth-mock.Dockerfile .
docker run --rm --network host --read-only --cap-drop ALL --security-opt no-new-privileges=true threavia-mcp-oauth-mock:dev
```

The process refuses a non-loopback listen address and origin. It listens at
`http://127.0.0.1:18084` by default. Do not publish it through an ingress or
reverse proxy. Browser and backend must reach the same loopback origin.

Register an HTTP MCP declaration with URL `http://127.0.0.1:18084/mcp`,
`oauth: true`, and no pre-registered OAuth client ID. Activate it in a test
project. Select these exact tools:

| Tool | Explicitly classify as read-only |
| --- | --- |
| `mock_read` | yes |
| `mock_list_notes` | yes |
| `mock_slow_read` | yes |
| `mock_create_note` | no |
| `mock_delete_note` | no |

Leave resources and prompts disabled: this fixture exposes only tools.

In Settings → MCP servers, select the development backend and click the visible
**Connect account…** action for this server. Click **Autoriser le test** on the
fictional account's consent page. After returning to Core, sign-in finishes
automatically. A dialog names the server and backend and confirms five tools
are available; **Review tools** opens discovery. There is no additional sign-in
completion button. **Check connection again** remains in the server's actions menu.
Reload the page: the same backend stays selected, the row checks the connection
again and shows **Connected**, the backend name and five available tools.
**View tools** opens discovery without another sign-in. An unavailable server
must show **Connection unverified** and a retry action, not a claim of disconnection.
Starting with **Refuser la connexion** must show a cancelled connection and must
not authenticate a new account.

OAuth uses a public client, dynamic client registration, an authorization code,
PKCE S256, exact redirect binding and MCP resource binding. Codes last one
minute and are single-use; access tokens last one minute and refresh tokens
rotate. Credentials are opaque, random and meaningful only to this fixture.
The mock account is always `mock-user`. No external identity or service is used.

Use a new Job after any MCP declaration or account change. For recipes:

1. Call `mock_read` to verify the fictional account.
2. Create a note with `mock_create_note` and text `test-mcp`. Deny one request,
   verify absence using `mock_list_notes`, then make a distinct approved request.
   Delete only the resulting note ID using `mock_delete_note`, approving cleanup.
3. Call `mock_slow_read` and use Stop during its thirty-second wait. The local
   `GET /stats` counters should show `slowCancelled` increasing and no completed
   slow read for that attempt. A subsequent read should still work.
4. Disconnect the account on the selected backend; a new Job/connection must
   fail to authenticate. Connect again and verify recovery.

`GET /healthz` reports liveness. `GET /stats` exposes only aggregate recipe
counters (no tokens or note contents) so cancellation and writes can be checked.
Keep this loopback-only.

All registrations, pending logins, grants and notes live in memory and disappear
when the mock restarts. Backend-stored grants then become invalid: disconnect
and reconnect through Core. Restarting the backend alone preserves its grants.

Automated tests simulate consent over HTTP and use the actual Threavia OAuth
client and MCP SDK. They verify refused consent, redirect constraints, wrong
PKCE/resource rejection, code replay rejection, discovery, fictional note CRUD,
refresh, logout, reconnect and cancellation reaching the server. They do not
validate browser rendering or human button clicks.

## VS Code check

Install the development VSIX and run **Developer: Reload Window**. Add the local
Core, then use **Threavia: Manage MCP Servers** → **List servers and test a
connection**, select `oauth_mock` and the development backend. Discovery should
return `CONNECTED` and five tools, using the account connected through Web.
Open the same test conversation to check its history: an unfinished tool from a
cancelled Job must say **Interrupted when work was cancelled.**, including after
closing and reopening the conversation. Calls with a real result retain it.
