import { ApiError } from '@/api/client';
import { announceJobEnded } from '@/lib/job-ended';

import * as data from './data';

/**
 * Core, played in memory.
 *
 * Every request the client makes in the demo is answered here, from the
 * fictitious world in data.ts, and nothing leaves the browser. What a person
 * changes — a task filed, a decision pinned, a permission approved — changes
 * this copy for as long as the tab is open, so the demo can be used, not only
 * looked at. A reload starts it over.
 */

type Json = Record<string, unknown>;

const clone = <T>(value: T): T => structuredClone(value);
/** A field of a request body, read as the text it should be. */
const text = (value: unknown, fallback = '') => (typeof value === 'string' ? value : fallback);

const state = {
  projects: clone(data.projects) as Json[],
  sessions: clone(data.sessions) as Json[],
  timelines: data.timelines() as Record<string, { runs: Json[]; jobs: Json[]; events: Json[] }>,
  validations: clone(data.validations) as Json[],
  userInputs: clone(data.userInputs) as Json[],
  backends: clone(data.backends) as Json[],
  tasks: clone(data.tasks) as Json[],
  decisions: clone(data.decisions) as Json[],
  artifacts: clone(data.artifacts) as Json[],
  skills: clone(data.skills) as Json[],
  schedules: clone(data.schedules) as Json[],
  audit: clone(data.auditEntries) as Json[],
  projectPolicies: clone(data.projectPolicies),
  sessionPolicies: {} as Record<string, Json | null>,
  pushSubscriptions: clone(data.pushConfig.subscriptions) as Json[],
  files: {} as Record<string, Blob>,
};

let sequence = 9000;
const nowIso = () => new Date().toISOString();
const newId = () => crypto.randomUUID();

const notFound = (what = 'that') => new ApiError(404, 'not_found', `${what} is not in the demo`);
const unavailable = (what: string) => new ApiError(503, 'demo', `${what} is not available in the demo: nothing here reaches a server.`);

function list(items: unknown[]) {
  return { items };
}

function sessionOf(id: string) {
  const session = state.sessions.find((entry) => entry.id === id);
  if (!session) throw notFound('This session');
  return session;
}

function timelineOf(id: string) {
  sessionOf(id);
  return (state.timelines[id] ??= { runs: [], jobs: [], events: [] });
}

function pushEvent(sessionId: string, type: string, payload: Json, jobId?: string) {
  const timeline = timelineOf(sessionId);
  const session = sessionOf(sessionId);
  timeline.events.push({
    id: newId(),
    sequence: sequence++,
    timestamp: nowIso(),
    type,
    projectId: session.projectId,
    sessionId,
    runId: timeline.runs.at(-1)?.id,
    jobId,
    payload,
  });
  session.updatedAt = nowIso();
  // The demo has no stream to say a Job ended, so it says it itself, once the
  // request that ended it has been answered.
  if (type === 'job.completed' || type === 'job.failed') {
    const said = type === 'job.failed' ? text(payload.error) : text(payload.summary);
    setTimeout(() => announceJobEnded({ sessionId, failed: type === 'job.failed', text: said }), 300);
  }
}

function attentionFor(sessionId?: string) {
  const pending = <T extends Json>(items: T[]) =>
    items.filter((item) => item.status === 'PENDING' && (!sessionId || (item.scope as Json).sessionId === sessionId));
  return { validations: pending(state.validations), userInputs: pending(state.userInputs) };
}

/** Recomputes what a session says it is doing, from its jobs. */
function settle(sessionId: string) {
  const session = sessionOf(sessionId);
  const active = timelineOf(sessionId).jobs.find((job) => !['COMPLETED', 'FAILED', 'CANCELLED'].includes(String(job.status)));
  if (active) session.activeJobStatus = active.status;
  else delete session.activeJobStatus;
}

/** The demo's agent: honest about being one, and quick. */
function answer(sessionId: string, text: string, delivery?: string) {
  const timeline = timelineOf(sessionId);
  const running = timeline.jobs.find((job) => !['COMPLETED', 'FAILED', 'CANCELLED'].includes(String(job.status)));
  if (running && delivery) {
    pushEvent(sessionId, 'user.message', { text, delivery }, String(running.id));
    return running;
  }
  const jobId = newId();
  const job: Json = { id: jobId, runId: timeline.runs.at(-1)?.id, status: 'COMPLETED', originChannel: 'web', createdAt: nowIso(), updatedAt: nowIso(), startedAt: nowIso(), endedAt: nowIso() };
  timeline.jobs.push(job);
  pushEvent(sessionId, 'job.created', { runId: job.runId }, jobId);
  pushEvent(sessionId, 'user.message', { text }, jobId);
  pushEvent(sessionId, 'job.started', {}, jobId);
  pushEvent(
    sessionId,
    'agent.message',
    {
      text: 'This is the demo, so no agent reads this and nothing runs. In Threavia, your message would go to the backend running this session — a machine of yours — and its agent would answer here, step by step, asking before anything the permissions do not allow.',
    },
    jobId,
  );
  pushEvent(sessionId, 'job.completed', { summary: 'Answered (the demo agent).', usage: { costUsd: 0, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0 } }, jobId);
  settle(sessionId);
  return job;
}

function snapshot(sessionId: string) {
  const timeline = timelineOf(sessionId);
  return {
    session: sessionOf(sessionId),
    runs: timeline.runs,
    jobs: timeline.jobs,
    events: timeline.events,
    attention: attentionFor(sessionId),
    cursor: sequence,
  };
}

function effectivePolicy(sessionId: string) {
  const session = sessionOf(sessionId);
  const project = state.projectPolicies[String(session.projectId)] ?? {};
  const own = state.sessionPolicies[sessionId] ?? null;
  return {
    effective: own ? { ...own, rules: [...((project.rules as unknown[]) ?? []), ...((own.rules as unknown[]) ?? [])] } : project,
    inherited: !own,
    project,
    ...(own ? { own } : {}),
  };
}

function ready(projectId: string) {
  const byId = new Map(state.tasks.map((task) => [task.id, task]));
  return state.tasks.filter(
    (task) =>
      task.projectId === projectId &&
      task.status === 'TODO' &&
      ((task.dependsOn as string[]) ?? []).every((other) => byId.get(other)?.status === 'DONE'),
  );
}

function record(action: string, rest: Json) {
  state.audit.unshift({ id: newId(), ownerId: 'alex', actorId: 'alex', action, channel: 'web', createdAt: nowIso(), ...rest });
}

type Handler = (match: RegExpMatchArray, body: Json, query: URLSearchParams) => unknown;

const routes: [string, RegExp, Handler][] = [
  ['GET', /^\/api\/v1\/me$/, () => data.me],
  ['POST', /^\/api\/v1\/me\/presence$/, () => undefined],
  ['GET', /^\/api\/v1\/me\/attention$/, () => attentionFor()],
  ['GET', /^\/api\/v1\/me\/pinned-sessions$/, () =>
    list(state.sessions
      .filter((session) => session.pinnedAt && session.status === 'ACTIVE')
      .sort((a, b) => String(a.pinnedAt).localeCompare(String(b.pinnedAt))))],
  ['GET', /^\/api\/v1\/me\/clients$/, () => list(data.clients)],
  ['GET', /^\/api\/v1\/me\/push$/, () => ({ publicKey: data.pushConfig.publicKey, subscriptions: state.pushSubscriptions })],
  ['POST', /^\/api\/v1\/me\/push\/subscriptions$/, () => { throw unavailable('Notifications'); }],
  ['DELETE', /^\/api\/v1\/me\/push\/subscriptions\/([^/]+)$/, (m) => {
    state.pushSubscriptions = state.pushSubscriptions.filter((entry) => entry.id !== m[1]);
  }],
  ['POST', /^\/api\/v1\/me\/push\/test$/, () => { throw unavailable('A test notification'); }],
  ['GET', /^\/api\/v1\/me\/audit$/, (_m, _b, query) => {
    const projectId = query.get('projectId');
    return list(state.audit.filter((entry) => !projectId || entry.projectId === projectId));
  }],

  ['GET', /^\/api\/v1\/projects$/, () => list(state.projects)],
  ['POST', /^\/api\/v1\/projects$/, (_m, body) => {
    const project = { id: newId(), ownerId: 'alex', name: text(body.name, 'project'), description: '', instructions: '', status: 'ACTIVE', createdAt: nowIso(), updatedAt: nowIso() };
    state.projects.push(project);
    state.projectPolicies[project.id] = { mode: 'INTERACTIVE', allowFilesystemWrite: true, allowGitCommit: true, allowGitPush: false, allowNetwork: true };
    return project;
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)$/, (m) => state.projects.find((project) => project.id === m[1]) ?? (() => { throw notFound('This project'); })()],
  ['PATCH', /^\/api\/v1\/projects\/([^/]+)$/, (m, body) => {
    const project = state.projects.find((entry) => entry.id === m[1]);
    if (!project) throw notFound('This project');
    Object.assign(project, body, { updatedAt: nowIso() });
    return project;
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/sessions$/, (m, _b, query) =>
    list(
      state.sessions
        .filter((session) => session.projectId === m[1] && (query.get('includeArchived') === 'true' || session.status !== 'ARCHIVED'))
        .sort((a, b) => String(b.updatedAt).localeCompare(String(a.updatedAt))),
    )],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/directories$/, (m) => list(data.directories.filter((entry) => entry.projectId === m[1]))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/directories$/, () => { throw unavailable('Registering a directory'); }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/policy$/, (m) => state.projectPolicies[m[1]] ?? {}],
  ['PUT', /^\/api\/v1\/projects\/([^/]+)\/policy$/, (m, body) => {
    state.projectPolicies[m[1]] = body;
    record('execution_policy.set', { projectId: m[1], subjectId: m[1], detail: body });
    return body;
  }],

  ['GET', /^\/api\/v1\/projects\/([^/]+)\/tasks\/ready$/, (m) => list(ready(m[1]))],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/tasks$/, (m, _b, query) =>
    list(state.tasks.filter((task) => task.projectId === m[1] && (query.get('includeDone') === 'true' || task.status !== 'DONE')))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/tasks$/, (m, body) => {
    const task = { id: newId(), projectId: m[1], title: body.title, description: body.description ?? '', status: 'TODO', dependsOn: body.dependsOn ?? [], createdAt: nowIso(), updatedAt: nowIso() };
    state.tasks.push(task);
    return task;
  }],
  ['PATCH', /^\/api\/v1\/tasks\/([^/]+)$/, (m, body) => {
    const task = state.tasks.find((entry) => entry.id === m[1]);
    if (!task) throw notFound('This task');
    Object.assign(task, body, { updatedAt: nowIso(), completedAt: body.status === 'DONE' ? nowIso() : undefined });
    return task;
  }],
  ['DELETE', /^\/api\/v1\/tasks\/([^/]+)$/, (m) => {
    state.tasks = state.tasks.filter((task) => task.id !== m[1]);
    for (const task of state.tasks) task.dependsOn = ((task.dependsOn as string[]) ?? []).filter((other) => other !== m[1]);
  }],
  ['POST', /^\/api\/v1\/tasks\/([^/]+)\/dependencies$/, (m, body) => {
    const task = state.tasks.find((entry) => entry.id === m[1]);
    if (!task) throw notFound('This task');
    task.dependsOn = [...((task.dependsOn as string[]) ?? []), String(body.dependsOn)];
    return task;
  }],
  ['DELETE', /^\/api\/v1\/tasks\/([^/]+)\/dependencies\/([^/]+)$/, (m) => {
    const task = state.tasks.find((entry) => entry.id === m[1]);
    if (!task) throw notFound('This task');
    task.dependsOn = ((task.dependsOn as string[]) ?? []).filter((other) => other !== m[2]);
    return task;
  }],

  ['GET', /^\/api\/v1\/projects\/([^/]+)\/decisions$/, (m) =>
    list(state.decisions.filter((entry) => entry.projectId === m[1] && entry.status === 'ACTIVE'))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/decisions$/, (m, body) => {
    const decision = { id: newId(), projectId: m[1], title: body.title, content: body.content ?? '', importance: body.importance ?? 'NORMAL', status: 'ACTIVE', createdAt: nowIso(), updatedAt: nowIso() };
    state.decisions.unshift(decision);
    return decision;
  }],
  ['PATCH', /^\/api\/v1\/decisions\/([^/]+)$/, (m, body) => {
    const decision = state.decisions.find((entry) => entry.id === m[1]);
    if (!decision) throw notFound('This decision');
    decision.importance = body.importance;
    return decision;
  }],
  ['DELETE', /^\/api\/v1\/decisions\/([^/]+)$/, (m) => {
    state.decisions = state.decisions.filter((entry) => entry.id !== m[1]);
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/search$/, () => ({ tasks: [], decisions: [], history: [] })],

  ['GET', /^\/api\/v1\/projects\/([^/]+)\/artifacts$/, (m, _b, query) =>
    list(state.artifacts.filter((entry) => entry.projectId === m[1] && (!query.get('sessionId') || entry.sessionId === query.get('sessionId'))))],
  ['DELETE', /^\/api\/v1\/artifacts\/([^/]+)$/, (m) => {
    state.artifacts = state.artifacts.filter((entry) => entry.id !== m[1]);
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/skills$/, (m) => list(state.skills.filter((entry) => entry.projectId === m[1]))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/skills$/, () => { throw unavailable('Installing a skill'); }],
  ['DELETE', /^\/api\/v1\/skills\/([^/]+)$/, (m) => {
    state.skills = state.skills.filter((entry) => entry.id !== m[1]);
  }],
  ['GET', /^\/api\/v1\/skills\/([^/]+)\/files$/, (m) => list(data.skillFiles[m[1]] ?? [])],

  ['GET', /^\/api\/v1\/backends$/, () => list(state.backends)],
  ['GET', /^\/api\/v1\/backends\/([^/]+)\/skills$/, () => list([])],
  ['POST', /^\/api\/v1\/backends\/([^/]+)\/revoke$/, (m) => {
    const backend = state.backends.find((entry) => entry.id === m[1]);
    if (backend) backend.ownershipStatus = 'REVOKED';
  }],
  ['POST', /^\/api\/v1\/backends\/claim$/, () => { throw unavailable('Claiming a backend'); }],
  ['POST', /^\/api\/v1\/backend-tokens$/, () => ({ token: 'demo-token-not-valid-anywhere', expiresAt: new Date(Date.now() + 3_600_000).toISOString() })],

  ['POST', /^\/api\/v1\/sessions\/start$/, (_m, body) => {
    const project = state.projects.find((entry) => entry.id === body.projectId) ?? state.projects[0];
    const sessionId = newId();
    const message = text(body.message);
    state.sessions.push({ id: sessionId, projectId: project.id, title: message.length > 60 ? `${message.slice(0, 60)}…` : message || 'New session', status: 'ACTIVE', createdAt: nowIso(), updatedAt: nowIso() });
    state.timelines[sessionId] = {
      runs: [{ id: newId(), sessionId, backendInstanceId: body.backendInstanceId ?? data.IDS.laptop, resumeStatus: 'AVAILABLE', createdAt: nowIso(), updatedAt: nowIso() }],
      jobs: [],
      events: [],
    };
    pushEvent(sessionId, 'session.created', {});
    const job = answer(sessionId, message);
    return { session: sessionOf(sessionId), run: state.timelines[sessionId].runs[0], job };
  }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)$/, (m) => snapshot(m[1])],
  ['PATCH', /^\/api\/v1\/sessions\/([^/]+)$/, (m, body) => {
    const session = sessionOf(m[1]);
    if (body.title !== undefined) session.title = body.title;
    if (body.pinned === true) session.pinnedAt ??= nowIso();
    if (body.pinned === false) delete session.pinnedAt;
    if (body.backendInstanceId) {
      const timeline = timelineOf(m[1]);
      const from = timeline.runs.at(-1)?.backendInstanceId;
      const runEntry = { id: newId(), sessionId: m[1], backendInstanceId: body.backendInstanceId, resumeStatus: 'UNKNOWN', createdAt: nowIso(), updatedAt: nowIso() };
      timeline.runs.push(runEntry);
      record('session.backend_changed', { projectId: session.projectId, sessionId: m[1], subjectId: body.backendInstanceId, detail: { from, to: body.backendInstanceId, missingSkills: [] } });
      return { run: runEntry, missingSkills: [] };
    }
    return session;
  }],
  ['POST', /^\/api\/v1\/sessions\/([^/]+)\/(archive|restore)$/, (m) => {
    const session = sessionOf(m[1]);
    session.status = m[2] === 'archive' ? 'ARCHIVED' : 'ACTIVE';
    return session;
  }],
  ['DELETE', /^\/api\/v1\/sessions\/([^/]+)$/, (m, _b, query) => {
    state.sessions = state.sessions.filter((session) => session.id !== m[1]);
    // Its files go with it when asked; otherwise they stay, attached to nothing.
    if (query.get('artifacts') === 'delete') state.artifacts = state.artifacts.filter((entry) => entry.sessionId !== m[1]);
    else for (const entry of state.artifacts) if (entry.sessionId === m[1]) delete entry.sessionId;
  }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/events$/, () => list([])],
  ['POST', /^\/api\/v1\/sessions\/([^/]+)\/messages$/, (m, body) => answer(m[1], text(body.message), body.delivery as string | undefined)],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/diff$/, (_m, _b, query) => ({ path: query.get('path'), diff: data.ingressDiff, binary: false, truncated: false })],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/repository$/, (m, _b, query) => {
    const repo = data.repositories[m[1]];
    if (!repo) throw notFound('This session');
    if (query.get('fetch') === 'true' && repo.tracked) {
      // Origin heard from now: the cert-manager branch learns of a push.
      repo.fetchedAt = new Date().toISOString();
      if (m[1] === data.IDS.certManager) repo.behind = 1;
    }
    return { ...repo, checkedAt: new Date().toISOString() };
  }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/policy$/, (m) => effectivePolicy(m[1])],
  ['PUT', /^\/api\/v1\/sessions\/([^/]+)\/policy$/, (m, body) => {
    state.sessionPolicies[m[1]] = body && Object.keys(body).length ? body : null;
    return effectivePolicy(m[1]).effective;
  }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/schedules$/, (m) => list(state.schedules.filter((entry) => entry.sessionId === m[1]))],
  ['POST', /^\/api\/v1\/sessions\/([^/]+)\/schedules$/, (m, body) => {
    const schedule = { id: newId(), sessionId: m[1], cron: body.cron, timezone: body.timezone ?? 'Europe/Paris', message: body.message, enabled: true, upcoming: [], createdAt: nowIso(), updatedAt: nowIso() };
    state.schedules.push(schedule);
    return schedule;
  }],
  ['PATCH', /^\/api\/v1\/schedules\/([^/]+)$/, (m, body) => {
    const schedule = state.schedules.find((entry) => entry.id === m[1]);
    if (!schedule) throw notFound('This schedule');
    Object.assign(schedule, body, { updatedAt: nowIso() });
    if (body.enabled === false) delete schedule.nextRunAt;
    return schedule;
  }],
  ['DELETE', /^\/api\/v1\/schedules\/([^/]+)$/, (m) => {
    state.schedules = state.schedules.filter((entry) => entry.id !== m[1]);
  }],
  ['POST', /^\/api\/v1\/schedules\/([^/]+)\/run$/, (m) => {
    const schedule = state.schedules.find((entry) => entry.id === m[1]);
    if (!schedule) throw notFound('This schedule');
    return answer(String(schedule.sessionId), String(schedule.message));
  }],

  ['POST', /^\/api\/v1\/jobs\/([^/]+)\/cancel$/, (m) => {
    for (const [sessionId, timeline] of Object.entries(state.timelines)) {
      const job = timeline.jobs.find((entry) => entry.id === m[1]);
      if (!job) continue;
      job.status = 'CANCELLED';
      job.endedAt = nowIso();
      for (const list of [state.validations, state.userInputs]) {
        for (const item of list) if ((item.scope as Json).jobId === m[1]) item.status = 'CANCELLED';
      }
      pushEvent(sessionId, 'job.cancelled', {}, String(job.id));
      settle(sessionId);
      return job;
    }
    throw notFound('This job');
  }],
  ['POST', /^\/api\/v1\/validations\/([^/]+)\/resolve$/, (m, body) => {
    const validation = state.validations.find((entry) => entry.id === m[1]);
    if (!validation) throw notFound('This request');
    const scope = validation.scope as Json;
    Object.assign(validation, { status: 'RESOLVED', approved: body.approved, note: body.note ?? '', resolvedAt: nowIso() });
    pushEvent(String(scope.sessionId), 'validation.resolved', { validationId: validation.id, approved: body.approved, note: body.note ?? '', title: validation.title }, String(scope.jobId));
    record('validation.resolved', { projectId: scope.projectId, sessionId: scope.sessionId, detail: { approved: body.approved, title: validation.title, note: body.note ?? '' } });
    const timeline = timelineOf(String(scope.sessionId));
    const job = timeline.jobs.find((entry) => entry.id === scope.jobId);
    if (job) {
      const command = text((validation.requestPayload as Json & { input: Json }).input.command);
      const toolCallId = newId();
      if (body.approved) {
        pushEvent(String(scope.sessionId), 'tool.started', { toolCallId, name: 'Bash', input: { command } }, String(job.id));
        pushEvent(String(scope.sessionId), 'tool.completed', { toolCallId, name: 'Bash', output: { output: 'customresourcedefinition.apiextensions.k8s.io/certificates.cert-manager.io configured\ndeployment.apps/cert-manager configured' } }, String(job.id));
        pushEvent(String(scope.sessionId), 'agent.message', { text: 'Applied. cert-manager runs 1.16.1 and both ClusterIssuers report `Ready`. (In the demo nothing ran: this answer is part of the script.)' }, String(job.id));
      } else {
        pushEvent(String(scope.sessionId), 'agent.message', { text: 'Understood, I will not apply it. The kustomization stays bumped locally, for you to apply when you choose.' }, String(job.id));
      }
      pushEvent(String(scope.sessionId), 'job.completed', { summary: body.approved ? 'cert-manager runs 1.16.1 and both ClusterIssuers are Ready.' : 'Not applied, as you asked.', usage: { costUsd: 0.62, inputTokens: 900, outputTokens: 1400, cacheReadTokens: 210000, cacheWriteTokens: 9000 } }, String(job.id));
      Object.assign(job, { status: 'COMPLETED', endedAt: nowIso() });
      settle(String(scope.sessionId));
    }
    return validation;
  }],
  ['POST', /^\/api\/v1\/user-input\/([^/]+)\/resolve$/, (m, body) => {
    const request = state.userInputs.find((entry) => entry.id === m[1]);
    if (!request) throw notFound('This question');
    const scope = request.scope as Json;
    Object.assign(request, { status: 'RESOLVED', value: body.value, resolvedAt: nowIso() });
    pushEvent(String(scope.sessionId), 'user_input.resolved', { requestId: request.id, value: body.value }, String(scope.jobId));
    pushEvent(String(scope.sessionId), 'agent.message', { text: `Noted: “${String(body.value)}”. (In the demo the agent stops here; for real it would carry on from your answer.)` }, String(scope.jobId));
    const job = timelineOf(String(scope.sessionId)).jobs.find((entry) => entry.id === scope.jobId);
    if (job) Object.assign(job, { status: 'COMPLETED', endedAt: nowIso() });
    pushEvent(String(scope.sessionId), 'job.completed', { summary: `Carrying on with “${text(body.value)}”.` }, String(scope.jobId));
    settle(String(scope.sessionId));
    return request;
  }],
];

/** Answers one request as Core would, or refuses it as Core would. */
export async function demoRequest(method: string, path: string, body: unknown): Promise<unknown> {
  // A beat of latency, so loading states are seen as they would be.
  await new Promise((resolve) => setTimeout(resolve, 60));
  const url = new URL(path, 'http://demo');
  const json = (body ?? {}) as Json;
  for (const [verb, pattern, handler] of routes) {
    if (verb !== method) continue;
    const match = url.pathname.match(pattern);
    if (match) return clone(handler(match, json, url.searchParams) ?? null);
  }
  throw unavailable(`${method} ${url.pathname}`);
}

/** The content of a demo file, from memory: what data.ts made up, or what was dropped. */
export async function demoBlob(path: string): Promise<Blob> {
  await new Promise((resolve) => setTimeout(resolve, 120));
  const match = path.match(/^\/api\/v1\/artifacts\/([^/]+)\/content$/);
  if (!match) throw notFound('That file');
  const uploaded = state.files[match[1]];
  if (uploaded) return uploaded;
  const content = data.artifactContent[match[1]];
  if (content === undefined) throw notFound('That file');
  return new Blob([content]);
}

/** An upload, kept in memory: a file, or a refusal for what cannot be faked. */
export async function demoUpload(path: string, file: File): Promise<unknown> {
  await new Promise((resolve) => setTimeout(resolve, 300));
  const match = path.match(/^\/api\/v1\/projects\/([^/]+)\/artifacts$/);
  if (!match) throw unavailable('Installing a skill');
  const artifact = {
    id: newId(),
    ownerId: 'alex',
    projectId: match[1],
    filename: file.name,
    mimeType: file.type || 'application/octet-stream',
    size: file.size,
    sha256: Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) => byte.toString(16).padStart(2, '0')).join(''),
    createdAt: nowIso(),
  };
  state.artifacts.unshift(artifact);
  state.files[artifact.id] = file;
  return artifact;
}
