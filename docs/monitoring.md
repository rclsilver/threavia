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

The first implementation requires a Linux backend, Bash, bubblewrap and working
user namespaces. NixOS service toolboxes and Linux backend images include
bubblewrap. Container runtimes must permit nested namespaces and a private `/proc` mount. The backend checks this at startup and advertises support only when isolation works. When the isolation cannot be provided, a command fails explicitly;
it is never launched directly on the host as a fallback.

The sandbox mounts the host filesystem read-only, isolates processes, IPC and
networking, and supplies a private temporary directory. Network access is
enabled only if the effective policy permits it. With filesystem writes
permitted, only the resolved Job working directory is writable. Filesystem,
network and shell refusals still apply before asking for approval. This is not
a replacement for the account's operating-system permissions. Shell rules do
not inspect every operation of an arbitrary program.

There are at most five active watches per session and twenty per backend.
A configured policy maximum duration also bounds each command's requested
duration. A monitor is not an additional agent Job and does not hold its
execution slot. Its explicit timeout bounds its lifetime independently of the
agent's provider execution accounting.

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
after the connection began. Backend exit kills its child sandboxes.

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
