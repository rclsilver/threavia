import { monitors } from './monitors';
import { demoMCP } from './mcp';
import { demoControl } from './control';
import { ApiError } from '@/api/client';
import { announceJobEnded } from '@/lib/job-ended';
import { uuid } from '@/lib/uuid';
import type { Event } from '@/api/types';

import * as data from './data';
import { demoReview, demoReviewAction, demoReviewFile } from './review';
import type { ReviewAction } from '@/api/reviews';

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
  directories: clone(data.directories) as Json[],
  bindings: clone(data.directoryBindings) as Json[],
  tasks: clone(data.tasks) as Json[],
  decisions: clone(data.decisions) as Json[],
  artifacts: clone(data.artifacts) as Json[],
  skills: clone(data.skills) as Json[],
  schedules: clone(data.schedules) as Json[],
  audit: clone(data.auditEntries) as Json[],
  projectPolicies: clone(data.projectPolicies),
  instancePolicy: clone(data.instancePolicy) as Json | null,
  backendPolicies: {} as Record<string, Json | null>,
  sessionPolicies: {} as Record<string, Json | null>,
  pushSubscriptions: clone(data.pushConfig.subscriptions) as Json[],
  files: {} as Record<string, Blob>,
};

let sequence = 9000;
const nowIso = () => new Date().toISOString();
const newId = uuid;
const lifecycleListeners = new Set<(event: Event) => void>();

/** Reuse the real clients' event path for changes made in the playable demo. */
export function demoLifecycleEvents(receive: (event: Event) => void): () => void {
  lifecycleListeners.add(receive);
  return () => { lifecycleListeners.delete(receive); };
}

function emitLifecycle(type: string, projectId: string | undefined, payload: Json, sessionId?: string) {
  const event: Event = { id: newId(), sequence: sequence++, timestamp: nowIso(), type, projectId, payload, ...(sessionId && { sessionId }) };
  for (const receive of lifecycleListeners) receive(event);
}

function recordKnowledge(action: string, projectId: string | undefined, subjectId: unknown, detail: Json, ownerNotice = false) {
  record(action, { ...(projectId && { projectId }), subjectId, detail });
  emitLifecycle(action, ownerNotice ? undefined : projectId, detail);
}

function taskOf(id: string): Json {
  const task = state.tasks.find((entry) => entry.id === id);
  if (!task) throw notFound('This task');
  return task;
}

function taskDetail(task: Json): Json {
  return { taskId: task.id, title: task.title, status: task.status, dependsOn: [...((task.dependsOn as string[]) ?? [])] };
}

function decisionDetail(decision: Json): Json {
  return { decisionId: decision.id, title: decision.title, status: decision.status, importance: decision.importance };
}

function projectDetail(project: Json): Json {
  return { projectId: project.id, name: project.name, status: project.status };
}

function recordSessionLifecycle(session: Json, action: string, detail: Json) {
  record(action, { projectId: session.projectId, sessionId: session.id, subjectId: session.id, detail });
  if (action !== 'session.deleted') {
    const saved = state.timelines[String(session.id)]?.events.slice().reverse().find((event) => event.type === action);
    if (saved) {
      for (const receive of lifecycleListeners) receive(clone(saved) as Event);
      return;
    }
  }
  emitLifecycle(action, String(session.projectId), detail, action === 'session.deleted' ? undefined : String(session.id));
}

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

function directoryOf(id: string) {
  const directory = state.directories.find((entry) => entry.id === id);
  if (!directory) throw notFound('This directory');
  return directory;
}

function createDirectory(body: Json, projectId?: string) {
  const name = text(body.name).trim();
  if (!name) throw new ApiError(400, 'invalid', 'Enter a directory name.');
  if (state.directories.some((entry) => entry.projectId === projectId && entry.name === name)) throw new ApiError(409, 'conflict', 'This scope already has a directory with that name.');
  const directory = { id: newId(), ...(projectId && { projectId }), name, description: text(body.description).trim(), ...(text(body.gitRemote).trim() && { gitRemote: text(body.gitRemote).trim() }), createdAt: nowIso(), updatedAt: nowIso() };
  state.directories.push(directory); return directory;
}

function recordDecision(body: Json, projectId?: string) {
  const title = text(body.title).trim();
  if (!title) throw new ApiError(400, 'invalid', 'Enter a decision title.');
  const previous = body.supersedes ? state.decisions.find((entry) => entry.id === body.supersedes && (!projectId || entry.projectId === projectId)) : undefined;
  if (body.supersedes && !previous) throw new ApiError(400, 'invalid', 'Choose a decision in this scope, or promote a project decision to global memory.');
  if (previous && previous.status !== 'ACTIVE') throw new ApiError(409, 'conflict', 'This decision has already been replaced.');
  const decision = { id: newId(), ...(projectId && { projectId }), title, content: text(body.content).trim(), importance: body.importance ?? 'NORMAL', status: 'ACTIVE', ...(previous && { supersedes: previous.id }), createdAt: nowIso(), updatedAt: nowIso() };
  if (previous) previous.status = 'SUPERSEDED';
  state.decisions.unshift(decision);
  const promoted = previous?.projectId && previous.projectId !== projectId ? { previousProjectId: previous.projectId } : {};
  recordKnowledge('decision.created', projectId, decision.id, { ...decisionDetail(decision), ...(previous && { supersedes: previous.id }), ...promoted });
  if (previous) recordKnowledge('decision.superseded', text(previous.projectId) || undefined, previous.id, { ...decisionDetail(previous), supersededBy: decision.id, ...promoted });
  return decision;
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
function modelSettings(sessionId: string, model?: string, effort?: string, targetBackend?: Json) {
  const session = sessionOf(sessionId);
  const current = timelineOf(sessionId).runs.at(-1)?.backendInstanceId;
  const backend = targetBackend ?? state.backends.find((entry) => entry.id === current);
  const catalog = backend?.modelCatalog as { defaultModel?: string; defaultEffort?: string; models: { id: string; efforts: string[]; defaultEffort?: string }[] } | undefined;
  const selected = model ?? text(session.model);
  const reasoning = effort ?? text(session.effort);
  const effective = selected || catalog?.defaultModel || '';
  const option = catalog?.models.find((entry) => entry.id === effective);
  if ((selected || reasoning) && !(backend?.features as string[] | undefined)?.includes('MODEL_SELECTION')) throw new ApiError(400, 'invalid', 'This backend does not support model selection.');
  if (selected && !option) throw new ApiError(400, 'invalid', 'This model is unavailable on this backend.');
  if (reasoning && !option?.efforts.includes(reasoning)) throw new ApiError(400, 'invalid', 'This effort is unavailable for the model.');
  return { model: effective, effort: reasoning || (selected ? option?.defaultEffort : catalog?.defaultEffort) || option?.defaultEffort || '' };
}

function previewHandoff(sessionId: string, target: string) {
  const session = sessionOf(sessionId);
  const timeline = timelineOf(sessionId);
  const backend = state.backends.find((entry) => entry.id === target);
  if (!backend) throw notFound('This backend');
  const current = timeline.runs.at(-1);
  const reasons: string[] = [];
  const warnings: string[] = [];
  const capacity = backend.capacity as { activeRuns: number; maxConcurrentRuns: number };
  if (session.status !== 'ACTIVE') reasons.push('The session is archived.');
  if (current?.backendInstanceId === target) reasons.push('The session already uses this backend.');
  if (backend.ownershipStatus === 'REVOKED') reasons.push('The backend is revoked.');
  if (!(backend.capabilities as string[]).includes('CODE')) reasons.push('The backend does not support CODE work.');
  if (capacity.maxConcurrentRuns <= 0) reasons.push('The backend is draining and accepts no new work.');
  else if (capacity.activeRuns >= capacity.maxConcurrentRuns) reasons.push('The backend is full.');
  if (timeline.jobs.some((job) => !['COMPLETED', 'FAILED', 'CANCELLED'].includes(String(job.status)))) reasons.push('Finish or cancel all running and queued jobs before moving.');
  try { modelSettings(sessionId, undefined, undefined, backend); } catch (error) { reasons.push((error as Error).message); }
  if (backend.operationalStatus !== 'READY') warnings.push(`The backend is ${text(backend.operationalStatus).toLowerCase()}; the next job may wait for it.`);
  if (backend.providerAuthState === 'AUTHENTICATION_REQUIRED') warnings.push('Provider sign-in is required on the destination backend.');
  const directory = state.directories.find((entry) => entry.id === session.workingDirectoryId);
  const binding = directory && state.bindings.find((entry) => entry.knownDirectoryId === directory.id && entry.backendInstanceId === target);
  return { fromRunId: current?.id, backend: clone(backend), canMove: reasons.length === 0, reasons, warnings, missingSkills: [], nativeSessionTransferred: false,
    workingDirectory: directory ? { knownDirectoryId: directory.id, name: directory.name, ...(binding && { path: binding.path }), resolution: binding ? 'bound' : 'resolve' } : { resolution: 'backend_default' } };
}

function answer(sessionId: string, text: string, delivery?: string, artifactIds?: string[]) {
  const timeline = timelineOf(sessionId);
  const running = timeline.jobs.find((job) => !['COMPLETED', 'FAILED', 'CANCELLED'].includes(String(job.status)));
  const attachments = (artifactIds ?? []).map((id) => {
    const artifact = state.artifacts.find((entry) => entry.id === id);
    if (!artifact || artifact.projectId !== sessionOf(sessionId).projectId) throw notFound('That attachment');
    return {artifactId:id, filename:artifact.filename, mimeType:artifact.mimeType, size:artifact.size};
  });
  if (running && delivery && delivery !== 'QUEUE') {
    if (delivery === 'NOW') {
      for (const request of [...state.validations, ...state.userInputs]) {
        if (request.status === 'PENDING' && (request.scope as Json).jobId === running.id) {
          request.status = 'RESOLVED';
          request.resolvedChannel = 'system';
          request.resolvedAt = nowIso();
        }
      }
      running.status = 'RUNNING';
      running.updatedAt = nowIso();
    }
    pushEvent(sessionId, 'user.message', { text, delivery }, String(running.id));
    settle(sessionId);
    return running;
  }
  const jobId = newId();
  const settings = modelSettings(sessionId);
  const job: Json = { ...settings, id: jobId, runId: timeline.runs.at(-1)?.id, status: 'COMPLETED', originChannel: 'web', createdAt: nowIso(), updatedAt: nowIso(), startedAt: nowIso(), endedAt: nowIso() };
  timeline.jobs.push(job);
  pushEvent(sessionId, 'job.created', { runId: job.runId }, jobId);
  pushEvent(sessionId, 'user.message', { text, attachments }, jobId);
  pushEvent(sessionId, 'job.started', settings, jobId);
  pushEvent(
    sessionId,
    'agent.message',
    {
      text: 'This is the demo, so no agent reads this and nothing runs. In Threavia, your message would go to the backend running this session — a machine of yours — and its agent would answer here, step by step, asking before anything the permissions do not allow.',
    },
    jobId,
  );
  pushEvent(sessionId, 'job.completed', { ...settings, summary: 'Answered (the demo agent).', usage: { costUsd: 0, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0 } }, jobId);
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
  const run = timelineOf(sessionId).runs.at(-1);
  const backend = defaultPolicyView(state.backendPolicies[String(run?.backendInstanceId)] ?? null, state.instancePolicy).effective;
  const project = defaultPolicyView(state.projectPolicies[String(session.projectId)] ?? null, backend).effective;
  const own = state.sessionPolicies[sessionId] ?? null;
  return {
    effective: defaultPolicyView(own, project).effective,
    inherited: !own,
    project,
    ...(own ? { own } : {}),
  };
}

function defaultPolicyView(own: Json | null, outer: Json | null = null) {
  const parent = outer ?? { mode: 'INTERACTIVE', allowFilesystemWrite: true, allowGitCommit: true, allowGitPush: false, allowNetwork: true };
  const rules = [...((parent.rules as Json[]) ?? []), ...((own?.rules as Json[]) ?? [])];
  const denials = rules.filter((rule) => rule.effect === 'DENY');
  const merged = rules.filter((rule) => rule.effect === 'DENY' || !denials.some((bound) => bound.capability === rule.capability && (!bound.match || bound.match === rule.match)));
  return { effective: { ...(own ?? parent), rules: merged }, parent, inherited: !own, ...(own ? { own } : {}) };
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

function recordScheduleChange(schedule: Json, action: string) {
  const sessionId = String(schedule.sessionId);
  const detail = { scheduleId: schedule.id, cron: schedule.cron, timezone: schedule.timezone, message: schedule.message, enabled: schedule.enabled };
  pushEvent(sessionId, action, detail);
  record(action, { projectId: sessionOf(sessionId).projectId, sessionId, subjectId: schedule.id, detail });
}

type Handler = (match: RegExpMatchArray, body: Json, query: URLSearchParams) => unknown;

const routes: [string, RegExp, Handler][] = [
 ['GET', /^\/api\/v1\/sessions\/([^/]+)\/monitors$/, ([, id]) => ({ items: monitors.filter(m => m.sessionId === id) })],
 ['GET', /^\/api\/v1\/projects\/([^/]+)\/monitors$/, ([, id]) => ({ items: monitors.filter(m => state.sessions.some(s => s.id === m.sessionId && s.projectId === id)) })],
 ['POST', /^\/api\/v1\/monitors\/([^/]+)\/stop$/, ([, id]) => {
  const m = monitors.find(m => m.id === id); if (!m) throw notFound('This monitor');
  m.state = 'STOPPED'; m.updatedAt = nowIso(); emitLifecycle('monitor.changed', state.sessions.find(s => s.id === m.sessionId)?.projectId as string, { id, state: m.state }, m.sessionId);
 }],

  ['GET', /^\/api\/v1\/me\/control-accesses$/, (_m, body) => demoControl('list', '', body)],
  ['POST', /^\/api\/v1\/me\/control-accesses$/, (_m, body) => { const result = demoControl('create', '', body); emitLifecycle('control_access.changed', '', { action: 'created' }); return result; }],
  ['DELETE', /^\/api\/v1\/control-accesses\/([^/]+)$/, (m, body) => { const result = demoControl('revoke', m[1], body); emitLifecycle('control_access.changed', '', { accessId: m[1], action: 'revoked' }); return result; }],
  ['GET', /^\/api\/v1\/me\/mcp$/, (m, body) => { const result = demoMCP('list', m[1] || '', body); return result; }],
  ['POST', /^\/api\/v1\/me\/mcp$/, (m, body) => { const result = demoMCP('create', m[1] || '', body); emitLifecycle('mcp.changed', '', {}); return result; }],
  ['PUT', /^\/api\/v1\/mcp\/([^/]+)$/, (m, body) => { const result = demoMCP('update', m[1] || '', body); emitLifecycle('mcp.changed', '', {}); return result; }],
  ['DELETE', /^\/api\/v1\/mcp\/([^/]+)$/, (m, body) => { const result = demoMCP('delete', m[1] || '', body); emitLifecycle('mcp.changed', '', {}); return result; }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/mcp$/, (m, body) => { const result = demoMCP('bindings', m[1] || '', body); return result; }],
  ['PUT', /^\/api\/v1\/projects\/([^/]+)\/mcp$/, (m, body) => { const result = demoMCP('bind', m[1] || '', body); emitLifecycle('mcp.changed', '', {}); return result; }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/mcp$/, (m, body) => { const result = demoMCP('restriction', m[1] || '', body); return result; }],
  ['PUT', /^\/api\/v1\/sessions\/([^/]+)\/mcp$/, (m, body) => { const result = demoMCP('restrict', m[1] || '', body); emitLifecycle('mcp.changed', '', {}); return result; }],
  ['POST', /^\/api\/v1\/backends\/([^/]+)\/mcp$/, (m, body) => { const result = demoMCP('operation', m[1] || '', body); return result; }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/review$/, (m) => { sessionOf(m[1]); return demoReview(m[1]); }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/review\/file$/, (m, _body, query) => { sessionOf(m[1]); return demoReviewFile(m[1], query); }],
  ['POST', /^\/api\/v1\/sessions\/([^/]+)\/review$/, (m, body) => { const session = sessionOf(m[1]); const review = demoReviewAction(m[1], body as unknown as ReviewAction); emitLifecycle('review.changed', text(session.projectId), { version: review.version, status: review.status, required: review.required }, m[1]); return review; }],
  ['PUT', /^\/api\/v1\/me\/default-backend$/, (_m, body) => {
    const id = body.backendInstanceId;
    if (id !== null && !state.backends.some((backend) => backend.id === id && backend.ownershipStatus === 'CLAIMED')) throw notFound('This backend');
    for (const backend of state.backends) backend.isDefault = backend.id === id;
    return undefined;
  }],
  ['GET', /^\/api\/v1\/me\/policy$/, () => defaultPolicyView(state.instancePolicy)],
  ['PUT', /^\/api\/v1\/me\/policy$/, (_m, body) => {
    state.instancePolicy = body.mode ? body : null;
    record('execution_policy.instance_set', { detail: body });
    return defaultPolicyView(state.instancePolicy);
  }],
  ['GET', /^\/api\/v1\/backends\/([^/]+)\/policy$/, (m) => defaultPolicyView(state.backendPolicies[m[1]] ?? null, state.instancePolicy)],
  ['PUT', /^\/api\/v1\/backends\/([^/]+)\/policy$/, (m, body) => {
    state.backendPolicies[m[1]] = body.mode ? body : null;
    record('execution_policy.backend_set', { subjectId: m[1], detail: body });
    return defaultPolicyView(state.backendPolicies[m[1]], state.instancePolicy);
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/policy\/origin$/, (m) => defaultPolicyView(state.projectPolicies[m[1]] ?? null, state.instancePolicy)],
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
    const name = text(body.name).trim();
    if (!name) throw new ApiError(400, 'invalid', 'Enter a project name.');
    const project = { id: newId(), ownerId: 'alex', name, description: text(body.description).trim(), instructions: '', status: 'ACTIVE', createdAt: nowIso(), updatedAt: nowIso() };
    state.projects.push(project);
    state.projectPolicies[project.id] = { mode: 'INTERACTIVE', allowFilesystemWrite: true, allowGitCommit: true, allowGitPush: false, allowNetwork: true };
    recordKnowledge('project.created', project.id, project.id, projectDetail(project));
    return project;
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)$/, (m) => state.projects.find((project) => project.id === m[1]) ?? (() => { throw notFound('This project'); })()],
  ['PATCH', /^\/api\/v1\/projects\/([^/]+)$/, (m, body) => {
    const project = state.projects.find((entry) => entry.id === m[1]);
    if (!project) throw notFound('This project');
    const changedFields = ['name', 'description', 'instructions'].filter((field) => text(body[field]).trim() && text(body[field]).trim() !== project[field]);
    if (!changedFields.length) return project;
    const previousName = project.name;
    for (const field of changedFields) project[field] = text(body[field]).trim();
    project.updatedAt = nowIso();
    recordKnowledge('project.updated', m[1], m[1], { ...projectDetail(project), changedFields, ...(changedFields.includes('name') && { previousName }) });
    return project;
  }],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/(archive|restore)$/, (m) => {
    const project = state.projects.find((entry) => entry.id === m[1]);
    if (!project) throw notFound('This project');
    const status = m[2] === 'archive' ? 'ARCHIVED' : 'ACTIVE';
    if (status === project.status) throw new ApiError(409, 'conflict', 'The project is already in that state.');
    project.status = status; project.updatedAt = nowIso();
    if (status === 'ARCHIVED') project.archivedAt = nowIso(); else delete project.archivedAt;
    recordKnowledge(m[2] === 'archive' ? 'project.archived' : 'project.restored', m[1], m[1], projectDetail(project));
    return project;
  }],
  ['DELETE', /^\/api\/v1\/projects\/([^/]+)$/, (m) => {
    const project = state.projects.find((entry) => entry.id === m[1]);
    if (!project) throw notFound('This project');
    const ids = new Set(state.sessions.filter((session) => session.projectId === m[1]).map((session) => String(session.id)));
    if ([...ids].some((id) => state.timelines[id]?.jobs.some((job) => !['COMPLETED', 'FAILED', 'CANCELLED'].includes(String(job.status))))) {
      throw new ApiError(409, 'conflict', 'This project has unfinished jobs; stop them before deleting it.');
    }
    state.projects = state.projects.filter((entry) => entry.id !== m[1]);
    state.sessions = state.sessions.filter((entry) => entry.projectId !== m[1]);
    state.tasks = state.tasks.filter((entry) => entry.projectId !== m[1]);
    state.decisions = state.decisions.filter((entry) => entry.projectId !== m[1]);
    state.artifacts = state.artifacts.filter((entry) => entry.projectId !== m[1]);
    state.skills = state.skills.filter((entry) => entry.projectId !== m[1]);
    const directoryIds = new Set(state.directories.filter((entry) => entry.projectId === m[1]).map((entry) => entry.id));
    state.directories = state.directories.filter((entry) => entry.projectId !== m[1]);
    state.bindings = state.bindings.filter((entry) => !directoryIds.has(entry.knownDirectoryId));
    state.schedules = state.schedules.filter((entry) => !ids.has(String(entry.sessionId)));
    for (const id of ids) { delete state.timelines[id]; delete state.sessionPolicies[id]; }
    delete state.projectPolicies[m[1]];
    recordKnowledge('project.deleted', m[1], m[1], projectDetail(project), true);
    return undefined;
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/sessions$/, (m, _b, query) =>
    list(
      state.sessions
        .filter((session) => session.projectId === m[1] && (query.get('includeArchived') === 'true' || session.status !== 'ARCHIVED'))
        .sort((a, b) => String(b.updatedAt).localeCompare(String(a.updatedAt))),
    )],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/directories$/, (m) => list(state.directories.filter((entry) => entry.projectId === m[1] || !entry.projectId))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/directories$/, (m, body) => createDirectory(body, m[1])],
  ['GET', /^\/api\/v1\/directories$/, () => list(state.directories.filter((entry) => !entry.projectId))],
  ['POST', /^\/api\/v1\/directories$/, (_m, body) => createDirectory(body)],
  ['POST', /^\/api\/v1\/directories\/([^/]+)\/global$/, (m) => {
    const directory = directoryOf(m[1]);
    if (!directory.projectId) return directory;
    if (state.directories.some((entry) => entry.id !== directory.id && !entry.projectId && entry.name === directory.name)) throw new ApiError(409, 'conflict', 'A global directory already has that name. Rename one first.');
    delete directory.projectId;
    directory.updatedAt = nowIso();
    return directory;
  }],
  ['PATCH', /^\/api\/v1\/directories\/([^/]+)$/, (m, body) => {
    const directory = directoryOf(m[1]);
    if (body.name !== undefined && !text(body.name).trim()) throw new ApiError(400, 'invalid', 'Enter a directory name.');
    const name = text(body.name, String(directory.name)).trim();
    if (state.directories.some((entry) => entry.id !== directory.id && entry.projectId === directory.projectId && entry.name === name)) throw new ApiError(409, 'conflict', 'This scope already has a directory with that name.');
    for (const key of ['name', 'description', 'gitRemote']) if (typeof body[key] === 'string') directory[key] = text(body[key]).trim();
    if (!directory.gitRemote) delete directory.gitRemote;
    directory.updatedAt = nowIso(); return directory;
  }],
  ['DELETE', /^\/api\/v1\/directories\/([^/]+)$/, (m) => {
    directoryOf(m[1]);
    if (state.sessions.some((session) => session.workingDirectoryId === m[1])) throw new ApiError(409, 'conflict', 'A session uses this working directory.');
    state.directories = state.directories.filter((entry) => entry.id !== m[1]);
    state.bindings = state.bindings.filter((entry) => entry.knownDirectoryId !== m[1]);
    return undefined;
  }],
  ['GET', /^\/api\/v1\/directories\/([^/]+)\/bindings$/, (m) => { directoryOf(m[1]); return list(state.bindings.filter((entry) => entry.knownDirectoryId === m[1])); }],
  ['POST', /^\/api\/v1\/directories\/([^/]+)\/bindings$/, (m, body) => {
    directoryOf(m[1]);
    if (!text(body.path).startsWith('/')) throw new ApiError(400, 'invalid', 'The binding path must be absolute.');
    if (!state.backends.some((backend) => backend.id === body.backendInstanceId && backend.ownershipStatus !== 'REVOKED')) throw notFound('This backend');
    const binding = state.bindings.find((entry) => entry.knownDirectoryId === m[1] && entry.backendInstanceId === body.backendInstanceId);
    if (binding) { binding.path = body.path; binding.updatedAt = nowIso(); return binding; }
    const created = { knownDirectoryId: m[1], backendInstanceId: body.backendInstanceId, path: body.path, createdAt: nowIso(), updatedAt: nowIso() }; state.bindings.push(created); return created;
  }],
  ['DELETE', /^\/api\/v1\/directories\/([^/]+)\/bindings\/([^/]+)$/, (m) => {
    directoryOf(m[1]); state.bindings = state.bindings.filter((entry) => entry.knownDirectoryId !== m[1] || entry.backendInstanceId !== m[2]); return undefined;
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/policy$/, (m) => defaultPolicyView(state.projectPolicies[m[1]] ?? null, state.instancePolicy).effective],
  ['PUT', /^\/api\/v1\/projects\/([^/]+)\/policy$/, (m, body) => {
    if (body.mode) state.projectPolicies[m[1]] = body;
    else delete state.projectPolicies[m[1]];
    record('execution_policy.set', { projectId: m[1], subjectId: m[1], detail: body });
    return defaultPolicyView(state.projectPolicies[m[1]] ?? null, state.instancePolicy).effective;
  }],

  ['GET', /^\/api\/v1\/projects\/([^/]+)\/tasks\/ready$/, (m) => list(ready(m[1]))],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/tasks$/, (m, _b, query) =>
    list(state.tasks.filter((task) => task.projectId === m[1] && (query.get('includeDone') === 'true' || task.status !== 'DONE')))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/tasks$/, (m, body) => {
    const title = text(body.title).trim();
    if (!title) throw new ApiError(400, 'invalid', 'Enter a task title.');
    if (!state.projects.some((entry) => entry.id === m[1])) throw notFound('This project');
    const dependencies = [...new Set((body.dependsOn as string[]) ?? [])];
    if (dependencies.some((id) => taskOf(id).projectId !== m[1])) throw new ApiError(400, 'invalid', 'Dependencies belong to the same project.');
    const task = { id: newId(), projectId: m[1], title, description: text(body.description).trim(), status: 'TODO', dependsOn: dependencies, createdAt: nowIso(), updatedAt: nowIso() };
    state.tasks.push(task);
    recordKnowledge('task.created', m[1], task.id, taskDetail(task));
    return task;
  }],
  ['PATCH', /^\/api\/v1\/tasks\/([^/]+)$/, (m, body) => {
    const task = taskOf(m[1]);
    if (body.status && !['TODO', 'IN_PROGRESS', 'DONE'].includes(text(body.status))) throw new ApiError(400, 'invalid', 'Unknown task status.');
    const changedFields = ['title', 'description', 'status'].filter((field) => text(body[field]).trim() && text(body[field]).trim() !== task[field]);
    if (!changedFields.length) return task;
    const previousStatus = task.status;
    for (const field of changedFields) task[field] = text(body[field]).trim();
    task.updatedAt = nowIso();
    if (body.status) task.completedAt = body.status === 'DONE' ? nowIso() : undefined;
    recordKnowledge('task.updated', text(task.projectId), task.id, { ...taskDetail(task), changedFields, ...(changedFields.includes('status') && { previousStatus }) });
    return task;
  }],
  ['DELETE', /^\/api\/v1\/tasks\/([^/]+)$/, (m) => {
    const removed = taskOf(m[1]);
    state.tasks = state.tasks.filter((task) => task.id !== m[1]);
    for (const task of state.tasks) task.dependsOn = ((task.dependsOn as string[]) ?? []).filter((other) => other !== m[1]);
    recordKnowledge('task.deleted', text(removed.projectId), removed.id, taskDetail(removed));
  }],
  ['POST', /^\/api\/v1\/tasks\/([^/]+)\/dependencies$/, (m, body) => {
    const task = taskOf(m[1]); const blocker = taskOf(text(body.dependsOn));
    if (blocker.projectId !== task.projectId) throw new ApiError(400, 'invalid', 'Dependencies belong to the same project.');
    const reaches = (id: string, seen = new Set<string>()): boolean => {
      if (id === m[1]) return true;
      if (seen.has(id)) return false;
      seen.add(id); return ((taskOf(id).dependsOn as string[]) ?? []).some((other) => reaches(other, seen));
    };
    if (reaches(text(blocker.id))) throw new ApiError(400, 'invalid', 'Dependencies cannot form a cycle.');
    if (((task.dependsOn as string[]) ?? []).includes(text(blocker.id))) return task;
    task.dependsOn = [...((task.dependsOn as string[]) ?? []), text(blocker.id)];
    recordKnowledge('task.updated', text(task.projectId), task.id, { ...taskDetail(task), changedFields: ['dependsOn'], dependencyAction: 'added', dependencyTaskId: blocker.id, dependencyTitle: blocker.title });
    return task;
  }],
  ['DELETE', /^\/api\/v1\/tasks\/([^/]+)\/dependencies\/([^/]+)$/, (m) => {
    const task = taskOf(m[1]);
    if (!((task.dependsOn as string[]) ?? []).includes(m[2])) return task;
    const blocker = taskOf(m[2]);
    task.dependsOn = ((task.dependsOn as string[]) ?? []).filter((other) => other !== m[2]);
    recordKnowledge('task.updated', text(task.projectId), task.id, { ...taskDetail(task), changedFields: ['dependsOn'], dependencyAction: 'removed', dependencyTaskId: blocker.id, dependencyTitle: blocker.title });
    return task;
  }],

  ['GET', /^\/api\/v1\/projects\/([^/]+)\/decisions$/, (m, _b, query) =>
    list(state.decisions.filter((entry) => entry.projectId === m[1] && (query.get('includeSuperseded') === 'true' || entry.status === 'ACTIVE')))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/decisions$/, (m, body) => recordDecision(body, m[1])],
  ['GET', /^\/api\/v1\/me\/decisions$/, (_m, _b, query) =>
    list(state.decisions.filter((entry) => !entry.projectId && (query.get('includeSuperseded') === 'true' || entry.status === 'ACTIVE')))],
  ['POST', /^\/api\/v1\/me\/decisions$/, (_m, body) => recordDecision(body)],
  ['GET', /^\/api\/v1\/me\/memory\/search$/, (_m, _b, query) => {
    const terms = (query.get('q') ?? '').toLowerCase().trim();
    if (!terms) throw new ApiError(400, 'invalid', 'Enter a search query.');
    return list(state.decisions.filter((entry) => !entry.projectId && entry.status === 'ACTIVE' && `${text(entry.title)} ${text(entry.content)}`.toLowerCase().includes(terms)));
  }],
  ['POST', /^\/api\/v1\/decisions\/([^/]+)\/global$/, (m) => {
    const decision = state.decisions.find((entry) => entry.id === m[1]);
    if (!decision) throw notFound('This decision');
    if (decision.status !== 'ACTIVE') throw new ApiError(409, 'conflict', 'This decision has already been replaced.');
    if (!decision.projectId) return decision;
    return recordDecision({ title: decision.title, content: decision.content, importance: decision.importance, supersedes: decision.id });
  }],
  ['PATCH', /^\/api\/v1\/decisions\/([^/]+)$/, (m, body) => {
    const decision = state.decisions.find((entry) => entry.id === m[1]);
    if (!decision) throw notFound('This decision');
    if (!['NORMAL', 'IMPORTANT'].includes(text(body.importance))) throw new ApiError(400, 'invalid', 'Unknown importance.');
    if (decision.importance === body.importance) return decision;
    const previousImportance = decision.importance;
    decision.importance = body.importance; decision.updatedAt = nowIso();
    recordKnowledge('decision.updated', text(decision.projectId) || undefined, decision.id, { ...decisionDetail(decision), previousImportance });
    return decision;
  }],
  ['DELETE', /^\/api\/v1\/decisions\/([^/]+)$/, (m) => {
    const removed = state.decisions.find((entry) => entry.id === m[1]);
    if (!removed) throw notFound('This decision');
    let revived: Json | undefined;
    if (removed.supersedes) {
      const previous = state.decisions.find((entry) => entry.id === removed.supersedes);
      if (previous && previous.projectId === removed.projectId && previous.status === 'SUPERSEDED') { previous.status = 'ACTIVE'; previous.updatedAt = nowIso(); revived = previous; }
    }
    state.decisions = state.decisions.filter((entry) => entry.id !== m[1]);
    for (const entry of state.decisions) if (entry.supersedes === m[1]) delete entry.supersedes;
    recordKnowledge('decision.deleted', text(removed.projectId) || undefined, removed.id, decisionDetail(removed));
    if (revived) recordKnowledge('decision.updated', text(revived.projectId) || undefined, revived.id, { ...decisionDetail(revived), restored: true });
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/search$/, () => ({ tasks: [], decisions: [], history: [] })],

  ['GET', /^\/api\/v1\/projects\/([^/]+)\/artifacts$/, (m, _b, query) =>
    list(state.artifacts.filter((entry) => entry.projectId === m[1] && (!query.get('sessionId') || entry.sessionId === query.get('sessionId'))))],
  ['DELETE', /^\/api\/v1\/artifacts\/([^/]+)$/, (m) => {
    state.artifacts = state.artifacts.filter((entry) => entry.id !== m[1]);
  }],
  ['GET', /^\/api\/v1\/projects\/([^/]+)\/skills$/, (m) => list(state.skills.filter((entry) => entry.projectId === m[1]))],
  ['POST', /^\/api\/v1\/projects\/([^/]+)\/skills$/, () => { throw unavailable('Installing a skill'); }],
  ['GET', /^\/api\/v1\/me\/skills$/, () => list(state.skills.filter((entry) => !entry.projectId))],
  ['POST', /^\/api\/v1\/me\/skills$/, () => { throw unavailable('Installing a skill'); }],
  ['PUT', /^\/api\/v1\/projects\/([^/]+)\/skills\/([^/]+)\/activation$/, (m, body) => {
    const skill = state.skills.find((entry) => entry.id === m[2] && !entry.projectId);
    if (!skill) throw new ApiError(400, 'invalid', 'Only a global skill can be turned off in a project.');
    const others = ((skill.disabledProjectIds as string[] | undefined) ?? []).filter((id) => id !== m[1]);
    skill.disabledProjectIds = body.enabled ? others : [...others, m[1]];
    const project = state.projects.find((entry) => entry.id === m[1]);
    const detail = { skillId: skill.id, name: skill.name, action: body.enabled ? 'enabled' : 'disabled', projectName: project?.name };
    record('skill.changed', { projectId: m[1], subjectId: skill.id, detail });
    emitLifecycle('skill.changed', m[1], detail);
  }],
  ['POST', /^\/api\/v1\/skills\/([^/]+)\/global$/, (m) => {
    const skill = state.skills.find((entry) => entry.id === m[1]);
    if (!skill) throw notFound('This skill');
    if (!skill.projectId) return skill;
    if (state.skills.some((entry) => !entry.projectId && entry.name === skill.name)) {
      throw new ApiError(409, 'conflict', `A global skill is already named ${String(skill.name)}; uninstall one first.`);
    }
    const previousProjectId = skill.projectId;
    delete skill.projectId;
    skill.updatedAt = nowIso();
    const detail = { skillId: skill.id, name: skill.name, action: 'made_global', previousProjectId };
    record('skill.changed', { subjectId: skill.id, detail });
    emitLifecycle('skill.changed', undefined, detail);
    return skill;
  }],
  ['DELETE', /^\/api\/v1\/skills\/([^/]+)$/, (m) => {
    const skill = state.skills.find((entry) => entry.id === m[1]);
    if (!skill) throw notFound('This skill');
    state.skills = state.skills.filter((entry) => entry.id !== m[1]);
    const detail = { skillId: skill.id, name: skill.name, installedRevision: skill.installedRevision };
    // A global skill belongs to no project: its events are the owner's.
    const projectId = typeof skill.projectId === 'string' ? skill.projectId : undefined;
    record('skill.removed', { ...(projectId && { projectId }), subjectId: skill.id, detail });
    emitLifecycle('skill.removed', projectId, detail);
  }],
  ['GET', /^\/api\/v1\/skills\/([^/]+)\/files$/, (m) => list(data.skillFiles[m[1]] ?? [])],

  ['GET', /^\/api\/v1\/backends$/, () => list(state.backends)],
  ['GET', /^\/api\/v1\/backends\/([^/]+)\/skills$/, () => list([])],
  ['POST', /^\/api\/v1\/backends\/([^/]+)\/revoke$/, (m) => {
    const backend = state.backends.find((entry) => entry.id === m[1]);
    if (backend) { backend.ownershipStatus = 'REVOKED'; backend.isDefault = false; }
  }],
  ['POST', /^\/api\/v1\/backends\/claim$/, () => { throw unavailable('Claiming a backend'); }],
  ['POST', /^\/api\/v1\/backend-tokens$/, () => ({ token: 'demo-token-not-valid-anywhere', expiresAt: new Date(Date.now() + 3_600_000).toISOString() })],

  ['POST', /^\/api\/v1\/sessions\/start$/, (_m, body) => {
    const project = state.projects.find((entry) => entry.id === body.projectId) ?? state.projects[0];
    const sessionId = newId();
    const message = text(body.message);
    state.sessions.push({ id: sessionId, projectId: project.id, model: text(body.model), effort: text(body.effort), ...(typeof body.workingDirectoryId === 'string' && { workingDirectoryId: body.workingDirectoryId }), title: message.length > 60 ? `${message.slice(0, 60)}…` : message || 'New session', status: 'ACTIVE', createdAt: nowIso(), updatedAt: nowIso() });
    state.timelines[sessionId] = {
      runs: [{ id: newId(), sessionId, backendInstanceId: body.backendInstanceId ?? data.IDS.laptop, resumeStatus: 'AVAILABLE', createdAt: nowIso(), updatedAt: nowIso() }],
      jobs: [],
      events: [],
    };
    pushEvent(sessionId, 'session.created', { title: sessionOf(sessionId).title });
    const job = answer(sessionId, message, undefined, body.artifactIds as string[] | undefined);
    recordSessionLifecycle(sessionOf(sessionId), 'session.created', { title: sessionOf(sessionId).title, backendInstanceId: body.backendInstanceId ?? data.IDS.laptop });
    return { session: sessionOf(sessionId), run: state.timelines[sessionId].runs[0], job };
  }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)$/, (m) => snapshot(m[1])],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/handoff$/, (m, _body, query) => previewHandoff(m[1], query.get('backendInstanceId') ?? '')],
  ['PATCH', /^\/api\/v1\/sessions\/([^/]+)$/, (m, body) => {
    const session = sessionOf(m[1]);
    if (body.backendInstanceId && ['model', 'effort', 'title', 'pinned', 'workingDirectoryId'].some((key) => body[key] !== undefined)) throw new ApiError(400, 'invalid', 'Change the backend separately from other session settings.');
    if (body.backendInstanceId) {
      const preview = previewHandoff(m[1], text(body.backendInstanceId));
      if (body.expectedRunId && body.expectedRunId !== preview.fromRunId) throw new ApiError(409, 'conflict', 'The session moved since this preview; review the destination again.');
      if (!preview.canMove) throw new ApiError(409, 'conflict', preview.reasons.join(' '));
    }
    if (body.model !== undefined || body.effort !== undefined) {
      if (body.backendInstanceId) throw new ApiError(400, 'invalid', 'Change the backend and model in separate requests.');
      modelSettings(m[1], body.model as string | undefined, body.effort as string | undefined);
      if (body.model !== undefined) session.model = body.model;
      if (body.effort !== undefined) session.effort = body.effort;
      session.updatedAt = nowIso();
      pushEvent(m[1], 'session.model_changed', { model: session.model || '', effort: session.effort || '' });
      record('session.model_changed', { projectId: session.projectId, sessionId: m[1], subjectId: m[1], detail: { model: session.model || '', effort: session.effort || '' } });
    }
    if (body.workingDirectoryId !== undefined) {
      if (body.workingDirectoryId !== null && typeof body.workingDirectoryId !== 'string') throw new ApiError(400, 'invalid', 'workingDirectoryId must be a string or null.');
      const id = text(body.workingDirectoryId);
      if (id) {
        const directory = directoryOf(id);
        if (directory.projectId && directory.projectId !== session.projectId) throw new ApiError(400, 'invalid', 'The directory belongs to another project.');
      }
      const before = session.workingDirectoryId;
      if (id) session.workingDirectoryId = id;
      else delete session.workingDirectoryId;
      if (before !== session.workingDirectoryId) {
        session.updatedAt = nowIso();
        pushEvent(m[1], 'working_directory.changed', id ? { knownDirectoryId: id } : {});
        record('session.working_directory_changed', { projectId: session.projectId, sessionId: m[1], subjectId: m[1], detail: { fromKnownDirectoryId: before || null, knownDirectoryId: id || null, effectiveFrom: 'next_job' } });
      }
    }
    if (body.title !== undefined) session.title = body.title;
    if (body.pinned === true) session.pinnedAt ??= nowIso();
    if (body.pinned === false) delete session.pinnedAt;
    if (body.backendInstanceId) {
      const timeline = timelineOf(m[1]);
      const from = timeline.runs.at(-1)?.backendInstanceId;
      const runEntry = { id: newId(), sessionId: m[1], backendInstanceId: body.backendInstanceId, resumeStatus: 'UNKNOWN', createdAt: nowIso(), updatedAt: nowIso() };
      timeline.runs.push(runEntry);
      const moved = { backendInstanceId: body.backendInstanceId, fromBackendInstanceId: from, backendName: previewHandoff(m[1], text(body.backendInstanceId)).backend.name };
      pushEvent(m[1], 'run.created', moved);
      emitLifecycle('run.created', text(session.projectId), moved, m[1]);
      record('session.backend_changed', { projectId: session.projectId, sessionId: m[1], subjectId: body.backendInstanceId, detail: { from, to: body.backendInstanceId, missingSkills: [] } });
      return { run: runEntry, missingSkills: [] };
    }
    return session;
  }],
  ['POST', /^\/api\/v1\/sessions\/([^/]+)\/(archive|restore)$/, (m) => {
    const session = sessionOf(m[1]);
    const status = m[2] === 'archive' ? 'ARCHIVED' : 'ACTIVE';
    if (session.status === status) throw new ApiError(409, 'conflict', 'The session already has this status.');
    const fromStatus = session.status;
    session.status = status;
    session.updatedAt = nowIso();
    const action = m[2] === 'archive' ? 'session.archived' : 'session.restored';
    pushEvent(m[1], action, {});
    recordSessionLifecycle(session, action, { title: session.title, fromStatus, status });
    return session;
  }],
  ['DELETE', /^\/api\/v1\/sessions\/([^/]+)$/, (m, _b, query) => {
    const session = sessionOf(m[1]);
    const busy = state.timelines[m[1]]?.jobs.some((job) => !['COMPLETED', 'FAILED', 'CANCELLED'].includes(String(job.status)));
    if (busy) throw new ApiError(409, 'conflict', 'Stop the unfinished jobs before deleting this session.');
    state.sessions = state.sessions.filter((session) => session.id !== m[1]);
    // Its files go with it when asked; otherwise they stay, attached to nothing.
    if (query.get('artifacts') === 'delete') state.artifacts = state.artifacts.filter((entry) => entry.sessionId !== m[1]);
    else for (const entry of state.artifacts) if (entry.sessionId === m[1]) delete entry.sessionId;
    state.schedules = state.schedules.filter((entry) => entry.sessionId !== m[1]);
    delete state.timelines[m[1]];
    recordSessionLifecycle(session, 'session.deleted', { sessionId: session.id, title: session.title, artifactsDeleted: query.get('artifacts') === 'delete' });
  }],
  ['GET', /^\/api\/v1\/sessions\/([^/]+)\/events$/, () => list([])],
  ['POST', /^\/api\/v1\/sessions\/([^/]+)\/messages$/, (m, body) => answer(m[1], text(body.message), body.delivery as string | undefined, body.artifactIds as string[] | undefined)],
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
    const schedule = { id: newId(), sessionId: m[1], cron: body.cron, timezone: body.timezone ?? 'Europe/Paris', message: body.message, enabled: body.enabled ?? true, upcoming: [], createdAt: nowIso(), updatedAt: nowIso() };
    state.schedules.push(schedule);
    recordScheduleChange(schedule, 'schedule.created');
    return schedule;
  }],
  ['PATCH', /^\/api\/v1\/schedules\/([^/]+)$/, (m, body) => {
    const schedule = state.schedules.find((entry) => entry.id === m[1]);
    if (!schedule) throw notFound('This schedule');
    Object.assign(schedule, body, { updatedAt: nowIso() });
    if (body.enabled === false) delete schedule.nextRunAt;
    recordScheduleChange(schedule, 'schedule.updated');
    return schedule;
  }],
  ['DELETE', /^\/api\/v1\/schedules\/([^/]+)$/, (m) => {
    const schedule = state.schedules.find((entry) => entry.id === m[1]);
    if (!schedule) throw notFound('This schedule');
    recordScheduleChange(schedule, 'schedule.deleted');
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

/** Play a backend reconnecting, using the same event path as a real Core. */
export function demoBackendLifecycle(receive: (event: Event) => void): () => void {
  const backend = state.backends.find((entry) => entry.id === data.IDS.ci);
  if (!backend) return () => {};
  const initial = clone(backend);
  const auditIds = new Set<string>();
  const statuses = ['OFFLINE', 'STARTING', 'READY', 'DEGRADED'] as const;
  let stage = 0;
  const timer = setInterval(() => {
    if (!state.backends.includes(backend)) return;
    const operationalStatus = statuses[stage++ % statuses.length];
    const providerAuthState = operationalStatus === 'READY' ? 'AUTHENTICATED' : initial.providerAuthState;
    const from = backend.operationalStatus;
    Object.assign(backend, { operationalStatus, providerAuthState, updatedAt: nowIso(),
      conditions: operationalStatus === 'READY' ? [] : clone(initial.conditions),
    });
    if (operationalStatus !== 'OFFLINE') backend.lastHeartbeatAt = nowIso();
    if (from !== operationalStatus && operationalStatus !== 'STARTING') {
      const id = newId();
      auditIds.add(id);
      record('backend.availability_changed', { id, actorId: operationalStatus === 'OFFLINE' ? 'core' : `backend:${String(backend.id)}`, channel: operationalStatus === 'OFFLINE' ? 'core' : 'backend', subjectId: backend.id,
        detail: { name: backend.name, from, to: operationalStatus, providerAuthState } });
    }
    receive({ id: newId(), sequence: sequence++, timestamp: nowIso(), type: 'backend.status_changed',
      payload: { backendInstanceId: String(backend.id), operationalStatus, providerAuthState },
    });
  }, 8_000);
  return () => {
    clearInterval(timer);
    state.audit = state.audit.filter((entry) => !auditIds.has(String(entry.id)));
    if (state.backends.includes(backend)) Object.assign(backend, initial);
  };
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
