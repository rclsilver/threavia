# Install Threavia

Install **Core**, connect **at least one backend**, then open the web client or
[VS Code](../clients/vscode/README.md#install). Core does not execute coding
agents itself. A backend can run beside Core, in a cluster, or on a laptop.

Use a version from [GitHub releases](https://github.com/rclsilver/threavia/releases).
Image and chart versions omit the `v` prefix; Git tags include it. Pin Core and
new backends to the same release. The Compose environment example pins 0.22.0;
change it when choosing another release. Documentation on `master` may describe
features newer than a published release.

## Docker Compose

This local setup needs Docker with the Compose plugin and Git. It runs the
published Core image and PostgreSQL 17, with no source build or Node installation.

From the repository root:

```sh
cp deploy/docker/compose.env.example .env.docker
```

Edit `.env.docker`: select `THREAVIA_VERSION` and replace
`THREAVIA_POSTGRES_PASSWORD` before the first start. Keep this file private and
untracked. Then:

```sh
docker compose --env-file .env.docker -p threavia -f deploy/docker/compose.yaml up -d
docker compose --env-file .env.docker -p threavia -f deploy/docker/compose.yaml logs -f core
```

Open <http://localhost:8080>. Check startup with:

```sh
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
curl -fsS http://localhost:8080/version
```

The web application and HTTP API use port **8080**. Backends use **9090** for
their control connection. Both published ports bind to `127.0.0.1` only;
PostgreSQL has no published port. This example uses `auth.mode: none` and
plaintext connections for local evaluation. Connect a backend running on the
same host using the [local settings below](#connect-a-backend).

Database data lives in the `threavia_postgres-data` volume. Stop the services
without deleting it:

```sh
docker compose --env-file .env.docker -p threavia -f deploy/docker/compose.yaml down
```

Use the same project name and environment file when starting them again.
Changing the password variable after PostgreSQL has initialized does not change
its stored password: rotate the database credential and Core configuration
together. Back up PostgreSQL and object storage before an upgrade. Change the
pinned release and run `up -d` to update; Core applies pending migrations at
startup. Downgrading an image does not undo database migrations.

Attachments, artifacts and Core-managed Skills require
[S3-compatible storage](#enable-artifacts-and-skills). For access from another
machine, follow [remote access and authentication](#remote-access-and-authentication)
and adapt the Compose configuration; changing the port bindings alone is not
a complete remote installation.

## Kubernetes / Helm

The Core chart deploys Core and its services. You provide:

- PostgreSQL, its database/user and a Secret containing the password;
- S3-compatible storage and an existing bucket for artifacts and Skill bundles;
- an ingress controller and TLS certificates for remote HTTP and gRPC access.

Object storage can be left disabled for an initial trial. A backend on a laptop
needs no Kubernetes deployment; cluster backends have separate charts.

For a first install reached through a local port-forward, create a namespace and
a password Secret. Write `postgres.secret.env` locally with `password=<your
existing database password>`; keep it outside version control.

```sh
kubectl create namespace threavia
kubectl -n threavia create secret generic threavia-postgres --from-env-file=postgres.secret.env
```

Create `core-values.yaml`, adjusting the database address, user, database name
and SSL mode to your PostgreSQL installation:

```yaml
postgres:
  host: postgresql.databases.svc.cluster.local
  user: threavia
  database: threavia
  sslMode: require
  existingSecret: threavia-postgres
  existingSecretPasswordKey: password
  autoMigrate: true
auth:
  mode: none
```

Choose a released version without its `v` prefix:

```sh
export THREAVIA_VERSION=0.22.0
helm upgrade --install threavia oci://ghcr.io/rclsilver/charts/threavia \
  --version "$THREAVIA_VERSION" --namespace threavia \
  --values core-values.yaml --wait
kubectl -n threavia port-forward service/threavia 8080:8080 9090:9090
```

Keep the port-forward running. Open <http://localhost:8080> and connect a local
backend to `localhost:9090`, with TLS disabled only for this local tunnel.

For a remotely reachable installation, replace `auth.mode: none` with `basic`
or `oidc`, set `http.allowedHosts`, enable the HTTP and gRPC ingress entries,
and configure their TLS and controller-specific annotations. The gRPC ingress
must support a long-lived bidirectional stream; the HTTP ingress must allow
SSE without buffering or a short idle timeout. Backends use the gRPC hostname
and TLS port, which may differ from the web hostname and port.

See the [Core chart guide](../deploy/helm/threavia/README.md) and
[values](../deploy/helm/threavia/values.yaml) for ingress, direct gRPC TLS,
Secret references and registration-key settings. Helm sources in this checkout
can also be installed by replacing the OCI reference with
`./deploy/helm/threavia`; use the matching release checkout because source
`Chart.yaml` versions are placeholders that CI stamps when packaging a release.

## Release binaries

Each release includes all three binaries for Linux and macOS, on amd64 and
arm64, with checksums. Download the archive for your platform and `SHA256SUMS`
from the [same release](https://github.com/rclsilver/threavia/releases).
For example, for Linux amd64:

```sh
export THREAVIA_VERSION=0.22.0
curl -fLO "https://github.com/rclsilver/threavia/releases/download/v${THREAVIA_VERSION}/threavia_${THREAVIA_VERSION}_linux_amd64.tar.gz"
curl -fLO "https://github.com/rclsilver/threavia/releases/download/v${THREAVIA_VERSION}/SHA256SUMS"
sha256sum --ignore-missing --check SHA256SUMS
tar -xzf "threavia_${THREAVIA_VERSION}_linux_amd64.tar.gz"
cd "threavia_${THREAVIA_VERSION}_linux_amd64"
```

Use `shasum -a 256` to compare against `SHA256SUMS` on macOS if `sha256sum` is
unavailable. The release Core binary includes the web client. To run Core
against an existing local PostgreSQL database:

```sh
export THREAVIA_POSTGRES_HOST=localhost
export THREAVIA_POSTGRES_USER=threavia
export THREAVIA_POSTGRES_DATABASE=threavia
export THREAVIA_POSTGRES_PASSWORD='your-database-password'
export THREAVIA_POSTGRES_SSLMODE=disable  # local database only
export THREAVIA_POSTGRES_AUTO_MIGRATE=true
export THREAVIA_AUTH_MODE=none
export THREAVIA_HTTP_ADDR=127.0.0.1:8080
export THREAVIA_GRPC_ADDR=127.0.0.1:9090
./threavia-core
```

Use your service manager to keep it running outside a terminal. See
[storage](#enable-artifacts-and-skills) and
[authentication](#remote-access-and-authentication) for a full installation.
Building from source instead is documented in [Development](development.md).

## Connect a backend

A backend needs access to Core's **HTTP API** for registration and **gRPC
endpoint** for its control stream, a provider CLI, and the files and tools the
agent should use. Install a release binary on that machine, or use one of the
[container, Kubernetes](#container-and-kubernetes-backends) or
[NixOS](nix.md) options.

### 1. Authenticate the provider

Run the provider's login command **as the operating-system user that will run
the backend**:

| Backend binary | Install and authenticate |
| --- | --- |
| `threavia-backend-claude` | Install [Claude Code](https://code.claude.com/docs/en/setup), then run `claude auth login`, or provide `ANTHROPIC_API_KEY`. |
| `threavia-backend-codex` | Install [Codex CLI](https://developers.openai.com/codex/cli), then run `codex login`, or provide `OPENAI_API_KEY`. See [Codex compatibility](codex.md). |

The provider login is separate from signing in to Threavia. Provider credentials
stay on the backend; Core's registration token authenticates the backend to Core.
The service must also find the provider CLI, Git and your project's required
tools in its environment.

### 2. Register with Core

In the web client, open **Settings → Backends** and issue a registration token.
Give each backend process its own token. For a local Core, export:

```sh
export THREAVIA_BACKEND_CORE_API=http://localhost:8080
export THREAVIA_BACKEND_CORE_ADDRESS=localhost:9090
export THREAVIA_BACKEND_TLS_ENABLED=false
export THREAVIA_BACKEND_REGISTRATION_TOKEN='paste-your-one-shot-token'
export THREAVIA_BACKEND_INSTANCE_NAME=laptop-claude
export THREAVIA_BACKEND_DISCOVERY_ROOTS="$HOME/git,$HOME/dev"
export THREAVIA_BACKEND_DEFAULT_WORKING_DIRECTORY="$HOME/git"
./threavia-backend-claude
```

Create the configured working directory first, or choose an existing one. For
Codex, use another token and instance name and run `./threavia-backend-codex`.
Its default state path is separate, so both providers can run on the same machine.

For a remote Core, replace the connection settings with your real endpoints:

```sh
export THREAVIA_BACKEND_CORE_API=https://threavia.example.com
export THREAVIA_BACKEND_CORE_ADDRESS=backends.threavia.example.com:443
export THREAVIA_BACKEND_TLS_ENABLED=true
```

Use `THREAVIA_BACKEND_TLS_CA_FILE` for a private certificate authority. Disabling
certificate verification is not needed for a correctly configured deployment.

The backend registers once and saves its identity and execution state locally:
`~/.threavia/backend.db` for Claude, `~/.threavia/codex/backend.db` for Codex.
It uses that identity after restarts, so the one-shot registration token is no
longer needed. Preserve this state **and** the provider's native home/session
data. Separate instances need separate state paths and identities.

Registration tokens assign the backend to your account immediately. An optional
shared registration key instead creates an unclaimed backend; claim its one-time
code in the client before use. See the Core chart settings for that alternative.

### 3. Start your first session

1. Check that the backend appears and becomes **READY** in Settings → Backends.
   A degraded backend shows its reported reason; fix provider authentication or
   missing tools on its machine.
2. Create a project and add any standing instructions. Add a known directory
   and bind it to a path on the backend, or use the backend's default directory.
   Directory discovery searches `THREAVIA_BACKEND_DISCOVERY_ROOTS`; it does not
   copy files or clone a repository automatically.
3. Create a session, select the backend and directory, review permissions, and
   send a small request such as “Explain this repository without changing files.”
4. Read the timeline and answer any question or approval request from Web or
   VS Code. You can set your preferred backend as the account default afterward.

Known directories name backend paths, not paths on the client device. Forgetting
a directory in Core does not remove its files. Keep the backend machine awake
and online while it works; closing your client is safe.

## Container and Kubernetes backends

### Docker backend

The backend images include their provider CLI and Git. Supply provider and Core
credentials at runtime and persist their home directories. This Codex example
connects to an already configured remote Core over TLS.

Create a private `.env.backend-codex` file (ignored by this repository) with:

```dotenv
THREAVIA_BACKEND_CORE_API=https://threavia.example.com
THREAVIA_BACKEND_CORE_ADDRESS=backends.threavia.example.com:443
THREAVIA_BACKEND_TLS_ENABLED=true
THREAVIA_BACKEND_REGISTRATION_TOKEN=paste-a-new-one-shot-token
THREAVIA_BACKEND_INSTANCE_NAME=container-codex
OPENAI_API_KEY=your-provider-api-key
```

Use the same release as Core:

```sh
export THREAVIA_VERSION=0.22.0
docker run -d --name threavia-codex --restart unless-stopped \
  --env-file .env.backend-codex \
  --mount type=volume,source=threavia-codex-home,target=/home/threavia \
  "ghcr.io/rclsilver/threavia-backend-codex:${THREAVIA_VERSION}"
```

The default working directory is `/home/threavia`. Put checkouts in this
persistent home or mount a workspace and configure
`THREAVIA_BACKEND_DEFAULT_WORKING_DIRECTORY` and discovery roots. The image runs
as UID 65532; mounted checkouts must be readable and writable by that user.
Install any extra build tools your project requires in a derived backend image.

For Claude, use `ghcr.io/rclsilver/threavia-backend-claude`, a separate token and
`ANTHROPIC_API_KEY`. Mount both `/home/threavia` and
`/home/threavia/.threavia` as named volumes: the Claude image declares the latter
as a volume, so explicitly naming it avoids anonymous backend-state storage.
You can use persistent CLI login data instead of an API key when supported by
your provider setup.

### Kubernetes backend

For per-Job Pods and persistent Monitors, use the
[Kubernetes execution guide](kubernetes-execution.md). It separates the trusted
control backend, executor, native provider state and shared workspaces. It does
not require nested Bubblewrap or privileged workers. The same-process layout
below remains available in the default `local` execution mode for existing
installations; it does not provide the Kubernetes executor's isolation contract.

Use the separate [Claude](../deploy/helm/threavia-backend-claude/README.md) or
[Codex](../deploy/helm/threavia-backend-codex/README.md) chart. Each uses a
StatefulSet and persists the whole home, including backend and provider state.
Provision a StorageClass or existing PVC, and keep persistence enabled.

For example, write a private `backend-codex.secret.env` containing
`registrationToken=<new Core token>` and `apiKey=<provider API key>`, then:

```sh
kubectl -n threavia create secret generic threavia-codex --from-env-file=backend-codex.secret.env
```

Create `backend-values.yaml` for your remote TLS endpoint:

```yaml
core:
  api: https://threavia.example.com
  address: backends.threavia.example.com:443
  tls:
    enabled: true
  existingSecret: threavia-codex
provider:
  existingSecret: threavia-codex
  existingSecretKey: apiKey
backend:
  defaultWorkingDirectory: /home/threavia
  discoveryRoots: ["/home/threavia"]
persistence:
  enabled: true
  size: 10Gi
```

```sh
helm upgrade --install threavia-codex oci://ghcr.io/rclsilver/charts/threavia-backend-codex \
  --version "$THREAVIA_VERSION" --namespace threavia \
  --values backend-values.yaml --wait
```

For Claude, use its chart name and a separate Secret with its own registration
token and provider API key. See each chart's values for workspace mounts,
resources and extra environment. An in-cluster plaintext Core service needs
`core.tls.enabled: false` and its internal HTTP host in `http.allowedHosts`;
remote connections should use the TLS configuration above.

## Enable artifacts and Skills

Create a bucket in your S3-compatible store and give Core credentials to access
it. Core checks the bucket at startup; it does not create one. Back up both its
objects and PostgreSQL metadata.

For a binary or container deployment, configure Core with:

```dotenv
THREAVIA_S3_ENABLED=true
THREAVIA_S3_ENDPOINT=https://s3.example.com
THREAVIA_S3_REGION=us-east-1
THREAVIA_S3_BUCKET=threavia
THREAVIA_S3_ACCESS_KEY_ID=your-access-key
THREAVIA_S3_SECRET_ACCESS_KEY=your-secret-key
THREAVIA_S3_FORCE_PATH_STYLE=true
```

For the Compose example, put these settings in a private `.env.s3` file and add
a `compose.storage.yaml` override:

```yaml
services:
  core:
    env_file:
      - ./.env.s3
    environment:
      THREAVIA_S3_ENABLED: "true"
```

With multiple Compose files, paths are relative to the first file's directory.
Save this override and `.env.s3` beside `deploy/docker/compose.yaml`, then run
from the repository root:

```sh
docker compose --env-file .env.docker -p threavia \
  -f deploy/docker/compose.yaml -f deploy/docker/compose.storage.yaml up -d
```

Include both files in later Compose commands. The explicit `environment` entry
overrides the base example's `THREAVIA_S3_ENABLED: "false"`; the other S3 settings
come from the environment file. Core must be able to reach the storage endpoint
from inside its container, where `localhost` refers to that container.

For Helm, add to `core-values.yaml`:

```yaml
s3:
  enabled: true
  endpoint: https://s3.example.com
  region: us-east-1
  bucket: threavia
  useTLS: true
  forcePathStyle: true
  existingSecret: threavia-s3
```

Create `threavia-s3` in the same namespace with keys `accessKeyId` and
`secretAccessKey`, then upgrade the release with these values. Disable path-style
addressing if your storage service requires virtual-hosted bucket names.

## Remote access and authentication

For remote installations, configure **both endpoints**:

| Endpoint | Clients | Typical public address |
| --- | --- | --- |
| HTTP / SSE | Browser and VS Code; backend registration | `https://threavia.example.com` |
| gRPC | Backend control stream | `backends.threavia.example.com:443` |

Terminate TLS at your proxy/ingress or enable gRPC TLS directly on Core. Preserve
long-lived SSE and gRPC streams. Set `THREAVIA_HTTP_ALLOWED_HOSTS` (Helm:
`http.allowedHosts`) to the web/API hostname; add any internal registration host
you actually use. A refused host returns HTTP 421.

Core supports these authentication modes:

| Mode | Core environment | Use |
| --- | --- | --- |
| `none` | `THREAVIA_AUTH_MODE=none` | Local evaluation only; every request acts as the same user. |
| `basic` | `THREAVIA_AUTH_MODE=basic`, `THREAVIA_AUTH_BASIC_USERNAME`, `THREAVIA_AUTH_BASIC_PASSWORD` | A single-account installation behind HTTPS. |
| `oidc` | `THREAVIA_AUTH_MODE=oidc`, `THREAVIA_AUTH_OIDC_ISSUER`, `THREAVIA_AUTH_OIDC_CLIENT_ID` | Sign in with your identity provider. Configure audience/user claim if required by its tokens. |

For OIDC, register a public client with Authorization Code + PKCE. Allow the web
origin and its callback `https://threavia.example.com/auth/callback`, plus
`vscode://rclsilver.threavia/auth/callback` for the extension (and
`vscode-insiders://rclsilver.threavia/auth/callback` for Insiders).
These are client redirects; Core verifies tokens rather than keeping a client
secret. See the [VS Code sign-in guide](../clients/vscode/README.md#sign-in).

For a native Core process, set `THREAVIA_HTTP_ADDR` to the interface your proxy
reaches. The Compose example already listens on container interfaces but only
publishes on host loopback. Keep it behind an authenticated HTTPS entry point
when exposing it. Store deployment credentials in private environment files or
Kubernetes Secrets, not in images or committed values files.
