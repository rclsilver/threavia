# Provider authentication and quota recovery

Claude and Codex backends recognize explicit authentication refusals, exhausted
quotas and request rate limits. They stop the provider attempt and its tools
before reporting `job.waiting_backend`. The Job remains unfinished, with a
persisted `backendWait` reason, readable message and earliest retry time. Web
and VS Code show that message in the conversation and its header.

| Reason | Backend state | Recovery |
| --- | --- | --- |
| `AUTHENTICATION_REQUIRED` | `DEGRADED`, provider auth `AUTHENTICATION_REQUIRED` | Run `claude auth login` or `codex login` as the backend's operating-system user, or repair its configured API credential. |
| `QUOTA_EXCEEDED` | `DEGRADED`, provider auth remains authenticated | Wait for the provider's reset time, or resolve the account's billing/limit issue. |
| `RATE_LIMITED` | `DEGRADED`, provider auth remains authenticated | Retry after the provider's explicit delay when supplied. |

Authentication is checked on connection and every minute through the installed
CLI: Claude `auth status --json`, Codex app-server `account/read` with token
refresh. These checks start no inference turn and transmit no credentials to
Core. An unsupported or unsuccessful probe cannot erase a confirmed auth
failure. For a configured Codex API key, validity is unknown until a real
request; repair the configuration and restart that backend. The native login
check is a readiness signal, and the next actual request can still be refused.

Quota reports and percentages alone never suspend a Job: model scopes and paid
overage can differ. An actual provider refusal is required. An explicit reset
or retry timestamp delays the next attempt; otherwise the delay is one minute.
A timer opens an opportunity to retry, without claiming the quota is restored.
Reconnection cannot bypass the Job's persisted earliest retry time. Generic
403s, network errors, context limits and provider overload keep their normal
failure behavior.

Core checks durable pending work every ten seconds and when a backend reports
ready. It resumes the **same Job**, using the existing native conversation when
available, its original model/effort, original instructions and later steering.
The Run's active slot remains reserved throughout the wait; subsequent Jobs
stay queued. Action counts, active duration limits and known usage survive
attempts. Waiting time does not consume the execution duration budget. Terminal
usage includes all reported attempts; missing usage remains unknown. A provider
interruption cannot guarantee an external action is safe to repeat: the recovery
prompt asks the agent to inspect existing work before continuing.

The stopped Job can be cancelled immediately, including while its backend is
offline. Connection-loss `WAITING_BACKEND` Jobs have no `backendWait` and retain
their existing reconciliation rules, because the provider may still be running.

Deploy Core (including migration `000024_provider_wait`) before updating the
backends. `Welcome.provider_recovery` negotiates this behavior: with an older
Core, updated backends report an ordinary terminal failure instead of emitting
an unsupported wait event. Older backends keep their previous behavior. No
automatic backend/model fallback or remote login challenge is introduced.
