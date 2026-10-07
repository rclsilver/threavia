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

The MVP vertical slice of specification section 30 works end to end: from the
web client, pick a Project, a BackendInstance and a KnownDirectory, send a
message, watch Claude Code work, answer its permission requests and questions,
and continue the conversation in the same provider session.

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

The live runs drove the real Claude Code CLI: it created a file after its Write
permission was approved through Core, answered a follow-up question from memory
of the first turn without rereading the file, survived a Core restart mid-job,
and stopped on cancellation.

What is deliberately not implemented, matching the deferrals of section 30 and
the non-goals of section 33:

- Core Tools, Tasks and Decisions: the contracts exist, the services do not;
- S3 Artifact flows, Skills, Android, Voice and cross-backend handoff;
- OIDC authentication, which is accepted by the contract and refused at startup
  rather than silently degrading;
- the directory discovery and clone fallbacks of section 11: an unbound
  directory is reported to the user instead of being guessed at;
- the breadth of ExecutionPolicy: the first slice runs interactively, which is
  what makes the validation flow observable.

## Requirements

- Go 1.26 or later. `GOTOOLCHAIN=auto`, the default, fetches it when the
  installed Go is older.
- Docker, for the local PostgreSQL and S3-compatible storage
- A C compiler, only for `make test-race`: the race detector is
  ThreadSanitizer, a C++ runtime, so a race-enabled binary needs cgo and the
  system linker. Nothing else in the repository uses cgo.
- `buf`, `protoc-gen-go` and `protoc-gen-go-grpc`, only to regenerate the
  protobuf bindings: `make tools` installs the pinned versions

On NixOS, [`shell.nix`](shell.nix) provides Go, gcc and helm:

```bash
nix-shell              # enter the shell
nix-shell --run make   # or run a single target
```

## Local development

```bash
make dev-up        # start PostgreSQL
make migrate       # apply the database migrations
make test          # run the test suite
make test-db       # add the database integration and end-to-end tests
make dev-up-storage # add S3-compatible object storage (Garage), configured
make build         # build both binaries into bin/
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

Then open <http://localhost:8080>, create a project, add a known directory and
bind it to a real path on that backend, and send a first message.

`.env.example` lists every environment variable with its default.

### Other tasks

```bash
make help          # list every target
make generate      # regenerate gen/ from api/proto
make lint          # go vet + buf lint
make test-race     # test suite under the race detector (needs a C compiler)
make dev-reset     # stop the dependencies and delete their data
```

## Repository layout

Matching specification section 25:

```text
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
internal/core/tools/             Core Tools exposed to agents
internal/backends/claude/        Claude adapter, runner and MCP bridge
internal/envutil/                environment parsing helpers
internal/logging/                shared structured logger
pkg/backend-sdk/                 reusable Go SDK for BackendInstances
migrations/                      embedded PostgreSQL migrations
deploy/helm/threavia/            Helm chart
examples/backend-example/        smallest possible BackendInstance
docs/                            architecture, protocol and API notes
web/                             web client, served from the Core binary
shell.nix                        NixOS development shell
```

`internal/core/config`, `internal/envutil`, `internal/logging` and `gen/` are the
only additions to the proposed tree; the reasons are recorded in
[`docs/architecture.md`](docs/architecture.md).

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — what is implemented, and every
  implementation choice the specification left open
- [`docs/protocol.md`](docs/protocol.md) — the Backend ↔ Core control protocol
- [`docs/api.md`](docs/api.md) — the client HTTP API

## License

Apache License 2.0, see [`LICENSE`](LICENSE).
