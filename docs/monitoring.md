# Persistent background monitoring

Threavia monitors belong to a session and execute on its backend, outside the
Claude Code or Codex process. Finishing an agent Job does not stop them. The same
tools, state model and permission checks apply to both providers.

## Agent tools

- `monitor_start`: command, description and optional `timeoutSeconds` (default
  3600, maximum 86400). This is a backend-local tool; the backend validates the
  command against the current execution policy and asks for approval of the
  exact command, directory, permissions and duration before registration.
- `monitor_list`: this session's watches and recent results.
- `monitor_stop`: stop one watch in this session. A stop requested while its
  backend is offline is retained and delivered on reconnect.

Use these tools whenever work must continue after a final response. Native
Claude `Monitor` is refused in both native settings and the shared policy gate,
including under an ALLOW rule. Ordinary native background commands retain
their existing Job lifetime. Recurring messages still use Threavia schedules.

Each stdout change can produce a monitoring event, coalesced to at most one per
30 seconds. Stderr is retained for diagnostics; completion, failure, expiry and
interruption are also outcomes. Output is an 8 KiB tail, not a full log.
Filter the command's output to the conditions that matter, including failures.

An event queues a new Job in the same session. It never interrupts user work or
bypasses a human review. While an earlier monitoring Job is queued or running,
new observations are coalesced until that Job ends. At most 20 automatic
resumptions are allowed per watch; the watch can continue recording output
until it expires or is stopped. Waiting makes no model call; event processing
uses the configured agent and can consume tokens. Monitoring messages are
attributed to Monitor and explicitly identify their output as untrusted data,
never new user permissions.

## Execution and limits

Both Claude and Codex use the same platform-specific launcher:

- Linux (amd64/arm64): Bash, bubblewrap and working user namespaces are required.
  NixOS service toolboxes and Linux backend images include bubblewrap. Container
  runtimes must permit nested namespaces and a private `/proc` mount. The sandbox
  mounts the host filesystem read-only and isolates processes, IPC and networking.
- macOS (Intel/Apple Silicon): the launcher uses the OS-provided
  `/usr/bin/sandbox-exec` and `/bin/bash`. Seatbelt starts with a deny-by-default
  profile, permits filesystem reads, and grants writes only to a private
  temporary directory and, when allowed, the canonical Job working directory.
  Network operations, including Unix-domain sockets, are denied unless the
  effective policy permits network access. Sandboxed commands cannot signal
  the trusted supervisor outside their sandbox.

On both platforms, the backend probes the real sandbox at startup and only
advertises support when initialization succeeds. When it fails, the backend
logs `persistent monitors are unavailable` with the reason, including what the
sandbox printed. It never falls back to an unrestricted host shell unless its
operator opted in (see below). macOS `sandbox-exec` is deprecated by Apple;
removal or a profile incompatibility makes this capability unavailable
explicitly.

Network access is enabled only if the effective policy permits it. Filesystem,
network and shell refusals still apply before asking for approval. These are not
a replacement for the account's operating-system permissions. Shell rules do
not inspect every operation of an arbitrary program.

On macOS a separate supervisor owns the command's process group, a private
temporary directory and a backend lifetime pipe. A stop, timeout, backend
SIGKILL, or EOF on that pipe kills the command group; normal command completion
also cleans up its remaining group members. Commands must remain in the
foreground of that group: daemonization, `setsid` and deliberate group escape
are not supported. Seatbelt restrictions remain inherited even by detached
descendants, but unlike Linux PID namespaces, macOS process groups do not
provide a kernel guarantee of killing a deliberately detached daemon.

There are at most five active watches per session and twenty per backend.
A configured policy maximum duration also bounds each command's requested
duration. A monitor is not an additional agent Job and does not hold its
execution slot. Its explicit timeout bounds its lifetime independently of the
agent's provider execution accounting.

### Without a sandbox

Some machines cannot run the sandbox and cannot be changed to: no bubblewrap
and no root to install it, or a kernel that refuses unprivileged user
namespaces. Their operator can set `THREAVIA_BACKEND_UNSANDBOXED_MONITORS=true`
to run watches directly on the host instead. It is off by default and applies
only where the probe fails: a backend whose sandbox starts keeps using it.

The backend then logs `persistent monitors run without a sandbox` at startup,
with the reason the probe failed, and advertises `PERSISTENT_MONITORS`. A watch
runs in Bash with the account's full access for its whole duration: nothing
keeps it to the working directory, off the network or away from other
processes. The approval request carries `sandboxed: false`. The supervisor
described above for macOS owns its process group on Linux too, so a stop, the
timeout or the backend's death ends the processes it started, within the same
limits on daemonization and `setsid`.

A sandbox is what makes a read-only or offline policy hold for a watch. Without
one, `monitor_start` is refused, before any approval, when the effective policy
forbids filesystem writes or network access.

## State, outages and recovery

States are STARTING, RUNNING, STOPPING, STOPPED, COMPLETED, FAILED, EXPIRED and
INTERRUPTED. Core stores metadata, the latest bounded output, observation
sequence and wakeup delivery state; the backend durably stores unacknowledged
observations beside its state database in `<state-database>.monitors.json`, mode 0600. Each database has its own supervisor state.

A Core disconnection does not stop local watches. Reconnect replays observations
by monotonic sequence; wakeup delivery markers and their Jobs commit atomically. A backend reports
observations every five seconds. Clients distinguish confirmed activity from
an observation more than thirty seconds old or an offline backend.

A backend restart never adopts an old PID or reruns a command. Saved running
watches become interrupted. Complete inventories also reconcile registrations
whose process never reached local persistence; fresh registrations have a
thirty-second launch grace. Inventories do not interrupt watches registered
after the connection began. Backend exit terminates supervised commands as
described in the platform-specific execution limits above.

A session archive, deletion, handoff to another Run, or change to the effective
execution policy requests stopping its old watches on the next report. A
disconnected backend can only receive this request when it reconnects; its
original approval and timeout still bound local work meanwhile.

## Clients and activation

Web shows an activity badge in session lists and a background monitoring panel
above the conversation, including command, dates, bounded output and stop.
VS Code shows monitoring in session descriptions and offers Background Monitors
from the session context menu or conversation title bar; inspection and stop
use native Quick Picks and text documents.

The demo contains a fictitious deployment watch that can be stopped.

Apply migration 28 and update Core and both backends. The backend advertises
`PERSISTENT_MONITORS`; Core acknowledges support in Welcome. New agent Jobs get
the tools when both sides support them. Older backends retain temporary
background execution. This change does not automatically update or restart
already installed services.

## Testing without a local Mac

The `Monitor platforms` GitHub Actions workflow runs on real hosted macOS 15
Intel and Apple Silicon runners (`macos-15-intel` and `macos-15`). A feature-branch
push or a pull request can run it before any merge or deployment. These tests
require the real sandbox; they fail rather than skip when it is unavailable.
They cover workspace read/write boundaries, private scratch space, symlink
escapes, TCP and Unix-socket denial, permitted loopback, refusal to signal the
supervisor, individual stop, process-group cleanup and backend SIGKILL. Shared
backend, policy and provider-runner tests run under the race detector, then both
native backend binaries are built. No provider credentials or paid model calls
are required.

On Linux, portable tests exercise profile construction, environment filtering,
normal exit, cancellation and supervisor lifetime after backend SIGKILL. Both
macOS test binaries and backend binaries can also be cross-compiled with
`CGO_ENABLED=0 GOOS=darwin GOARCH=amd64` or `arm64`. Cross-compilation and a mocked
`sandbox-exec` do not validate Seatbelt enforcement. Docker on Linux cannot run
these native macOS checks. A successful native workflow is required before
claiming the macOS launcher has been runtime-validated. Full Web/VS Code visual
acceptance and a real provider session remain separate checks.

Platform references: [Apple sandbox-exec manual](https://keith.github.io/xcode-man-pages/sandbox-exec.1.html),
[Codex Seatbelt base policy](https://github.com/openai/codex/blob/main/codex-rs/sandboxing/src/seatbelt_base_policy.sbpl),
and [GitHub-hosted macOS runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
