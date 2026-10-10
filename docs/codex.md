# Codex backend

`threavia-backend-codex` is a CODE BackendInstance using the local
[Codex app-server protocol](https://developers.openai.com/codex/app-server).
The packaged CLI version is 0.161.0. It uses the same SDK and adapter as Claude
for registration, durable events, directory discovery, project context,
Core tools, skill bundles, validations and reconciliation.

It offers the same features as the Claude backend: the shared system prompt,
questions and validations, the four execution modes and fine rules, skills,
project memory and artifacts, web search and fetch, NOW/NEXT input, sub-agents,
plans and background commands. Each has passed real authenticated Codex
integration tests; see [Feature parity](#feature-parity) for how each one maps
and [Remaining work](#remaining-work) for what is still to validate.

## Run locally

Install Codex and authenticate with `codex login` on the account running the
backend, or provide `OPENAI_API_KEY`. Create a registration token on Core, then:

```sh
export THREAVIA_BACKEND_CORE_ADDRESS=localhost:9090
export THREAVIA_BACKEND_CORE_API=http://localhost:8080
export THREAVIA_BACKEND_REGISTRATION_TOKEN="$TOKEN"
export THREAVIA_BACKEND_TLS_ENABLED=false # local Core only
make run-backend-codex
```

`make build-go` builds `bin/threavia-backend-codex` alongside Core and Claude.
All shared `THREAVIA_BACKEND_*` settings apply. Codex adds:

| Variable | Default |
| --- | --- |
| `THREAVIA_BACKEND_CODEX_BINARY` | `codex` |
| `THREAVIA_BACKEND_CODEX_MODEL` | the account's configured model |
| `THREAVIA_BACKEND_CODEX_REASONING_EFFORT` | the model's default |
| `THREAVIA_BACKEND_STATE_PATH` | `~/.threavia/codex/backend.db` |
| `THREAVIA_BACKEND_SKILL_CACHE_PATH` | `~/.threavia/skills/codex` |
| `THREAVIA_BACKEND_SCRATCH_PATH` | `~/.threavia/codex/scratch` |

Codex's native conversations and authentication remain in its own home. Preserve
them together with Threavia's state to resume after replacing a backend. Use
different registration tokens and state paths for separate backend processes.

## Behaviour and policies

One app-server process runs each Job, in the directory Core resolved. A first
Job creates a persistent thread; following Jobs resume the saved thread id.
Resumption failure is reported and never silently creates another conversation.
Agent output and native tool events become ordinary Threavia events. Deltas
provide live progress; completed messages are persisted once. Codex reports
usage once per model request; the Job is charged for every request of the turns
it ran, its sub-agents' included, never for the earlier turns of a resumed
conversation. Codex does not report a monetary cost through this protocol.

`JOB_INPUT_NEXT` uses native `turn/steer`: the current turn reads the message
at its next step. `JOB_INPUT_NOW` interrupts
the active turn and starts the message in the same thread. Pending approval and
question waits are cancelled when their turn ends. A Core disconnection does
not stop the provider; the shared SDK persists and replays events.

The sandbox starts read-only in every mode. INTERACTIVE, GUARDED and AUTONOMOUS
use native `untrusted` approvals. Commands and file changes needing approval are checked through the
current Threavia policy before being approved once. AUTONOMOUS permits actions
the policy allows; INTERACTIVE asks for reads as well, and GUARDED uses native
read-only classification for commands without explicit rules. Duration and
action limits stop the provider process group.

SUPERVISED uses Codex's native `auto_review` reviewer with `on-request`
approvals. Standing supervision instructions are repeated in every user
message, including NOW and NEXT, so compaction does not silently drop them.
Explicit DENY rules still refuse; explicit ASK rules and Core tools marked
as requiring validation still ask the human. A policy without filesystem
writes disallows all native sandbox escapes, including reviewer-approved ones.
For operations without a native review path (the backend web tools, or commands
covered by local native allow rules), an isolated Codex reviewer evaluates the
request and falls back to a human when uncertain or unavailable. Changing a
supervised policy interrupts the old turn and continues with the new policy,
revoking pending automatic decisions.

Fine-grained rules support `DENY`, `ASK` and `ALLOW` for all seven Threavia
capabilities, with precedence `DENY > ASK > ALLOW`. Capability switches remain
binding. The backend installs a synchronous `PreToolUse` command hook, so
explicit rules also apply to commands Codex normally executes without asking.
`ASK` waits for a Threavia validation; `ALLOW` handles subsequent native approval
without prompting. Each part of a compound command must be covered before a
shell allow applies; substitutions and redirections require native approval.

Paths are normalized against the working directory and checked through symlink
targets. All patch paths, including move destinations, are checked before the
patch executes. Named paths in ordinary shell utilities are checked as well;
recursive searches spanning a refused subtree are declined. FILE allows apply
to elementary file utilities; scripts and programmable tools require a SHELL
allow. Host rules inspect written commands and tool inputs, not every file an
arbitrary program might open. Use SHELL refusals and account-level isolation
where arbitrary scripts must be constrained.

Rules can change during a Job. Pending approvals from an older policy cannot
authorize an action under the new one. Core tools enforce TOOL rules at the
endpoint itself, and tools requiring validation still ask in every mode.
A read-only policy never approves an unclassified shell escape.
Native `.rules` files no longer prevent a Job from starting. This app-server
version cannot ignore them, so the host checks commands before execution even
when a native allow rule would skip approval. GUARDED uses a conservative
read-only fallback in that case; unfamiliar commands may require more prompts.
Native deny rules can still further restrict execution.
Local Codex hooks are disabled for the Job through thread-scoped hook state.
The backend checks definitions with `hooks/list`, enables and trusts only its
own two hooks, and requires a SessionStart handshake before allowing tools.
Missing hook support or conflicting managed settings fail with
`POLICY_UNSUPPORTED`; no user/project configuration is modified.
Blocked native hooks produce failed tool events even when Codex does not emit
a native tool item. Native MCP consent forms for the private Threavia server
are accepted; policy checks and human validations happen at the endpoint.
Other servers and authentication elicitations are declined.
Broad permission overlays and session-wide permission grants are declined;
the agent can request approval for a specific command instead. The gate is a
guard rail inside the backend account's real permissions, as for Claude, and
does not classify every possible shell script as a security boundary.

Only the per-Job Threavia MCP endpoint is enabled; machine and project MCP
servers, apps, plugins, browser tools and goals are disabled for these Jobs,
as Claude's are, and local hooks except the backend's two policy hooks.
Unified exec runs without a terminal (see [Background commands](#background-commands)).
The MCP endpoint token is delivered through stdin; the command hook receives
its private endpoint through the Job process's environment, never argv.
Both are redacted from failure diagnostics.

Effective Core-managed and backend-local skills are registered through native
`skills/extraRoots/set` and verified with `skills/list`. Codex receives their
native names, descriptions and instructions for automatic discovery; explicit
`$skill-name` mentions also send typed skill inputs. Unrelated host/repository
skills are disabled for the Job. No AGENTS.md or persistent configuration is
modified.

## Feature parity

| Claude Code | Codex |
| --- | --- |
| `--append-system-prompt` | `developerInstructions`: the same text (`shared/runner.SystemPrompt`), including the executables on PATH and the scratch directory, plus the Codex-only lines below |
| Task (sub-agents) | native `multi_agent`, reported as `Task` |
| TodoWrite | `mcp__threavia__update_plan`, or the native plan when the CLI offers it, reported as `TodoWrite` |
| Bash `run_in_background` and its output | unified exec without TTY |
| WebSearch / WebFetch | `mcp__threavia__web_search` / `web_fetch` |
| `--plugin-dir` skills | `skills/extraRoots/set` |
| permission prompt tool | `PreToolUse` hook and native approvals |
| `auto` mode reviewer | native `auto_review` |
| interrupt + stream-json input | `turn/interrupt` + `turn/start`, `turn/steer` |

Both backends sweep Run scratch directories older than seven days at start.

### Sub-agents

Codex spawns sub-agents as threads of the same app-server process. Their
events carry their own thread id; the backend accepts a thread only after
`thread/read` traces its parent back to the Job's thread, and refuses
requests from any other. Their tool calls reach the same policy hook (under
the root session id) and their approvals the same gate, so DENY, ASK, ALLOW,
policy updates and the action limit apply to them unchanged.

Each stretch of a sub-agent's work is one `Task` tool call: opened by its
`subAgentActivity` *started* or *interacted* item, completed with its last
message, failed when interrupted or when the Job ends first. Its tool calls are
ordinary tool events; its messages are progress, not messages to the user, and
never the Job's summary. Its tokens are added to the Job's usage. NOW and
cancellation interrupt sub-agent turns as well as the root turn. The Run keeps
the root thread as its native id.

Spawning, messaging or waiting for a sub-agent acts on nothing by itself, so,
as Claude Code runs Task without asking, only a TOOL rule naming `Task`
applies to those calls.

### Plans

This CLI version does not offer its `update_plan` tool to every model. The
backend exposes `mcp__threavia__update_plan` (steps with `pending`,
`in_progress` or `completed`) and reports its calls, like any native
`turn/plan/updated`, as a `TodoWrite` call whose input carries `todos` in
Claude Code's shape. Only a TOOL rule naming `TodoWrite` applies to it.
Context compaction is reported as progress.

### Background commands

Codex 0.161.0 sends no hook and no approval for `write_stdin`; only a
`terminalInteraction` notification follows the write. With a terminal,
approving `sh` would let the agent type any command after the policy check.
The backend therefore enables unified exec with `unified_exec_tty=false`: a
command can run in the background and its output can be read back with an
empty `write_stdin`, but its stdin is closed and `tty: true` is refused. This
is what Claude Code offers (a background command, its output, its end).

A command starts in the read-only sandbox. One that fails there before its
yield time ends is retried outside it once approved; one already running in
the background is not. The instructions therefore tell the agent to request
escalated sandbox permissions, with a justification, for a background command
that writes or uses the network; that request goes through the same gate. When
the Job ends, is cancelled or reaches a limit, the backend calls
`thread/backgroundTerminals/clean` on the root and sub-agent threads before
stopping the process.

## Web tools

`mcp__threavia__web_search` and `mcp__threavia__web_fetch` expose search and URL
reading under the current Threavia policy, with normalized WebSearch/WebFetch
events. TOOL rules can use those normalized names or the full MCP name.
Native hosted search is disabled on the main thread so web requests go through
the backend policy gate. The search tool first obtains approval, then uses native hosted search
in an isolated ephemeral Codex thread with local tools and MCP disabled. It
requires an observed native search, returns source URLs/excerpts, supports
allowed_domains, and filters results from refused domains. Search results are
untrusted data; hosted search remains a provider service, not a local network
sandbox.
The search helper's hook permits only native `webrun` search queries; it blocks
page opening and all local tools. Reviewer helpers cannot use web tools.

WebFetch checks the URL and every redirect before issuing a request. Downloads
are limited to textual content and 2 MiB, with bounded transfer times and
64 KiB of extracted text. An optional extraction prompt runs in an isolated
helper. Policy changes and NOW/cancellation invalidate ongoing web actions.
These helpers share the authenticated app-server, require no additional API
key, and never replace the Run's persistent native thread.

Both web and VS Code already select CODE backends and consume the same events.
Every Codex feature above is reported in Claude Code's vocabulary (Bash, Edit,
Task, TodoWrite, WebSearch, WebFetch), so no client needs provider-specific
handling.

## Deploy

Docker: `deploy/docker/backend-codex.Dockerfile`, with an overridable
`CODEX_VERSION` build argument. Keep the home directory on a persistent volume.

```sh
helm install codex deploy/helm/threavia-backend-codex \
  --set core.address=threavia:9090 \
  --set core.api=http://threavia:8080 \
  --set core.registrationToken="$TOKEN" \
  --set provider.apiKey="$OPENAI_API_KEY"
```

The chart also accepts `provider.model` and `provider.reasoningEffort`.
Use an existing Secret for deployed credentials rather than storing keys in a
values file. For NixOS, import `nixosModules.backend-codex` and configure
`services.threavia-backend-codex`, setting `package` to
`packages.<system>.threavia-backend-codex` and either `codexPackage` or
`codexBinary`. The options otherwise match the Claude module.

## Validate

```sh
go test ./internal/backends/codex/...
THREAVIA_CODEX_SMOKE=1 go test ./internal/backends/codex/runner -run TestInstalledCodexHandshake -v
make dev-up
make test-codex-real
```

The automated tests drive a real subprocess simulating app-server, including
failures and blocked approvals, without credentials or inference. Tests cover
fine-rule precedence, file paths, patch moves, policy updates,
failed hook transport, reviewer allow/deny/ask, native skills, steered input,
isolated search, domain refusals, redirects and cancellation, sub-agent
routing and refusal of foreign threads, Task/TodoWrite mapping, per-request
usage and background cleanup. The Core integration test exercises
ALLOW, ASK and DENY through the real policy endpoint before native approval.
The opt-in smoke test checks the installed CLI's initialization and local configuration
and account RPCs; it makes no inference request. A real Core + Codex inference
run still requires PostgreSQL, provider authentication and an unrestricted
development environment. `make test-codex-real` uses the installed CLI's existing
authentication and real inference against a temporary migrated Core schema.
It covers skills, human validation/questions, web tools, Core memory/artifacts,
thread resumption, supervised edits, NOW/NEXT input and cancellation; and, in
a second test, two parallel sub-agents (one ASK answered once, one DENY
reported), a plan, a background command writing a file, a background command
stopped with its Job, and a sub-agent stopped by cancellation without changing
the native id. It can consume Codex usage. Override TEST_POSTGRES_URL and
optionally THREAVIA_CODEX_E2E_MODEL, and THREAVIA_BACKEND_CODEX_BINARY when
`codex` is not on PATH.
Do not interpret a skipped opt-in test as proof that real inference works.

## Remaining work

State as of 2026-10-10, validated against Codex CLI 0.161.0 with the unit
suite (race detector), `go vet`, both real tests above against a local
PostgreSQL, and `nix build` of both backend packages. Recheck the app-server
schema (`codex app-server generate-json-schema --experimental`) and feature
flags when moving to another CLI version.

Done since the first handoff: sub-agents, background commands, plans and
compaction events, the shared system prompt, scratch sweeping, per-request
usage accounting (the previous code charged only a turn's last request), and
the Nix `vendorHash` the new web code had invalidated.

### Deployment and compatibility validation

Not run: the Docker image, the Helm chart and the NixOS module on a real
deployment; recovery after a backend or Core crash, a replaced container or a
long disconnection; the web and VS Code flows against a running deployment;
any CLI version other than 0.161.0. Establish a supported CLI version policy
and test required RPCs and hooks, with their failure diagnostics, against it.

### Native extensions — scope to decide

Machine/project MCP servers, apps, plugins, browser/computer tools, goals and
code mode stay disabled, as Claude's equivalents are (`--strict-mcp-config`,
`--setting-sources ""`). This is a product decision for both backends, not a
parity gap. Each extension selected later needs configuration ownership,
rule mapping, credentials, consent routing, events and cancellation, tested
with ALLOW, ASK and DENY.

### Known constraints

- Interactive stdin (a TTY session the agent types into) stays disabled until
  the CLI submits `write_stdin` to hooks or approvals. Claude Code has no such
  feature either.
- Native `.rules` DENY decisions can further restrict Threavia ALLOW decisions;
  GUARDED may prompt more when native rules exist.
- WebFetch reads bounded textual HTTP(S) content only. Hosted search is a
  provider service: filtering refused domains from results does not prove the
  service never contacted them.
- Fine shell rules inspect commands and declared paths, not every operation of
  an arbitrary program, for both backends.
- Native automatic supervision can judge differently from Claude's reviewer.
- The sandbox mounts a private `/tmp`: a file under `/tmp` written outside the
  sandbox (for instance a skill cache placed there) needs approval to be read.
