# Ollama backend

`threavia-backend-ollama` is a CODE BackendInstance whose models are served by
[Ollama](https://ollama.com). Ollama serves models, not agents: the agent is
[Codex CLI](codex.md), with Ollama as its model provider. Everything the Codex
backend does with hooks, approvals, the sandbox, skills, Core tools and
external MCP servers applies unchanged; this page lists only what differs.

## Requirements

- **Ollama 0.32.7 or later.** Codex sends MCP tools, Threavia's included, as
  namespaced tools; Ollama expands them and splits their calls back from that
  release on. An older server is reported as unavailable.
- **Codex CLI**, the same version as the Codex backend (0.161.0 packaged). No
  `codex login` and no `OPENAI_API_KEY`: nothing leaves for OpenAI.
- **A model that calls tools.** Only models whose `/api/show` capabilities
  include `tools` are offered. MoE models such as `gpt-oss:20b` or
  `qwen3-coder:30b` stay usable on a CPU.

## Run locally

```sh
export THREAVIA_BACKEND_CORE_ADDRESS=localhost:9090
export THREAVIA_BACKEND_CORE_API=http://localhost:8080
export THREAVIA_BACKEND_REGISTRATION_TOKEN="$TOKEN"
export THREAVIA_BACKEND_TLS_ENABLED=false # local Core only
export THREAVIA_BACKEND_OLLAMA_URL=http://localhost:11434
make run-backend-ollama
```

All shared `THREAVIA_BACKEND_*` settings apply. The Ollama backend adds:

| Variable | Default |
| --- | --- |
| `THREAVIA_BACKEND_OLLAMA_URL` | `http://localhost:11434` |
| `THREAVIA_BACKEND_OLLAMA_MODEL` | the first installed model able to call tools |
| `THREAVIA_BACKEND_OLLAMA_REASONING_EFFORT` | the model's default |
| `THREAVIA_BACKEND_OLLAMA_CONTEXT_LENGTH` | `32768` |
| `THREAVIA_BACKEND_CODEX_BINARY` | `codex` |
| `THREAVIA_BACKEND_STATE_PATH` | `~/.threavia/ollama/backend.db` |
| `THREAVIA_BACKEND_SKILL_CACHE_PATH` | `~/.threavia/skills/ollama` |
| `THREAVIA_BACKEND_SCRATCH_PATH` | `~/.threavia/ollama/scratch` |
| `CODEX_HOME` | `codex-home` beside the state |

`THREAVIA_BACKEND_OLLAMA_CONTEXT_LENGTH` must match the server's
`OLLAMA_CONTEXT_LENGTH`. The server does not report it, and Codex has to know
it: a conversation longer than the window Ollama allocates is cut short
without an error. Ollama's own default, 4096 tokens, is too short for an agent.

The backend keeps Codex's conversations in a `CODEX_HOME` of its own, so it can
run beside a Codex backend on the same account, and so ChatGPT credentials in
`~/.codex` never send Codex to OpenAI.

## What differs from the Codex backend

| Aspect | Ollama backend |
| --- | --- |
| Models | Read from the server (`/api/tags`, `/api/show`) at startup, then again with every status report (each minute) and every Job start: a model pulled or removed reaches the clients without a restart. |
| Reasoning efforts | The model's named thinking levels (`low`, `medium`, `high` for gpt-oss). A model whose thinking is only on or off offers none. |
| Status | DEGRADED with `ProviderUnreachable` when the server does not answer, is too old or has no tool-calling model. No authentication state. |
| Quotas | None: reported as unsupported. |
| Web | `web_fetch` only. `web_search` runs on OpenAI's hosted search, which Ollama does not have. |
| Sub-agents | Off: Codex does not run them reliably on Ollama. |
| File edits | Through the shell. Codex's `apply_patch` tool is a custom tool type Ollama does not declare. |
| Execution | Local only. `THREAVIA_BACKEND_EXECUTION_MODE=kubernetes` is refused. |

Codex is pointed at the server with a provider of its own
(`threavia-ollama`, Responses API, `<url>/v1`): the built-in `ollama` provider
cannot be moved off localhost by configuration. The backend writes the model
catalog Codex loads (`models.json` beside its state), with each model's context
window and thinking levels and short base instructions; the Threavia rules
arrive as developer instructions, as with Codex.

## Security

Ollama has no authentication. Anyone who reaches its port can run inference,
pull models and delete them. Bind it to an address only the backend and other
trusted clients reach.
