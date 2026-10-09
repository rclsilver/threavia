/**
 * The fictitious world the demo runs on.
 *
 * Nothing here comes from a server or goes to one. It is built once, relative
 * to the moment the page opened, so "Today" and "3 min ago" read true whenever
 * the demo is shown. The people, machines and clusters are made up.
 */

import { upsChartSvg, upsPreviewHtml } from './published';

/** A fixed identifier, readable in a URL and stable between visits. */
const id = (prefix: string, n: number) => `${prefix}-0000-4000-8000-${String(n).padStart(12, '0')}`;

export const IDS = {
  homelab: id('d0000001', 1),
  website: id('d0000001', 2),
  laptop: id('d0000002', 1),
  nas: id('d0000002', 2),
  ci: id('d0000002', 3),
  ingress: id('d0000003', 1),
  certManager: id('d0000003', 2),
  backups: id('d0000003', 3),
  grafana: id('d0000003', 4),
  blog: id('d0000003', 5),
  oldSession: id('d0000003', 6),
};

const now = Date.now();
export const at = (minutesAgo: number) => new Date(now - minutesAgo * 60_000).toISOString();

/** The next times a daily cron at hour:minute fires, local time, as a schedule would list them. */
function nextDaily(hour: number, minute: number, count: number, weekday?: number): string[] {
  const out: string[] = [];
  const next = new Date(now);
  next.setSeconds(0, 0);
  next.setHours(hour, minute);
  while (out.length < count) {
    if (next.getTime() > now && (weekday === undefined || next.getDay() === weekday)) out.push(next.toISOString());
    next.setDate(next.getDate() + 1);
  }
  return out;
}

export const me = { userId: 'alex', authMode: 'oidc', subject: 'alex', name: 'Alex Martin', email: 'alex@example.org' };

export const projects = [
  {
    id: IDS.homelab,
    ownerId: 'alex',
    name: 'homelab',
    description: 'Kubernetes, Puppet and the machines under the stairs',
    instructions: `## Testing

- Run \`helm lint\` and the chart tests before packaging anything.
- A change to an ingress is checked from outside the cluster with \`curl\`, not only with \`kubectl get\`.

## Shipping

- Never push to \`main\` without a review.
- Chart versions are pinned; an upgrade is its own commit.

## The cluster

- \`node-3\` is the only node with the GPU. Nothing else is scheduled there.
- Backups go to the NAS (\`nas.home\`), restore-tested every month.`,
    status: 'ACTIVE',
    createdAt: at(60 * 24 * 40),
    updatedAt: at(60 * 3),
  },
  {
    id: IDS.website,
    ownerId: 'alex',
    name: 'website',
    description: 'The personal site and its blog',
    instructions: '',
    status: 'ACTIVE',
    createdAt: at(60 * 24 * 20),
    updatedAt: at(60 * 26),
  },
];

export const backends = [
  {
    id: IDS.laptop,
    ownerId: 'alex',
    name: 'laptop',
    ownershipStatus: 'CLAIMED',
    operationalStatus: 'READY',
    providerAuthState: 'AUTHENTICATED',
    capabilities: ['CODE'],
    features: ['JOB_INPUT_NEXT', 'JOB_INPUT_NOW'],
    capacity: { maxConcurrentRuns: 2, activeRuns: 1 },
    protocolVersion: 1,
    lastHeartbeatAt: at(0.2),
    createdAt: at(60 * 24 * 40),
    updatedAt: at(0.2),
  },
  {
    id: IDS.nas,
    ownerId: 'alex',
    name: 'nas',
    ownershipStatus: 'CLAIMED',
    operationalStatus: 'READY',
    providerAuthState: 'AUTHENTICATED',
    capabilities: ['CODE'],
    features: ['JOB_INPUT_NEXT', 'JOB_INPUT_NOW'],
    capacity: { maxConcurrentRuns: 1, activeRuns: 0 },
    protocolVersion: 1,
    lastHeartbeatAt: at(0.3),
    createdAt: at(60 * 24 * 30),
    updatedAt: at(0.3),
  },
  {
    id: IDS.ci,
    ownerId: 'alex',
    name: 'ci-runner',
    ownershipStatus: 'CLAIMED',
    operationalStatus: 'DEGRADED',
    providerAuthState: 'AUTHENTICATION_REQUIRED',
    capabilities: ['CODE'],
    features: [],
    capacity: { maxConcurrentRuns: 1, activeRuns: 0 },
    protocolVersion: 1,
    lastHeartbeatAt: at(2),
    conditions: [
      {
        type: 'ProviderAuthenticated',
        status: 'False',
        reason: 'LoginRequired',
        message: 'The provider session expired. Run `claude /login` on ci-runner.',
      },
    ],
    createdAt: at(60 * 24 * 12),
    updatedAt: at(2),
  },
];

const session = (
  sessionId: string,
  projectId: string,
  title: string,
  createdMinutesAgo: number,
  updatedMinutesAgo: number,
  extra: Record<string, unknown> = {},
) => ({
  id: sessionId,
  projectId,
  title,
  status: 'ACTIVE',
  createdAt: at(createdMinutesAgo),
  updatedAt: at(updatedMinutesAgo),
  ...extra,
});

export const sessions = [
  session(IDS.certManager, IDS.homelab, 'Upgrade cert-manager to 1.16', 14, 1, { activeJobStatus: 'WAITING_VALIDATION' }),
  session(IDS.ingress, IDS.homelab, 'Fix the flaky deploy of the ingress controller', 70, 22),
  session(IDS.backups, IDS.homelab, 'Check last night’s backups', 60 * 9, 30, { activeJobStatus: 'WAITING_INPUT' }),
  session(IDS.grafana, IDS.homelab, 'Grafana dashboard for the UPS', 60 * 30, 60 * 28),
  session(IDS.oldSession, IDS.homelab, 'Move Puppet to the new CA', 60 * 24 * 12, 60 * 24 * 11, {
    status: 'ARCHIVED',
    archivedAt: at(60 * 24 * 11),
  }),
  session(IDS.blog, IDS.website, 'Write the post about the homelab rebuild', 60 * 27, 60 * 26),
];

export const directories = [
  { id: id('d0000004', 1), projectId: IDS.homelab, name: 'homelab', createdAt: at(60 * 24 * 40), updatedAt: at(60 * 24 * 40) },
  { id: id('d0000004', 2), projectId: IDS.website, name: 'site', createdAt: at(60 * 24 * 20), updatedAt: at(60 * 24 * 20) },
];

// ----------------------------------------------------------------- timelines

/** Builds a session's events, numbering them as Core would. */
class Timeline {
  events: Record<string, unknown>[] = [];
  jobs: Record<string, unknown>[] = [];
  private calls = 0;
  private sessionId: string;
  private projectId: string;
  private runId: string;
  private sequence: number;

  constructor(sessionId: string, projectId: string, runId: string, sequence: number) {
    this.sessionId = sessionId;
    this.projectId = projectId;
    this.runId = runId;
    this.sequence = sequence;
  }

  push(type: string, minutesAgo: number, payload: Record<string, unknown> = {}, jobId?: string) {
    this.events.push({
      id: `${this.sessionId.slice(0, 8)}-${this.sequence}`,
      sequence: this.sequence++,
      timestamp: at(minutesAgo),
      type,
      projectId: this.projectId,
      sessionId: this.sessionId,
      runId: this.runId,
      jobId,
      payload,
    });
  }

  /** A tool call, started and ended. */
  tool(jobId: string, minutesAgo: number, name: string, input: Record<string, unknown>, output: string, failed = false) {
    const toolCallId = `toolu_demo_${this.sessionId.slice(0, 6)}_${this.calls++}`;
    this.push('tool.started', minutesAgo, { toolCallId, name, input }, jobId);
    this.push(
      failed ? 'tool.failed' : 'tool.completed',
      minutesAgo - 0.05,
      failed ? { toolCallId, name, error: output } : { toolCallId, name, output: { output } },
      jobId,
    );
  }

  job(jobId: string, status: string, startedMinutesAgo: number, endedMinutesAgo?: number) {
    this.jobs.push({
      id: jobId,
      runId: this.runId,
      status,
      originChannel: 'web',
      createdAt: at(startedMinutesAgo),
      updatedAt: at(endedMinutesAgo ?? 0),
      startedAt: at(startedMinutesAgo),
      endedAt: endedMinutesAgo === undefined ? undefined : at(endedMinutesAgo),
    });
  }
}

export interface SessionState {
  runs: Record<string, unknown>[];
  jobs: Record<string, unknown>[];
  events: Record<string, unknown>[];
}

const run = (sessionId: string, n: number, backend: string, minutesAgo: number) => ({
  id: id('d0000005', n),
  sessionId,
  backendInstanceId: backend,
  nativeSessionId: `native-${n}`,
  resumeStatus: 'AVAILABLE',
  createdAt: at(minutesAgo),
  updatedAt: at(minutesAgo),
});

/** The long, finished investigation: steps that fold, a diff, an approval. */
function ingress(): SessionState {
  const r = run(IDS.ingress, 1, IDS.laptop, 70);
  const t = new Timeline(IDS.ingress, IDS.homelab, r.id, 1000);
  const j1 = id('d0000006', 1);
  const j2 = id('d0000006', 2);
  t.push('session.created', 70, { title: 'Fix the flaky deploy of the ingress controller' });
  t.push('job.created', 70, { runId: r.id }, j1);
  t.push('user.message', 70, { text: 'The ingress controller keeps failing on one node since last week. Find out why and fix it.' }, j1);
  t.push('job.started', 69.9, {}, j1);
  t.tool(j1, 69.5, 'Bash', { command: 'kubectl -n ingress-nginx get pods -o wide', description: 'List the controller pods' },
    'NAME                                        READY   STATUS             RESTARTS      NODE\ningress-nginx-controller-7d9f8c6b5-4kq2x   1/1     Running            0             node-1\ningress-nginx-controller-7d9f8c6b5-zt8pn   0/1     CrashLoopBackOff   14 (2m ago)   node-3');
  t.tool(j1, 69, 'Bash', { command: 'kubectl -n ingress-nginx logs ingress-nginx-controller-7d9f8c6b5-zt8pn --previous | tail -5', description: 'Read the crashed pod logs' },
    'E1008 09:12:01.902 main.go:129] could not bind to 0.0.0.0:443: address already in use\nF1008 09:12:01.903 main.go:131] exit status 1');
  t.push('agent.message', 68.6, { text: 'The second replica crashes on node-3: something already listens on port 443 there.' }, j1);
  t.tool(j1, 68, 'Grep', { pattern: 'hostPort|hostNetwork', path: 'kubernetes' }, 'kubernetes/ingress-nginx/values.yaml:14:  hostNetwork: true\nkubernetes/traefik/values.yaml:22:    hostPort: 443');
  t.tool(j1, 67.5, 'Read', { file_path: 'kubernetes/traefik/values.yaml' }, '1\tports:\n2\t  websecure:\n3\t    port: 8443\n…\n22\t    hostPort: 443');
  t.tool(j1, 67, 'Bash', { command: 'kubectl get pods -A -o wide --field-selector spec.nodeName=node-3 | grep -E "traefik|ingress"' },
    'ingress-nginx   ingress-nginx-controller-7d9f8c6b5-zt8pn   0/1   CrashLoopBackOff   node-3\ntraefik         traefik-6c4d8b7f9-qm2lx                    1/1   Running            node-3');
  t.tool(j1, 66.5, 'Bash', { command: 'kubectl get svc -A | grep -i loadbalancer' }, 'ingress-nginx   ingress-nginx-controller   LoadBalancer   10.43.12.7   192.168.1.240   80:31080/TCP,443:31443/TCP');
  t.push('agent.message', 66.2, { text: 'Traefik and the ingress controller both want host port 443 on node-3. The controller is already behind MetalLB, so it does not need the host network at all.' }, j1);
  t.tool(j1, 65.8, 'Edit', {
    file_path: 'kubernetes/ingress-nginx/values.yaml',
    old_string: '  replicaCount: 2\n  hostNetwork: true\n  dnsPolicy: ClusterFirstWithHostNet\n',
    new_string: '  replicaCount: 2\n  affinity:\n    podAntiAffinity:\n      preferredDuringSchedulingIgnoredDuringExecution:\n        - weight: 100\n          podAffinityTerm:\n            topologyKey: kubernetes.io/hostname\n',
  }, 'The file kubernetes/ingress-nginx/values.yaml has been updated.');
  t.tool(j1, 65.4, 'Bash', { command: 'helm template ingress-nginx ingress-nginx/ingress-nginx -f kubernetes/ingress-nginx/values.yaml | kubectl apply --dry-run=server -f -' },
    'Error: resource mapping not found for name: "ingress-nginx-admission" kind "ValidatingWebhookConfiguration"', true);
  t.tool(j1, 65, 'Bash', { command: 'helm template ingress-nginx ingress-nginx/ingress-nginx --version 4.11.3 -f kubernetes/ingress-nginx/values.yaml | kubectl apply --dry-run=server -f -' },
    'deployment.apps/ingress-nginx-controller configured (server dry run)');
  const upgrade = 'helm -n ingress-nginx upgrade ingress-nginx ingress-nginx/ingress-nginx --version 4.11.3 -f kubernetes/ingress-nginx/values.yaml';
  t.push('validation.resolved', 63.5, { validationId: id('d0000007', 1), approved: true, note: '', title: `Bash: ${upgrade}` }, j1);
  t.tool(j1, 63.4, 'Bash', { command: upgrade }, 'Release "ingress-nginx" has been upgraded. Happy Helming!\nREVISION: 18');
  t.tool(j1, 62.5, 'Bash', { command: 'kubectl -n ingress-nginx rollout status deploy/ingress-nginx-controller --timeout=180s' }, 'deployment "ingress-nginx-controller" successfully rolled out');
  t.tool(j1, 62, 'Bash', { command: 'curl -sk -o /dev/null -w "%{http_code}" https://grafana.home.example' }, '200');
  t.push('workspace.changed', 61.8, {
    knownDirectoryId: directories[0].id,
    files: [{ path: 'kubernetes/ingress-nginx/values.yaml', state: 'MODIFIED' }],
    additions: 7,
    deletions: 2,
    directory: '/home/alex/homelab',
    baseTree: 'demo-base',
    headTree: 'demo-head',
  }, j1);
  t.push('agent.message', 61.7, {
    text: 'Fixed. The second replica crashed because **traefik already held host port 443 on node-3**, and the ingress controller ran with `hostNetwork: true`.\n\n- Removed `hostNetwork`: the controller is already exposed through MetalLB on `192.168.1.240`.\n- Added a soft anti-affinity so the two replicas land on different nodes.\n- Rolled out revision 18; both replicas run, and the sites behind the ingress answer 200.\n\nNot committed yet — say the word and I will.',
  }, j1);
  t.push('job.completed', 61.7, { summary: 'Fixed', usage: { costUsd: 1.84, inputTokens: 4120, outputTokens: 6230, cacheReadTokens: 812000, cacheWriteTokens: 64000 } }, j1);
  t.job(j1, 'COMPLETED', 70, 61.7);

  t.push('job.created', 23, { runId: r.id }, j2);
  t.push('user.message', 23, { text: 'Great. Commit it and push.' }, j2);
  t.push('job.started', 22.9, {}, j2);
  t.tool(j2, 22.8, 'Bash', { command: 'git commit -am "fix(ingress-nginx): stop binding the host network"' }, '[main 7c21e9a] fix(ingress-nginx): stop binding the host network\n 1 file changed, 7 insertions(+), 2 deletions(-)');
  t.push('validation.resolved', 22.5, { validationId: id('d0000007', 2), approved: true, note: '', title: 'Bash: git push origin main' }, j2);
  t.tool(j2, 22.4, 'Bash', { command: 'git push origin main' }, 'To git.home.example:alex/homelab.git\n   93be1f0..7c21e9a  main -> main');
  t.push('agent.message', 22.2, { text: 'Committed as `7c21e9a` and pushed to `main`.' }, j2);
  t.push('job.completed', 22.2, { summary: 'Pushed', usage: { costUsd: 0.09, inputTokens: 12, outputTokens: 40, cacheReadTokens: 90000, cacheWriteTokens: 800 } }, j2);
  t.job(j2, 'COMPLETED', 23, 22.2);
  return { runs: [r], jobs: t.jobs, events: t.events };
}

/** Running now, and stopped on a permission it needs. */
function certManager(): SessionState {
  const r = run(IDS.certManager, 2, IDS.laptop, 14);
  const t = new Timeline(IDS.certManager, IDS.homelab, r.id, 2000);
  const j = id('d0000006', 3);
  t.push('session.created', 14, { title: 'Upgrade cert-manager to 1.16' });
  t.push('job.created', 14, { runId: r.id }, j);
  t.push('user.message', 14, { text: 'Upgrade cert-manager to 1.16. Read the upgrade notes first, and keep the ClusterIssuers working.' }, j);
  t.push('job.started', 13.9, {}, j);
  t.tool(j, 13, 'WebFetch', { url: 'https://cert-manager.io/docs/releases/upgrading/upgrading-1.15-1.16/' }, 'Upgrading from v1.15 to v1.16 … no breaking change to the CRDs; the `--enable-certificate-owner-ref` default changes.');
  t.tool(j, 12, 'Read', { file_path: 'kubernetes/cert-manager/kustomization.yaml' }, '1\thelmCharts:\n2\t  - name: cert-manager\n3\t    version: v1.15.3\n4\t    valuesFile: values.yaml');
  t.tool(j, 11, 'Edit', { file_path: 'kubernetes/cert-manager/kustomization.yaml', old_string: '    version: v1.15.3', new_string: '    version: v1.16.1' }, 'The file kubernetes/cert-manager/kustomization.yaml has been updated.');
  t.tool(j, 9, 'Bash', { command: 'kubectl get clusterissuers' }, 'NAME               READY   AGE\nletsencrypt-prod   True    212d\nletsencrypt-dns    True    98d');
  t.push('agent.message', 8, { text: 'The notes list no breaking change for us. The chart is bumped in the kustomization; I need to apply it to the cluster.' }, j);
  t.job(j, 'WAITING_VALIDATION', 14);
  return { runs: [r], jobs: t.jobs, events: t.events };
}

/** Waiting on a question only the person can answer. */
function backups(): SessionState {
  const r = run(IDS.backups, 3, IDS.nas, 60 * 9);
  const t = new Timeline(IDS.backups, IDS.homelab, r.id, 3000);
  const j1 = id('d0000006', 4);
  const j2 = id('d0000006', 5);
  t.push('session.created', 60 * 9, { title: 'Check last night’s backups' });
  t.push('job.created', 60 * 9, { runId: r.id }, j1);
  t.push('user.message', 60 * 9, { text: 'Every morning: check that last night’s backups ran, and tell me only if something is wrong.' }, j1);
  t.push('job.started', 60 * 9 - 0.1, {}, j1);
  t.tool(j1, 60 * 9 - 1, 'Bash', { command: 'restic -r /mnt/backup snapshots --latest 3' }, 'ID        Time                 Host   Paths\na91f2c3e  2026-10-07 02:00:12  nas    /srv\n0c7d1b44  2026-10-08 02:00:09  nas    /srv');
  t.push('agent.message', 60 * 9 - 2, { text: 'Last night’s snapshot is there and complete. I will check again tomorrow at 07:30.' }, j1);
  t.push('job.completed', 60 * 9 - 2, { summary: 'OK', usage: { costUsd: 0.04, inputTokens: 10, outputTokens: 30, cacheReadTokens: 40000, cacheWriteTokens: 500 } }, j1);
  t.job(j1, 'COMPLETED', 60 * 9, 60 * 9 - 2);

  t.push('job.created', 31, { runId: r.id }, j2);
  t.push('user.message', 31, { text: 'Check last night’s backups.', scheduleId: id('d0000008', 1) }, j2);
  t.push('job.started', 30.9, {}, j2);
  t.tool(j2, 30.5, 'Bash', { command: 'restic -r /mnt/backup snapshots --latest 2' }, 'ID        Time                 Host   Paths\n0c7d1b44  2026-10-08 02:00:09  nas    /srv');
  t.tool(j2, 30.2, 'Bash', { command: 'journalctl -u restic-backup --since yesterday | tail -3' }, 'restic-backup.service: Failed with result \'exit-code\'.\nFatal: unable to open repository: disk /mnt/backup is full (97%)');
  t.push('agent.message', 30, { text: 'Last night’s backup **did not run**: the backup disk is 97% full. I can prune old snapshots, but that deletes data — how far back should I keep?' }, j2);
  t.job(j2, 'WAITING_INPUT', 31);
  return { runs: [r], jobs: t.jobs, events: t.events };
}

function quiet(sessionId: string, n: number, backend: string, minutesAgo: number, ask: string, answer: string, projectId = IDS.homelab): SessionState {
  const r = run(sessionId, n, backend, minutesAgo);
  const t = new Timeline(sessionId, projectId, r.id, 4000 + n * 100);
  const j = id('d0000006', 10 + n);
  t.push('session.created', minutesAgo, {});
  t.push('job.created', minutesAgo, { runId: r.id }, j);
  t.push('user.message', minutesAgo, { text: ask }, j);
  t.push('job.started', minutesAgo - 0.1, {}, j);
  t.tool(j, minutesAgo - 1, 'Read', { file_path: 'README.md' }, '1\t# ' + ask.slice(0, 30));
  t.push('agent.message', minutesAgo - 2, { text: answer }, j);
  t.push('job.completed', minutesAgo - 2, { summary: 'Done', usage: { costUsd: 0.31, inputTokens: 300, outputTokens: 900, cacheReadTokens: 120000, cacheWriteTokens: 4000 } }, j);
  t.job(j, 'COMPLETED', minutesAgo, minutesAgo - 2);
  return { runs: [r], jobs: t.jobs, events: t.events };
}

/** A finished job that published what it made: a page and a chart. */
function grafana(): SessionState {
  const r = run(IDS.grafana, 4, IDS.laptop, 60 * 30);
  const t = new Timeline(IDS.grafana, IDS.homelab, r.id, 4400);
  const j = id('d0000006', 14);
  const minutes = 60 * 30;
  t.push('session.created', minutes, {});
  t.push('job.created', minutes, { runId: r.id }, j);
  t.push('user.message', minutes, { text: 'Make a Grafana dashboard for the UPS: load, battery and runtime left. Show me what it will look like before I import it.' }, j);
  t.push('job.started', minutes - 0.1, {}, j);
  t.tool(j, minutes - 1, 'Bash', { command: 'upsc ups@nas | grep -E "load|charge|runtime"' }, 'battery.charge: 100\nbattery.runtime: 2460\nups.load: 53');
  t.tool(j, minutes - 3, 'Write', { file_path: 'grafana/ups.json', content: '{ "title": "UPS", … }' }, 'File created successfully at: grafana/ups.json');
  t.tool(j, minutes - 5, 'Write', { file_path: 'preview/ups-dashboard-preview.html', content: '<!doctype html>…' }, 'File created successfully at: preview/ups-dashboard-preview.html');
  t.tool(j, minutes - 6, 'mcp__threavia__artifact_publish', { path: 'preview/ups-dashboard-preview.html', title: 'UPS dashboard — preview' }, '{"artifactId":"…","published":"It is in the conversation now."}');
  t.push('artifact.created', minutes - 6, { artifactId: artifacts[3].id, filename: artifacts[3].filename, mimeType: artifacts[3].mimeType, size: artifacts[3].size, title: 'UPS dashboard — preview' }, j);
  t.tool(j, minutes - 7, 'mcp__threavia__artifact_publish', { path: 'preview/ups-load-24h.svg', title: 'UPS load, last 24 hours' }, '{"artifactId":"…","published":"It is in the conversation now."}');
  t.push('artifact.created', minutes - 7, { artifactId: artifacts[4].id, filename: artifacts[4].filename, mimeType: artifacts[4].mimeType, size: artifacts[4].size, title: 'UPS load, last 24 hours' }, j);
  t.push('agent.message', minutes - 8, {
    text: 'The dashboard is in `grafana/ups.json`, provisioned from the ConfigMap: load, battery charge and runtime left, with an alert under 10 minutes of runtime. The preview above is rendered from last week’s data — try the 24 hours / 7 days toggle — and the chart is the load over the last day.',
  }, j);
  t.push('job.completed', minutes - 8, { summary: 'Done', usage: { costUsd: 0.47, inputTokens: 600, outputTokens: 2100, cacheReadTokens: 140000, cacheWriteTokens: 6000 } }, j);
  t.job(j, 'COMPLETED', minutes, minutes - 8);
  return { runs: [r], jobs: t.jobs, events: t.events };
}

export function timelines(): Record<string, SessionState> {
  return {
    [IDS.ingress]: ingress(),
    [IDS.certManager]: certManager(),
    [IDS.backups]: backups(),
    [IDS.grafana]: grafana(),
    [IDS.oldSession]: quiet(IDS.oldSession, 5, IDS.laptop, 60 * 24 * 12, 'Move Puppet to the new CA.', 'Every agent now trusts the new CA and the old one is revoked.'),
    [IDS.blog]: quiet(IDS.blog, 6, IDS.laptop, 60 * 27, 'Draft a post about rebuilding the homelab: why, what changed, what I would do again.', 'The draft is in `content/posts/homelab-rebuild.md`: the why, the three changes that mattered, and what I would do again.', IDS.website),
  };
}

// --------------------------------------------------------------- attention

const certUpgrade = 'kubectl apply -k kubernetes/cert-manager';

export const validations = [
  {
    id: id('d0000007', 10),
    scope: { projectId: IDS.homelab, sessionId: IDS.certManager, runId: id('d0000005', 2), jobId: id('d0000006', 3) },
    status: 'PENDING',
    title: `Bash: ${certUpgrade}`,
    summary: 'The agent wants to use Bash.',
    requestPayload: { tool: 'Bash', input: { command: certUpgrade, description: 'Apply the cert-manager upgrade to the cluster' } },
    payloadSha256: 'a3f1c9d2e8b7a6f5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1',
    originChannel: 'web',
    notify: false,
    context: { sessionTitle: 'Upgrade cert-manager to 1.16', projectName: 'homelab', backendName: 'laptop', directory: 'homelab' },
    createdAt: at(1),
  },
];

export const userInputs = [
  {
    id: id('d0000009', 1),
    scope: { projectId: IDS.homelab, sessionId: IDS.backups, runId: id('d0000005', 3), jobId: id('d0000006', 5) },
    status: 'PENDING',
    prompt: 'The backup disk is 97% full. How many daily snapshots should I keep when pruning?',
    choices: ['Keep 14 days', 'Keep 30 days', 'Do not prune, I will add a disk'],
    freeText: true,
    originChannel: 'schedule',
    notify: true,
    context: { sessionTitle: 'Check last night’s backups', projectName: 'homelab', backendName: 'nas', directory: 'homelab' },
    createdAt: at(30),
  },
];

// ------------------------------------------------------------------- policy

export const projectPolicies: Record<string, Record<string, unknown>> = {
  [IDS.homelab]: {
    mode: 'GUARDED',
    allowFilesystemWrite: true,
    allowGitCommit: true,
    allowGitPush: false,
    allowNetwork: true,
    maxDurationSeconds: 3600,
    rules: [
      { effect: 'DENY', capability: 'SHELL', match: 'kubectl delete namespace *', note: 'Never, on the shared cluster' },
      { effect: 'ASK', capability: 'SHELL', match: 'kubectl apply *' },
      { effect: 'ASK', capability: 'SHELL', match: 'helm upgrade *' },
      { effect: 'DENY', capability: 'FILE_READ', match: 'secrets/**' },
    ],
  },
  [IDS.website]: { mode: 'INTERACTIVE', allowFilesystemWrite: true, allowGitCommit: true, allowGitPush: false, allowNetwork: true },
};

// -------------------------------------------------------------- knowledge

const task = (n: number, projectId: string, title: string, description: string, status: string, minutesAgo: number, dependsOn: number[] = []) => ({
  id: id('d000000a', n),
  projectId,
  title,
  description,
  status,
  dependsOn: dependsOn.map((other) => id('d000000a', other)),
  createdAt: at(minutesAgo),
  updatedAt: at(minutesAgo / 2),
  completedAt: status === 'DONE' ? at(minutesAgo / 2) : undefined,
});

export const tasks = [
  task(1, IDS.homelab, 'Upgrade cert-manager to 1.16', 'Read the upgrade notes; keep both ClusterIssuers working.', 'IN_PROGRESS', 60 * 5),
  task(2, IDS.homelab, 'Pin every Helm chart version in the kustomizations', 'A floating version broke the ingress rollout once.', 'TODO', 60 * 26),
  task(3, IDS.homelab, 'Upgrade the cluster to Kubernetes 1.32', 'Read the deprecations first; MetalLB needs a bump too.', 'TODO', 60 * 24 * 3, [2, 1]),
  task(4, IDS.homelab, 'Add a second disk to the NAS', 'The backup disk is 97% full.', 'TODO', 30),
  task(5, IDS.homelab, 'Prune the restic repository', '', 'TODO', 29, [4]),
  task(6, IDS.homelab, 'Move ingress-nginx off the host network', 'Traefik holds host port 443 on node-3.', 'DONE', 70),
  task(7, IDS.homelab, 'Rotate the Grafana admin password', '', 'DONE', 60 * 24 * 4),
  task(8, IDS.website, 'Publish the homelab rebuild post', 'Draft in content/posts/homelab-rebuild.md.', 'TODO', 60 * 26),
];

const decision = (n: number, projectId: string, title: string, content: string, importance: string, minutesAgo: number) => ({
  id: id('d000000b', n),
  projectId,
  title,
  content,
  importance,
  status: 'ACTIVE',
  createdAt: at(minutesAgo),
  updatedAt: at(minutesAgo),
});

export const decisions = [
  decision(1, IDS.homelab, 'Ingress goes through MetalLB, never the host network', 'Two ingress controllers cannot both hold host port 443 on one node. Every ingress is a LoadBalancer service on the 192.168.1.240–250 pool.', 'IMPORTANT', 61),
  decision(2, IDS.homelab, 'Helm charts are pinned to an exact version', 'A floating chart version broke the ingress rollout once. Upgrades are a commit, never a side effect of a sync.', 'IMPORTANT', 60 * 24 * 2),
  decision(3, IDS.homelab, 'Backups go to the NAS, restore-tested monthly', '', 'NORMAL', 60 * 24 * 9),
  decision(4, IDS.homelab, 'Grafana stays behind the ingress, no NodePort', 'Exposing it on a NodePort skipped the auth proxy.', 'NORMAL', 60 * 24 * 6),
  decision(5, IDS.website, 'Posts are written in English', '', 'NORMAL', 60 * 24 * 15),
];

export const artifacts = [
  { id: id('d000000c', 1), ownerId: 'alex', projectId: IDS.homelab, filename: 'ingress-incident-report.md', mimeType: 'text/markdown', size: 2481, sha256: '5e2b7c9a1f0d3e4b6a8c2d1f9e7b5a3c1d0e8f6a4b2c9d7e5f3a1b0c8d6e4f2a', sessionId: IDS.ingress, createdAt: at(61) },
  { id: id('d000000c', 2), ownerId: 'alex', projectId: IDS.homelab, filename: 'ups-dashboard.json', mimeType: 'application/json', size: 18342, sha256: '9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b', sessionId: IDS.grafana, createdAt: at(60 * 28) },
  { id: id('d000000c', 3), ownerId: 'alex', projectId: IDS.homelab, filename: 'node-inventory.csv', mimeType: 'text/csv', size: 412, sha256: 'c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2', createdAt: at(60 * 24 * 5) },
  { id: id('d000000c', 4), ownerId: 'alex', projectId: IDS.homelab, filename: 'ups-dashboard-preview.html', mimeType: 'text/html; charset=utf-8', size: upsPreviewHtml.length, sha256: '3b1f9c7e5a2d8f4c6e0a9b7d5f3c1e8a6d4b2f0c9e7a5d3b1f8c6e4a2d0b9f7e', sessionId: IDS.grafana, createdAt: at(60 * 30 - 6) },
  { id: id('d000000c', 5), ownerId: 'alex', projectId: IDS.homelab, filename: 'ups-load-24h.svg', mimeType: 'image/svg+xml', size: upsChartSvg.length, sha256: '7e5c3a1f9d7b5e3c1a8f6d4b2e0c8a6f4d2b0e9c7a5f3d1b8e6c4a2f0d9b7e5c', sessionId: IDS.grafana, createdAt: at(60 * 30 - 7) },
];

/** What a download of each demo file gives, made up like the rest. */
export const artifactContent: Record<string, string> = {
  [artifacts[0].id]: '# Ingress incident — node-3\n\nThe second controller replica crashed because traefik held host port 443.\n',
  [artifacts[1].id]: '{ "title": "UPS", "panels": [] }\n',
  [artifacts[2].id]: 'node,role,gpu\nnode-1,control-plane,no\nnode-2,worker,no\nnode-3,worker,yes\n',
  [artifacts[3].id]: upsPreviewHtml,
  [artifacts[4].id]: upsChartSvg,
};

export const skills = [
  {
    id: id('d000000d', 1),
    ownerId: 'alex',
    projectId: IDS.homelab,
    name: 'ingress-triage',
    description: 'Find why an ingress controller replica will not start, without changing the cluster.',
    source: { type: 'GIT', url: 'https://git.home.example/alex/skills', path: 'ingress-triage', revision: 'main' },
    installedRevision: '4e1f0a9c7b3d2e8f6a5c4b3d2e1f0a9c8b7d6e5f',
    installedAt: at(60 * 24 * 3),
    artifactId: id('d000000c', 90),
    bundleSha256: '0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a0f9e',
    createdAt: at(60 * 24 * 3),
    updatedAt: at(60 * 24 * 3),
  },
  {
    id: id('d000000d', 2),
    ownerId: 'alex',
    projectId: IDS.homelab,
    name: 'helm-pinning',
    description: 'Pin every chart version and bump it in its own commit.',
    source: { type: 'UPLOAD', url: 'helm-pinning.tar.gz', path: '' },
    installedRevision: 'sha256:7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e9d8c',
    installedAt: at(60 * 24 * 8),
    artifactId: id('d000000c', 91),
    bundleSha256: '1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b',
    createdAt: at(60 * 24 * 8),
    updatedAt: at(60 * 24 * 8),
  },
];

export const skillFiles: Record<string, { path: string; size: number; text?: string }[]> = {
  [skills[0].id]: [
    {
      path: 'SKILL.md',
      size: 512,
      text: '---\nname: ingress-triage\ndescription: Find why an ingress controller replica will not start, without changing the cluster.\n---\n\n# Ingress triage\n\nUse this when an ingress controller pod is in `CrashLoopBackOff`.\n\n1. Read the **previous** logs of the crashed pod.\n2. Look for `address already in use` — another workload holds a host port.\n3. Run `scripts/who-holds-443.sh <node>` to name it.\n\nNever restart or delete anything: report what you found and ask.\n',
    },
    { path: 'reference/host-ports.md', size: 160, text: '# Host ports\n\nEvery ingress is a LoadBalancer service on the MetalLB pool. A pod with `hostNetwork: true` takes ports on the node for everyone else.\n' },
    {
      path: 'scripts/who-holds-443.sh',
      size: 240,
      text: '#!/bin/sh\n# Lists the pods on a node that ask for host port 443.\nset -eu\nnode="$1"\nkubectl get pods -A -o json --field-selector "spec.nodeName=$node" |\n  jq -r \'.items[] | select(any(.spec.containers[].ports[]?; .hostPort == 443)) | "\\(.metadata.namespace)/\\(.metadata.name)"\'\n',
    },
    { path: 'assets/diagram.png', size: 48211 },
  ],
  [skills[1].id]: [
    { path: 'SKILL.md', size: 220, text: '---\nname: helm-pinning\ndescription: Pin every chart version and bump it in its own commit.\n---\n\nEvery `helmCharts` entry carries an exact `version`. An upgrade changes one version, in a commit of its own.\n' },
  ],
};

export const schedules = [
  {
    id: id('d0000008', 1),
    sessionId: IDS.backups,
    cron: '30 7 * * *',
    timezone: 'Europe/Paris',
    message: 'Check last night’s backups.',
    enabled: true,
    nextRunAt: nextDaily(7, 30, 1)[0],
    lastRunAt: at(31),
    lastOutcome: 'SENT',
    lastJobId: id('d0000006', 5),
    upcoming: nextDaily(7, 30, 3),
    createdAt: at(60 * 9),
    updatedAt: at(31),
  },
  {
    id: id('d0000008', 2),
    sessionId: IDS.backups,
    cron: '0 9 * * 1',
    timezone: 'Europe/Paris',
    message: 'Summarise the week’s backup sizes and growth.',
    enabled: false,
    lastRunAt: at(60 * 24 * 3),
    lastOutcome: 'SKIPPED_BUSY',
    upcoming: [],
    createdAt: at(60 * 24 * 10),
    updatedAt: at(60 * 24 * 2),
  },
];

const audit = (n: number, action: string, minutesAgo: number, rest: Record<string, unknown>) => ({
  id: id('d000000e', n),
  ownerId: 'alex',
  actorId: 'alex',
  action,
  createdAt: at(minutesAgo),
  ...rest,
});

const policyDetail = (overrides: Record<string, unknown>) => ({ ...projectPolicies[IDS.homelab], ...overrides });

export const auditEntries = [
  audit(1, 'validation.resolved', 22.5, { projectId: IDS.homelab, sessionId: IDS.ingress, channel: 'mobile', detail: { approved: true, title: 'Bash: git push origin main', note: '' } }),
  audit(2, 'validation.resolved', 63.5, {
    projectId: IDS.homelab,
    sessionId: IDS.ingress,
    channel: 'web',
    detail: { approved: true, title: 'Bash: helm -n ingress-nginx upgrade ingress-nginx ingress-nginx/ingress-nginx --version 4.11.3 -f kubernetes/ingress-nginx/values.yaml', note: '' },
  }),
  audit(3, 'validation.resolved', 66, { projectId: IDS.homelab, sessionId: IDS.ingress, channel: 'web', detail: { approved: false, title: 'Bash: kubectl -n ingress-nginx delete pod ingress-nginx-controller-7d9f8c6b5-zt8pn', note: 'Not before we know why it crashes.' } }),
  audit(4, 'execution_policy.set', 60 * 5, { projectId: IDS.homelab, subjectId: IDS.homelab, channel: 'web', detail: policyDetail({}) }),
  audit(5, 'execution_policy.set', 60 * 24 + 30, {
    projectId: IDS.homelab,
    subjectId: IDS.homelab,
    channel: 'web',
    detail: policyDetail({ mode: 'INTERACTIVE', allowGitPush: true, maxDurationSeconds: 0, rules: (projectPolicies[IDS.homelab].rules as unknown[]).slice(0, 2) }),
  }),
  audit(6, 'session.backend_changed', 60 * 24 * 2, {
    projectId: IDS.homelab,
    sessionId: IDS.backups,
    subjectId: IDS.nas,
    channel: 'web',
    detail: { from: IDS.laptop, to: IDS.nas, missingSkills: ['restic-helpers'] },
  }),
  audit(7, 'execution_policy.set', 60 * 24 * 3, {
    projectId: IDS.homelab,
    subjectId: IDS.homelab,
    channel: 'api',
    detail: policyDetail({ mode: 'INTERACTIVE', allowGitPush: true, maxDurationSeconds: 0, rules: [] }),
  }),
];

export const pushConfig = {
  publicKey: 'BDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemo',
  subscriptions: [{ id: id('d000000f', 1), clientId: 'demo-phone', label: 'Android — Chrome, installed', createdAt: at(60 * 24 * 6), lastUsedAt: at(30) }],
};

export const clients = [
  { id: 'demo-desktop', name: 'macOS — Firefox', channel: 'web', active: true, since: at(40) },
  { id: 'demo-phone', name: 'Android — Chrome, installed', channel: 'web', active: false, since: at(60 * 3) },
];

/** The diff of the one file the ingress fix changed, as a backend would give it. */
export const ingressDiff = `--- a/kubernetes/ingress-nginx/values.yaml
+++ b/kubernetes/ingress-nginx/values.yaml
@@ -1,9 +1,14 @@
 controller:
   replicaCount: 2
-  hostNetwork: true
-  dnsPolicy: ClusterFirstWithHostNet
+  affinity:
+    podAntiAffinity:
+      preferredDuringSchedulingIgnoredDuringExecution:
+        - weight: 100
+          podAffinityTerm:
+            topologyKey: kubernetes.io/hostname
   admissionWebhooks:
     enabled: true
     timeoutSeconds: 10
   resources:
`;

// ---------------------------------------------------------------- repositories

/**
 * Where each Session's working directory stands in git. The cert-manager one
 * is behind origin without knowing it: a refresh from origin finds the commit
 * someone pushed from another machine.
 */
const repository = (directory: string, fields: Record<string, unknown>) => ({
  directory, tracked: true, ahead: 0, behind: 0, upstreamGone: false,
  staged: 0, unstaged: 0, untracked: 0, conflicted: 0, upstream: 'origin/main', ...fields,
});

export const repositories: Record<string, Record<string, unknown>> = {
  [IDS.certManager]: repository('/home/alex/src/homelab', { branch: 'cert-manager-1.16', head: '4f2a9c1', upstream: 'origin/cert-manager-1.16', ahead: 2, unstaged: 3, untracked: 1, fetchedAt: at(60 * 5) }),
  [IDS.ingress]: repository('/home/alex/src/homelab', { branch: 'main', head: '9be03d7', fetchedAt: at(52) }),
  [IDS.backups]: repository('/srv/homelab', { branch: 'main', head: '9be03d7', behind: 4, fetchedAt: at(60 * 24 * 2) }),
  [IDS.grafana]: repository('/home/alex/src/homelab', { branch: 'grafana-ups', head: 'c71d5e0', upstream: undefined, staged: 2, fetchedAt: at(60 * 31) }),
  [IDS.blog]: repository('/home/alex/src/site', { branch: 'drafts/homelab-rebuild', head: '12ab9f4', upstream: 'origin/drafts/homelab-rebuild', ahead: 1, fetchedAt: at(60 * 26) }),
  [IDS.oldSession]: { directory: '/home/alex', tracked: false, ahead: 0, behind: 0, upstreamGone: false, staged: 0, unstaged: 0, untracked: 0, conflicted: 0 },
};
