# Development and releases

This guide is for contributors building Threavia from source. For packaged
installations and backend setup, start with the [installation guide](installation.md).
Run the commands below from the repository root.

## Requirements

- Go 1.26 or later. `GOTOOLCHAIN=auto`, the default, fetches it when the
  installed Go is older.
- Node 22 or later, to build the web client. The Go binary embeds it, so a
  release build needs this too, not only development.
- Docker, for the local PostgreSQL and S3-compatible storage
- A C compiler, only for `make test-race`: the race detector is
  ThreadSanitizer, a C++ runtime, so a race-enabled binary needs cgo and the
  system linker. Nothing else in the repository uses cgo.
- `buf`, `protoc-gen-go` and `protoc-gen-go-grpc`, only to regenerate the
  protobuf bindings: `make tools` installs the pinned versions

On NixOS, [`shell.nix`](../shell.nix) provides Go, Node, gcc and helm:

```bash
nix-shell              # enter the shell
nix-shell --run make   # or run a single target
nix develop            # the same, through the flake
```

## Local development

```bash
make dev-up        # start PostgreSQL
make migrate       # apply the database migrations
make test          # run the test suite
make test-db       # add the database integration and end-to-end tests
make dev-up-storage # add S3-compatible object storage (Garage), configured
make build         # build the web client, then all three binaries into bin/
```

Run the whole thing: Core in one terminal, a backend in another, the client in a
browser.

```bash
# terminal 1
export THREAVIA_POSTGRES_PASSWORD=threavia
make run-core
```

Create a registration token for the backend, then start it. It registers once,
stores its identity locally and never needs the token again.

```bash
# terminal 2
TOKEN=$(curl -s -X POST localhost:8080/api/v1/backend-tokens \
  -H 'Content-Type: application/json' -d '{"label":"laptop"}' | jq -r .token)

export THREAVIA_BACKEND_CORE_ADDRESS=localhost:9090
export THREAVIA_BACKEND_CORE_API=http://localhost:8080
export THREAVIA_BACKEND_REGISTRATION_TOKEN="$TOKEN"
export THREAVIA_BACKEND_TLS_ENABLED=false    # local Core only; remote access needs TLS
make run-backend
```

Then open <http://localhost:8080>, create a project and send a first message.

### Using Codex

The Codex backend drives `codex app-server` (CLI 0.161.0). Install the CLI and
authenticate on the backend machine with `codex login`, or supply
`OPENAI_API_KEY`. With a separate Core registration token:

```bash
export THREAVIA_BACKEND_CORE_ADDRESS=localhost:9090
export THREAVIA_BACKEND_CORE_API=http://localhost:8080
export THREAVIA_BACKEND_REGISTRATION_TOKEN="$TOKEN"
export THREAVIA_BACKEND_TLS_ENABLED=false
make run-backend-codex
```

Select that backend when starting a session in the web or VS Code client.
Its default identity and database are separate from Claude's, so both can run
on one machine. `THREAVIA_BACKEND_CODEX_MODEL` and
`THREAVIA_BACKEND_CODEX_REASONING_EFFORT` optionally override Codex's defaults.
See [the Codex backend guide](codex.md) for deployment, supported policies
and protocol checks. It supports the same features as the Claude backend:
automatic supervision, skills, sub-agents, plans, background commands and
policy-controlled web search/fetch. With the development database running,
`make test-codex-real` exercises the installed, authenticated CLI against a
temporary Core schema using real inference.

The [Agent Skills compatibility matrix](skills.md) covers discovery,
scripts, supporting files and permissions across Claude Code and Codex.

Without `THREAVIA_AUTH_MODE`, Core runs with no authentication: every request
acts as a single local user. That is why it then listens on `127.0.0.1:8080`
only, so nobody else on the network can start sessions on your backends. Set
`THREAVIA_HTTP_ADDR` to reach it from another machine, preferably with `basic`
or `oidc` authentication; Core warns at startup when it listens beyond loopback
without it.

A known directory is optional. Give it a name such as `puppet` and the backend
locates it under its configured discovery roots when the Job starts, asking you
where it is only if it cannot (section 11):

```bash
export THREAVIA_BACKEND_DISCOVERY_ROOTS=$HOME/git,$HOME/dev
```

Manage known directories in a project's **Directories** page, in account
settings for global directories, or through **Manage Known Directories…** in
VS Code. A global directory belongs to your account on this Core and is available
to all your projects. The session tools `known_directory_list`,
`known_directory_register`, `known_directory_update`, `known_directory_bind`,
`known_directory_unbind` and `known_directory_delete` manage the same metadata;
listing and registration accept `scope: "global"`, with the current project as
the default. Forgetting metadata never deletes backend files.

On touch devices, including Android, Enter adds a line in new and existing
messages; tap the send button to send. Desktop keeps Enter to send and
Shift+Enter for a newline.

In **Settings → Backends**, use a backend's menu to set or clear your default.
VS Code offers **Set Default Backend…** and the Backends context menu. The
choice is stored per account on this Core, shared by every project and client,
and proposed first for new sessions. Existing sessions keep their backend.
Revoking the default backend clears the preference.

[`.env.example`](../.env.example) lists common development settings.

### Working on the web client

The client is a React application built with Vite
([`web/ui`](../web/ui/README.md)). In development it is served by Vite rather than
by Core, so editing a component reloads the browser without rebuilding or
restarting the Go binary:

```bash
# terminal 1
make run-core

# terminal 2
make dev-web       # http://localhost:5173, hot reload, in a container
```

Everything the client calls is proxied to Core, so the browser stays on a single
origin: no CORS, and the event stream behaves exactly as it does in production.
The container runs on the host network, so it reaches Core at `localhost:8080`
with no gateway or firewall rule to arrange. `THREAVIA_CORE_URL` points the
proxy elsewhere, and `make web-dev` runs the same server with no container.

In production the client is built and embedded in the binary, so a release is
one file:

```bash
make web           # build into web/ui/dist
make build         # the client, then all three binaries
```

A binary built without the client says so on the page rather than serving a
blank one. The API is unaffected either way.

The TypeScript types come from [`api/openapi.yaml`](../api/openapi.yaml), which is
the contract Core serves at `/api/spec.json`. A route the server registers and
the document omits fails `TestEveryRouteIsInTheOpenAPIDocument`, and a renamed
field breaks the web build rather than a user's screen.

### Other tasks

```bash
make help          # list every target
make generate      # regenerate gen/ from api/proto
make web-generate  # regenerate the TypeScript API types from api/openapi.yaml
make lint          # go vet + buf lint
make web-lint      # type-check and lint the web client
make vscode-check  # check, lint and test the VS Code extension
make vscode-package # build the VS Code extension into clients/vscode/threavia.vsix
make test-race     # test suite under the race detector (needs a C compiler)
make dev-reset     # stop the dependencies and delete their data
```

## Releasing

CI runs on every push and pull request: lint, then tests, then a build of every
target. Container images and the Helm charts are built on every run too, so a
broken Dockerfile surfaces long before release day, but they are published only
from a tag.

Tagging `vX.Y.Z` publishes:

- `ghcr.io/rclsilver/threavia-core:X.Y.Z`,
  `ghcr.io/rclsilver/threavia-backend-claude:X.Y.Z` and
  `ghcr.io/rclsilver/threavia-backend-codex:X.Y.Z`, all linux/amd64 and
  linux/arm64;
- the Helm charts as OCI artifacts at `oci://ghcr.io/rclsilver/charts/threavia`,
  `oci://ghcr.io/rclsilver/charts/threavia-backend-claude` and
  `oci://ghcr.io/rclsilver/charts/threavia-backend-codex`;
- a GitHub release with binary archives for linux and darwin, amd64 and arm64,
  the VS Code extension as `threavia-X.Y.Z.vsix`, and their SHA-256 checksums.

The flake needs no publishing: it is fetched from the repository, so the tag is
the release. A NixOS machine installs a backend from it with
`inputs.threavia.url = "github:rclsilver/threavia/vX.Y.Z"`, which is
[`docs/nix.md`](nix.md).

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

`make verify` runs locally exactly what the lint job runs: formatting, module
tidiness, generated-code freshness for both the protocol and the API types,
`go vet`, `buf lint` and the test suite. `make docker` builds all three images.

The Core image is a static binary on a minimal Alpine that carries git, which
installing a Skill from a git source runs, with the web client
embedded. The Claude backend image is larger by necessity: it ships Node, the
Claude Code CLI and git, because the agent needs a real userland to work in.
Provider credentials are supplied at runtime and never baked into it.

## Repository layout

Matching specification section 25:

```text
api/openapi.yaml                 the client API contract, served and generated from
api/proto/threavia/backend/v1/   protobuf definitions of the backend protocol
gen/threavia/backend/v1/         generated Go bindings, never edited by hand
cmd/threavia-core/               Core binary
cmd/threavia-backend-claude/     Claude BackendInstance binary
cmd/threavia-backend-codex/      Codex BackendInstance binary
internal/core/api/               client HTTP/JSON + SSE surface
internal/core/auth/              none / basic / oidc authentication
internal/core/backendconn/       backend gRPC control connections and leases
internal/core/config/            environment configuration
internal/core/domain/            identifiers, enums and state transitions
internal/core/events/            persistent event model and envelope
internal/core/service/           Core application services
internal/core/storage/postgres/  connection pool and migrations
internal/core/storage/s3/        S3-compatible object storage
internal/core/skills/            acquisition and packing of project skills
internal/core/tools/             Core Tools exposed to agents
internal/backends/shared/        adapter, execution contracts, policy gate, MCP, skills and workspace
internal/backends/claude/        Claude Code runner
internal/backends/codex/         Codex app-server runner and approval translation
internal/envutil/                environment parsing helpers
internal/logging/                shared structured logger
pkg/backend-sdk/                 reusable Go SDK for BackendInstances
migrations/                      embedded PostgreSQL migrations
deploy/docker/                   container images for Core, Claude and Codex
deploy/helm/threavia/            Helm chart for Core
deploy/helm/threavia-backend-claude/  Helm chart for a Kubernetes backend
deploy/helm/threavia-backend-codex/   Helm chart for a Codex Kubernetes backend
examples/backend-example/        smallest possible BackendInstance
docs/                            architecture, protocol and API notes
web/                             the embed, and the fallback when nothing is built
web/ui/                          React client: Vite, TanStack Query, Radix
shell.nix                        NixOS development shell
.github/workflows/ci.yml         lint, test, build, package, release
```

`internal/core/config`, `internal/envutil`, `internal/logging` and `gen/` are the
only additions to the proposed tree; the reasons are recorded in
[`docs/architecture.md`](architecture.md).
