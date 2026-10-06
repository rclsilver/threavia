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

This is the repository skeleton of specification section 35. It compiles, it is
tested, and the two binaries start, connect to each other and shut down cleanly.
It deliberately does **not** implement the Core services yet.

What works today:

- `threavia-core` serves the client HTTP API (liveness, readiness, version) and
  the backend gRPC control service, and applies its database migrations.
- `threavia-backend-claude` connects outbound to Core, completes the
  Hello/Welcome handshake, reports its status and heartbeats, and reconnects on
  its own when Core restarts.
- The PostgreSQL schema holds Projects, BackendInstances, Sessions, Runs, Jobs
  and Events, with the one-active-Job-per-Run and backend event deduplication
  invariants enforced by the database.
- The backend SDK buffers events durably until Core acknowledges them, and
  replays them after a reconnection.

What is not implemented yet, in the order of specification section 31:

- the Core services: Projects, KnownDirectories, `StartSession`, messages, event
  persistence and the global sequence, SSE, validation and user input requests,
  cancellation, reconciliation;
- driving Claude Code itself (`internal/backends/claude/runner` returns
  `ErrNotImplemented`);
- backend registration and claim flows, so a development-only static token
  mapping stands in for real backend credentials;
- the SQLite durable state of the backend SDK, replaced for now by an in-memory
  store;
- the S3 client, the web client, Skills, Tasks, Decisions and Core Tools.

Every one of these has its interface in place; none of them is faked.

## Requirements

- Go 1.25 or later
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
make dev-up        # start PostgreSQL and MinIO
make migrate       # apply the database migrations
make test          # run the test suite
make build         # build both binaries into bin/
```

Run Core and a backend in two terminals:

```bash
# terminal 1
export THREAVIA_POSTGRES_PASSWORD=threavia
export THREAVIA_BACKEND_DEV_TOKENS='dev-token=00000000-0000-0000-0000-000000000001'
make run-core

# terminal 2
export THREAVIA_BACKEND_CORE_ADDRESS=localhost:9090
export THREAVIA_BACKEND_TOKEN=dev-token
export THREAVIA_BACKEND_TLS_ENABLED=false
make run-backend
```

The backend only connects once its id exists in `backend_instances`, because
registration is not implemented yet:

```bash
docker compose exec postgres psql -U threavia -d threavia -c \
  "INSERT INTO backend_instances (id, owner_id, name, ownership_status, operational_status)
   VALUES ('00000000-0000-0000-0000-000000000001', 'dev', 'laptop', 'CLAIMED', 'OFFLINE');"
```

Check Core:

```bash
curl localhost:8080/healthz
curl localhost:8080/readyz
curl localhost:8080/version
```

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
internal/backends/claude/        Claude adapter and runner
internal/envutil/                environment parsing helpers
internal/logging/                shared structured logger
pkg/backend-sdk/                 reusable Go SDK for BackendInstances
migrations/                      embedded PostgreSQL migrations
deploy/helm/threavia/            Helm chart
examples/backend-example/        smallest possible BackendInstance
docs/                            architecture, protocol and API notes
web/                             frontend, technology still to be selected
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
