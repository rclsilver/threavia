# Threavia

Threavia is a self-hosted, Kubernetes-friendly control plane for coding agents.
It owns durable projects, sessions, jobs, events and project knowledge, while
execution stays on autonomous **BackendInstances** such as a laptop running
Claude Code or a Kubernetes pod running Codex.

The same logical Session is usable from Web, Android, VS Code and Voice. Clients
are stateless views and controllers of Core state: a client disconnecting never
stops agent execution.

The architecture and the V1 scope are specified in
[`THREAVIA_SPEC_V1.md`](THREAVIA_SPEC_V1.md). Everything in this repository
follows that document.

## Status

The V1 surface of the specification is implemented. From the web client: pick a
Project, a BackendInstance and a KnownDirectory, send a message, watch Claude
Code work, answer its permission requests and questions, and continue in the
same provider session. Beyond that first slice, a Project carries tasks,
decisions, searchable history, artifacts, skills and instructions; an
ExecutionPolicy bounds what an agent may do; a Session can move to another
backend; and Core authenticates with none, basic or an OIDC issuer.

Every acceptance criterion of section 32 is covered:

| # | Criterion | Covered by |
| --- | --- | --- |
| 1 | Core starts with PostgreSQL and migrations | live run |
| 2 | Claude backend registers, connects outbound, becomes READY | live run |
| 3 | Clients see the backend, the Project and the KnownDirectory | API verified live; the web client is served from Core |
| 4 | First message atomically creates Session + Run + Job | live run + `TestFirstSendCreatesEverythingAtomically` |
| 5 | Backend starts Claude in the resolved cwd | live run |
| 6 | Agent output streams Backend → Core → PostgreSQL → SSE | live run + `TestStreamDeliversLiveEvents` |
| 7 | A replayed backend event does not duplicate history | `TestBackendEventDeduplication` |
| 8 | A validation waits indefinitely and is answered from a client | live run + `TestValidationWaitsAndResolves` |
| 9 | A reloading client gets a snapshot and continues live | `TestStreamResumesFromItsCursor` |
| 10 | A Core restart loses no history; unacknowledged events replay | live run + `TestJobFinishedDuringAnOutageIsNotRerun` |
| 11 | A Job is cancelled with CANCELLING semantics | live run + `TestCancellationNeedsBackendConfirmation` |
| 12 | A second message resumes the same native Claude session | live run + `TestSecondMessageResumesTheNativeSession` |

The live runs drove the real Claude Code CLI against real PostgreSQL and real
S3-compatible storage: it created a file after its Write permission was approved
through Core, answered a follow-up from memory of the first turn, survived a
Core restart mid-job, stopped on cancellation, read back the project Skills it
had been handed and the project instructions it had been given, and worked in a
directory the backend located and bound on its own.

What is deliberately not implemented, matching the non-goals of section 33:

- the Android, Voice and VS Code clients: Core is client-agnostic and the web
  client is the one written here;
- automatic backend scheduling, scoring, failover and transparent migration: a
  backend change is explicit;
- the REVIEW capability, Crit/ChangeSet integration and vector memory search;
- project sharing, roles and team models: V1 isolates per user;
- external notification delivery: Core says what is worth pushing and pushes
  nothing itself.

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

On NixOS, [`shell.nix`](shell.nix) provides Go, Node, gcc and helm:

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
make build         # build the web client, then both binaries into bin/
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

A known directory is optional. Give it a name such as `puppet` and the backend
locates it under its configured discovery roots when the Job starts, asking you
where it is only if it cannot (section 11):

```bash
export THREAVIA_BACKEND_DISCOVERY_ROOTS=$HOME/git,$HOME/dev
```

`.env.example` lists every environment variable with its default.

### Working on the web client

The client is a React application built with Vite
([`web/ui`](web/ui/README.md)). In development it is served by Vite rather than
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
make build         # the client, then both binaries
```

A binary built without the client says so on the page rather than serving a
blank one. The API is unaffected either way.

The TypeScript types come from [`api/openapi.yaml`](api/openapi.yaml), which is
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
make test-race     # test suite under the race detector (needs a C compiler)
make dev-reset     # stop the dependencies and delete their data
```

## Releasing

CI runs on every push and pull request: lint, then tests, then a build of every
target. Container images and the Helm charts are built on every run too, so a
broken Dockerfile surfaces long before release day, but they are published only
from a tag.

Tagging `vX.Y.Z` publishes:

- `ghcr.io/rclsilver/threavia-core:X.Y.Z` and
  `ghcr.io/rclsilver/threavia-backend-claude:X.Y.Z`, both linux/amd64 and
  linux/arm64;
- the Helm charts as OCI artifacts at `oci://ghcr.io/rclsilver/charts/threavia`
  and `oci://ghcr.io/rclsilver/charts/threavia-backend-claude`;
- a GitHub release with binary archives for linux and darwin, amd64 and arm64,
  and their SHA-256 checksums.

The flake needs no publishing: it is fetched from the repository, so the tag is
the release. A NixOS machine installs a backend from it with
`inputs.threavia.url = "github:rclsilver/threavia/vX.Y.Z"`, which is
[`docs/nix.md`](docs/nix.md).

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

`make verify` runs locally exactly what the lint job runs: formatting, module
tidiness, generated-code freshness for both the protocol and the API types,
`go vet`, `buf lint` and the test suite. `make docker` builds both images.

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
internal/backends/claude/        Claude adapter, runner and MCP bridge
internal/envutil/                environment parsing helpers
internal/logging/                shared structured logger
pkg/backend-sdk/                 reusable Go SDK for BackendInstances
migrations/                      embedded PostgreSQL migrations
deploy/docker/                   container images for Core and the Claude backend
deploy/helm/threavia/            Helm chart for Core
deploy/helm/threavia-backend-claude/  Helm chart for a Kubernetes backend
examples/backend-example/        smallest possible BackendInstance
docs/                            architecture, protocol and API notes
web/                             the embed, and the fallback when nothing is built
web/ui/                          React client: Vite, TanStack Query, Radix
shell.nix                        NixOS development shell
.github/workflows/ci.yml         lint, test, build, package, release
```

`internal/core/config`, `internal/envutil`, `internal/logging` and `gen/` are the
only additions to the proposed tree; the reasons are recorded in
[`docs/architecture.md`](docs/architecture.md).

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — what is implemented, and every
  implementation choice the specification left open
- [`docs/protocol.md`](docs/protocol.md) — the Backend ↔ Core control protocol
- [`docs/api.md`](docs/api.md) — the client HTTP API, and the contract it is
  generated from
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — what a change has to carry with it,
  the demo included

## License

Apache License 2.0, see [`LICENSE`](LICENSE).
