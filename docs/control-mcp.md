# Control another Threavia over MCP

Core exposes a **Streamable HTTP** MCP server at `/mcp`. A main Threavia can
register a development Core as an external MCP server and run integration
recipes there: create projects and directories, start and follow sessions,
send messages, answer questions, accept or refuse permissions, stop work and
clean up. The same endpoint works with other MCP clients and authenticated
production deployments.

The public endpoint is separate from the private, per-Job backend endpoint.
It requires a dedicated Bearer token even when the target's normal client API
uses `AUTH_MODE=none`. Basic and OIDC client credentials do not authenticate MCP;
control tokens do not authenticate the normal Basic/OIDC client API.

## Grant a connection

On the **target Core**, open **Settings → MCP access**. Create a named access,
choose its rights and expiry, then copy its token once into private storage on
the calling backend. Core stores a SHA-256 digest, not the token. Listing
accesses never returns a token or its digest. Revocation keeps the record and
audit and refuses further calls on existing connections as well as new ones.
An already accepted operation or a Job it started is not undone by revocation.

Rights are independent and cover this account's resources across its projects:

| Scope | Operations |
| --- | --- |
| `read` | Identify target and grants; read projects, directories, bindings, sessions, history, jobs, attention, backends, models, policies, MCP configuration and audit; wait for events |
| `manage` | Create/update/archive/restore/delete resources, bind directories, send messages, move sessions, stop Jobs, claim/revoke backends, issue one-shot backend registration tokens, choose default backend and set policies, configure external MCP servers and backend connections |
| `answer` | Answer a pending question in a specified owned session |
| `approve` | Accept **or refuse** a specific permission, with its unchanged payload hash |

The form defaults to **read only**. Delegated approval is a separate explicit
grant; it is never implied by `manage`. Tokens cannot create other control
tokens, change their own scopes or execute commands on Core. Tools are hidden
when their scope is absent, and calls check the live grant again.

Remote permission receipts, audit entries and question responses identify
`mcp:<accessId>` and channel `mcp`. Messages show **MCP controller** in Web and
VS Code; provider prompts identify them as agent-authored messages under the
existing assignment. Delegated decisions are not recorded as human clicks.
This does not grant the human-review approval workflow or manufacture the human
receipt required by the existing local delegation relay tool.

VS Code's **Manage MCP Servers** offers a link to incoming MCP access settings.
Access creation and secret display live in Web, like full outgoing MCP
configuration, rather than opening a token-bearing editor document.

## Connect ChatGPT or another OAuth client

Clients that support MCP OAuth, such as ChatGPT connectors, need no token from
Settings. Core is the OAuth 2.1 authorization server of its own `/mcp`:

1. In ChatGPT, add a connector with the URL `https://threavia.example.com/mcp`
   and OAuth authentication. Leave the client ID empty: ChatGPT registers itself.
2. ChatGPT opens the consent page `/oauth/consent` in the Web client. Sign in as
   usual, check that the return address is the client you are connecting, and
   choose the rights. Only **read** is ticked by default, whatever the client
   asked for; tick more only if needed.
3. **Allow** returns to ChatGPT, which receives its tokens. **Deny** returns with
   `access_denied` and creates nothing.

The grant appears in **Settings → MCP access** under the client's name, marked
*Connected with OAuth*. Revoking it ends the connection at once: access tokens
and refresh tokens stop working. The name is chosen by the client when it
registers, so the consent page also shows where it will send you back.

The flow follows the MCP authorization specification: a 401 from `/mcp` points
to `/.well-known/oauth-protected-resource/mcp` (RFC 9728), which names Core as
authorization server (`/.well-known/oauth-authorization-server`, RFC 8414).
Clients register at `/oauth/register` (RFC 7591, public clients only, HTTPS or
loopback redirect URIs), use the authorization code with PKCE `S256` and an
optional `resource` that must be this Core's `/mcp` (RFC 8707). Codes last 5
minutes and work once; access tokens last 1 hour; refresh tokens rotate, work
once and last 60 days from their issue. Only digests are stored.

ChatGPT's servers must reach `/mcp`, `/.well-known/*` and `/oauth/*` over HTTPS
from the Internet; the consent page only needs to reach the Web client from
your browser. Set `THREAVIA_PUBLIC_URL` (Helm: `http.publicURL`) to the public
address, or make the proxy send `X-Forwarded-Proto: https`.

Settings tokens and the bootstrap token keep working as before. Migration
**33** adds the OAuth tables; nothing changes for backends or VS Code.

## Bootstrap a disposable development Core

The controller may provision these environment variables before starting Core:

- `THREAVIA_MCP_BOOTSTRAP_TOKEN`: a private random token, 32–256 bytes; or
  `THREAVIA_MCP_BOOTSTRAP_TOKEN_FILE`: a readable private file containing it.
  Configure exactly one source. Never put its value in a command argument,
  declaration, URL, shared artifact or conversation.
- `THREAVIA_MCP_BOOTSTRAP_OWNER`: the target account identifier (`dev` for the
  example below; use the actual Basic/OIDC account identity elsewhere).
- `THREAVIA_MCP_BOOTSTRAP_SCOPES`: comma-separated rights; defaults to `read`.

Initialization is **insert only**, once per owner/database. Changing environment
values cannot widen the grant, replace its secret or resurrect it after
revocation. Use Settings to issue a replacement token; deleting the disposable
database starts a genuinely new instance. No bootstrap is enabled by default.

[`deploy/dev/control.compose.yaml`](../deploy/dev/control.compose.yaml) is a
Linux host-network recipe, bound strictly to loopback: HTTP `18085`, backend
gRPC `19095`, PostgreSQL `55458`. Put only the bootstrap token assignment in a
private env file outside the checkout, with mode `0600`, and provide its path:

```sh
THREAVIA_CONTROL_ENV_FILE=/private/control-dev.env docker compose \
  --project-name threavia-control-my-test \
  -f deploy/dev/control.compose.yaml up -d --build --wait
```

This recipe grants all four rights **only to its disposable dev account**.
Normal deployments should choose their scopes explicitly. It starts Core and
PostgreSQL, with no provider login or inference. Register a separately configured
Claude/Codex backend against the dev gRPC address when a recipe needs an agent.
Provider credentials remain on that backend. Use a different Compose project
name and unused ports for simultaneous stacks. Build/start/stop/restart are
performed by the main instance's existing Compose commands; Core needs no
Docker socket. After validation and an explicit merge, purge the test stack:

```sh
THREAVIA_CONTROL_ENV_FILE=/private/control-dev.env docker compose \
  --project-name threavia-control-my-test \
  -f deploy/dev/control.compose.yaml down --volumes --remove-orphans
```

## Connect the main instance

Provision a backend environment variable such as `DEV_THREAVIA_AUTHORIZATION`
with the complete `Bearer ` header value. On the **main Core**, register a
declaration like this, discover its tools, then enable it in the controller's
project:

```json
{
  "name": "dev_threavia",
  "transport": "http",
  "url": "http://127.0.0.1:18085/mcp",
  "headers": { "Authorization": "DEV_THREAVIA_AUTHORIZATION" },
  "oauth": false,
  "tools": ["instance_info", "project_create", "project_read", "project_delete", "session_create", "session_read", "session_wait", "session_send", "session_answer", "session_resolve_validation", "job_cancel", "session_delete"],
  "readTools": ["instance_info", "project_read", "session_read", "session_wait"],
  "writeTools": [],
  "resources": false,
  "prompts": false,
  "enabled": true
}
```

Loopback means the **calling backend's** machine/network namespace. Use HTTPS
for another machine; the calling backend must be able to reach the target.
The target's existing allowed-host configuration also applies to `/mcp`.

The token grants rights **on the target**. The main instance separately decides
whether its agent may call an external write without asking. Under **Settings →
MCP servers → Write permissions**, authorize only the selected tools needed for
the recipe. Each addition names the server/tool and requires confirmation.
This fills `writeTools`, an exact-name list separate from `readTools`; never
classify a mutation as a read. Unselected/new tools stay unavailable, and writes
absent from `writeTools` still ask, even in autonomous mode. Explicit policy
`DENY`, `ASK`, disabled network access and the human-review checkpoint remain
effective. Both settings changes and Job snapshots take effect for subsequent
Jobs; update calling backends to use write preauthorization. Older backends
continue asking rather than silently accepting writes.

## Run and observe a recipe

Start with `instance_info` to confirm the target and effective scopes. Directory
tools distinguish global `known_directory_list/create` from project
`project_directory_list/create`; binding tools take an explicit backend ID.
`session_create` starts work immediately with a first message, target backend,
directory and optional model/effort. Empty draft sessions are not persisted.

Supply stable `idempotencyKey` values for `session_create` and `session_send`.
Retries return the same work; keys are isolated by token and operation. The
client never automatically replays an uncertain write. Inspect state before
retrying other mutations after a transport failure.

`session_read` includes recent events, jobs and pending attention. Use `before`
to page backwards. `session_wait` reads events forwards, at most 100 per page,
and waits at most 30 seconds. Pass its returned `cursor` as `afterSequence`,
including on a new connection after a cut. `hasMore` says to drain another page;
`timedOut` is **not** completion. `finished` describes Jobs, not whether every
event page has been consumed. Cursors advance only to the last returned event,
not a global snapshot position that might skip results. Waiting is scoped to
this active call; this V1 does not wake an inactive controller conversation.

To follow a message, pass the job id returned by `session_send` as `jobId` and
`until: "attention"`: the call returns only when that Job asks a question,
needs a permission or ends, with the Job as `job` and only its pending
requests in `attention`, rather than at every streamed event. Answer or decide,
then wait again with the returned `cursor`.

Answer questions with `session_answer`; permission decisions require the separate
`session_resolve_validation` tool and the original `payloadSha256` from attention.
Both require the matching session and request IDs. Choice questions reject
unoffered answers; a second permission decision returns a conflict. `job_cancel`
requests cancellation; follow events until the backend confirms its outcome.
Cancellation does not undo an external side effect already committed.

Project knowledge and files are reachable too. `read` covers `task_list`,
`task_ready`, `decision_list`, `decision_global_list`, `memory_search`,
`project_history_search`, `artifact_list`, `artifact_metadata`, `artifact_read`
(text up to 256 KiB, metadata only for binary files), `attention_list`,
`session_pinned_list`, `session_files`, `session_diff`, `session_repository` and
`audit_read`. `manage` covers `task_create`, `task_update`, `task_delete`,
`task_dependency_add/remove`, `decision_create`, `decision_global_create`,
`decision_set_importance`, `decision_make_global`, `decision_delete`,
`artifact_rename`, `artifact_delete`, `session_snooze`, `session_unsnooze` and
`session_mark_read`. Each maps to the HTTP route of the same contract.

For the external MCP recipe, configure only the fictional
[OAuth mock](mcp-oauth-mock.md), refuse the first note creation, accept the
second, inspect the result and cleanup, interrupt the slow read, then resume.
The remote MCP backend operation supports configuration/diagnostics and OAuth
protocol calls, but this V1 does not automate a browser consent screen or
desktop/mobile/VS Code UI tests.

## Validation and upgrade

Update Core first (migration **27** creates `control_accesses`), then calling
backends. No backend protocol change is required. Automated coverage includes
two isolated Cores, real HTTP MCP, the existing backend MCP proxy and policy
gate, real PostgreSQL and gRPC fixtures, owner/scope checks, expiry/revocation,
insert-only bootstrap, permission reject/accept, questions, cancellation,
cleanup, idempotency and forward pagination after reconnect. Provider execution
is simulated: these tests do not spend inference credits or prove browser/OAuth
UI behavior.
