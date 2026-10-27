# Kubernetes execution

Kubernetes mode runs every Claude or Codex Job in a separate, non-restarting
Pod. Persistent Monitors run in independent Pods, so completing the originating
Job does not stop them. Linux and macOS backends still use local execution by
default. This feature requires matching Core, backend, worker and executor
versions; it is newer than release 0.28.0.

## Components and boundaries

The control backend retains its Core identity, SQLite journal, approvals,
questions, review gates and tool routing. It connects outbound to Core as before.
The executor manages Pods and NetworkPolicies in one namespace. Only its Service
Account has those management rights; it cannot read Secrets through the API.
Worker Pods receive neither its Service Account token nor the Core credential.
The executor creates and deletes only execution attachment Secrets; they are
owned by their Pod. Pod specifications reference those Secrets instead of
exposing their values to ordinary Pod viewers.

Each administrator-defined profile fixes the image, executables, PVC mounts,
UID/GID, resources and baseline network destinations. A Job supplies its identity,
directory, capability switches and deadline, never a Pod manifest. A worker
attaches over TLS with a single-use execution credential bound to its Pod UID.
Only that execution's approved tool declarations and callbacks cross the stream.

Provider sessions and login data stay in the profile's native-state PVC. They
never travel to Core. The control backend's private home must **never** be
mounted into a worker. Workspaces, scratch and Skills use separate shared PVCs
or explicitly scoped subpaths. The current Job's assembled Skills and immutable
bundles mount read-only; scratch mounts only the current Run's directory.

Codex workers use the native `externalSandbox` turn policy: Kubernetes supplies
the process and mount boundary. Native policy hooks, review checkpoints and
Threavia approvals remain enabled. Local Codex backends retain the native
read-only sandbox. Workers need no nested Bubblewrap, privileged container,
host PID namespace or additional capabilities. Claude uses its existing native
permission integration in the isolated Pod.

Permission rules remain guardrails. A provider needs inference/authentication
network access even when ordinary agent network commands are forbidden. Job
NetworkPolicies therefore retain administrator-defined provider egress; they do
not distinguish the provider process from its tools. Monitors inherit none of
those exceptions or provider secrets. Their command helper denies network
socket creation when network access was not approved, while its trusted parent
retains the executor connection. Its procfs/ptrace boundary protects that
parent's environment and descriptors.

Core tools and configured external MCP servers remain trusted backend services,
forwarded through the execution-scoped bridge. In particular, external MCP
stdio programs run in the control backend as before, outside the provider Pod;
install only trusted backend extensions there. Their declared environment
references must not select Core/executor credentials. An executor profile is an
owner-level boundary, not a sandbox between projects sharing its PVCs and
provider account.

## Requirements

- Linux worker nodes on amd64 or arm64, with seccomp enabled. Monitors currently
  support those two Linux architectures.
- Kubernetes with enforced NetworkPolicy, DNS and a functioning container
  runtime. A CNI that silently ignores policies does not satisfy this contract.
- Shared storage accessible at the same absolute paths in the control backend
  and worker. RWO is suitable when all consumers are pinned to the same node;
  use RWX for consumers on different nodes. `ReadWriteOncePod` cannot serve this
  shared arrangement. Local-path storage's node affinity alone does not ensure
  every additional PVC is provisioned on the same node: set explicit selectors.
- Separate persistent storage for executor journal, control backend identity,
  workspace, shared Skills/scratch and native provider state. Match ownership to
  UID/GID 65532 in the official images. Profile mounts use only PVCs, not hostPath.
- Matching published images, or images built from this branch. The default
  provider versions are Codex 0.161.0 and Claude Code 2.1.311. Derived worker images
  can add project tools such as kubectl and Helm.

## Install

Use a namespace per trust boundary. The examples use `threavia`. Provision
`coding-workspaces`, `coding-shared` and `codex-native` PVCs with the storage
properties above. The backend and executor charts create their private state
PVCs. Back up those identities and the provider data; deleting them creates a
new backend or loses resumability.

Create separate Secrets for the Core registration credential, the provider API
key, and the executor profile token. Generate a random executor token with at
least 32 bytes and store it in a private file; create `codex-executor-client`
with `kubectl create secret generic ... --from-file=token=...`. The same token
Secret mounts only in the executor and its control backend. `codex-provider`
holds `apiKey`, while `codex-core` holds `registrationToken` or `token`.
Alternatively, omit the provider environment entry and authenticate the native
CLI using a trusted administrative Pod mounting only `codex-native`, with
`HOME=/home/agent`. Do not mount the control backend home to perform login.

Copy [executor-values.yaml](../deploy/kubernetes/executor-values.yaml), replace
`RELEASE_VERSION`, and choose node selectors and provider egress for your cluster.
Copy [backend-values.yaml](../deploy/kubernetes/backend-values.yaml) and set your
Core endpoints. Install the executor first, then the control backend:

```sh
helm upgrade --install coding-executor oci://ghcr.io/rclsilver/charts/threavia-executor \
  --version "$THREAVIA_VERSION" -n threavia -f executor-values.yaml --wait
helm upgrade --install coding-codex oci://ghcr.io/rclsilver/charts/threavia-backend-codex \
  --version "$THREAVIA_VERSION" -n threavia -f backend-values.yaml --wait
```

For an unreleased checkout, use the corresponding `deploy/helm/` directories and
set controller/backend/worker images to your locally built version. The executor
uses an internal TLS service on port 8443. Its chart generates separate server
key and public CA Secrets, retaining them on upgrade. For managed certificates,
provide `tls.existingSecret` and a separate `tls.caSecret` containing only
`ca.crt`; never point worker trust mounts at the private-key Secret. Generated
server certificates last one year. Renew or rotate certificates in a maintenance
window with workers stopped and update all clients' trust roots together.

To add Claude, declare another profile with a distinct token and native-state
PVC, `provider: claude`, the Claude backend worker executable/image and
`providerBinary: claude`. Use `ANTHROPIC_API_KEY` when using API-key auth and
install its own control backend chart. Do not reuse Codex's identity or token.

Non-Helm control backends select this runtime using
`THREAVIA_BACKEND_EXECUTION_MODE=kubernetes`, `EXECUTOR_ENDPOINT`,
`EXECUTOR_CA_FILE` and `EXECUTOR_TOKEN_FILE` under the same
`THREAVIA_BACKEND_` prefix. Workspace, Skills and scratch paths must refer to
the shared mounts; an ordinary laptop directory is not automatically a PVC.

## Lifetime and recovery

A backend incarnation renews a 30-second executor lease. Each worker also needs
a fresh absolute-expiry heartbeat; queued traffic cannot extend an orphan's
lifetime. A broken stream stops its worker. Core disconnection alone leaves
the backend and its workers running, with the existing durable event replay.

Completion and cancellation become visible only after the container confirms
termination, covering detached descendants. An API timeout, lost node or missing
admitted Pod is not proof of exit: the Job remains reserved and reports an
unconfirmed stop. Restore connectivity or verify and fence the node before
administrative recovery; do not force-delete Pods to claim that commands stopped.
The executor requests graceful runtime termination and retains its own Pod
finalizer until the worker's container status confirms exit. A `Failed` or
`NodeLost` phase alone does not release that fence. Do not remove the finalizer
to bypass an unreachable node; first establish that its work physically stopped.
Before uninstalling, stop Jobs and Monitors and let the executor finish their
cleanup. Preserve its journal PVC for a reinstall. Removing the controller while
work is active leaves guarded Pods for reconciliation on its next boot.

A new control backend incarnation fences the previous incarnation. An executor
restart reconciles its journal and interrupts owned executions before readiness.
Neither event replays commands. Policy changes stop the current container and
resume the native session in a new Pod with the new mounts/network permissions,
preserving consumed action/time budgets. A missing native binding fails visibly
instead of replaying an unknown attempt. Monitors recover as interrupted, never
by re-executing their stored command. Terminal executor records are retained for
30 days; failed resource cleanup is retried.

Monitors show `STARTING` while scheduling, then `RUNNING` or `STOPPING`, including
while their Session is idle. Output, stop, expiry and notification behavior use
the same Core/UI contract as Linux and macOS local Monitors.

## Test without a Mac or provider quota

From a Linux machine with Docker and Nix:

```sh
THREAVIA_TEST_DIR="$PWD/.test-artifacts/kubernetes" \
  nix develop --no-write-lock-file -c bash deploy/kubernetes/test/run.sh
```

The script creates a disposable K3s/k3d cluster with NetworkPolicy enforcement,
uses its own explicit kubeconfig, and removes the cluster on exit. It does not
use your current Kubernetes context. The fixture image contains the real
Threavia runners with deterministic CLI substitutes: no provider authentication
or billable inference is needed. Set `THREAVIA_TEST_NATIVE=1` to additionally
exercise the real Codex command protocol and its external sandbox boundary,
still without inference. Artifacts contain a test kubeconfig and random profile
tokens; keep that directory private and remove it after inspection.

The Kubernetes CI workflow runs those tests. The existing macOS workflow checks
Seatbelt and both local provider integrations on Intel and Apple Silicon; access
to a personal Mac is unnecessary. A successful fixture or native command test
does not establish the validity of a provider account or a live inference call.
