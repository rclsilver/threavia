import { describe, expect, it } from 'vitest';

import type { Project, Repository, Session, UserInputRequest, ValidationRequest } from '../src/api/types';
import {
  orderProjects,
  orderSessions,
  pinnedDescription,
  repositoryDescription,
  repositoryLines,
  sessionState,
  stateIcon,
  waitingBySession,
  waitingCount,
  waitingItems,
} from '../src/tree/model';

const session = (id: string, updatedAt: string, extra: Partial<Session> = {}): Session => ({
  id,
  projectId: 'p1',
  title: id,
  status: 'ACTIVE',
  createdAt: updatedAt,
  updatedAt,
  ...extra,
});

const project = (id: string, name: string, extra: Partial<Project> = {}): Project => ({
  id,
  ownerId: 'u',
  name,
  status: 'ACTIVE',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  ...extra,
});

const scope = (sessionId: string) => ({ projectId: 'p1', sessionId, runId: 'r', jobId: 'j' });

const validation = (id: string, sessionId: string, createdAt: string, payload: object): ValidationRequest => ({
  id,
  scope: scope(sessionId),
  status: 'PENDING',
  title: 'Run a command',
  requestPayload: payload as ValidationRequest['requestPayload'],
  payloadSha256: 'x',
  createdAt,
  notify: true,
  context: { sessionTitle: `Title of ${sessionId}`, projectName: 'Infra', backendName: 'laptop', directory: 'puppet' },
});

const question = (id: string, sessionId: string, createdAt: string): UserInputRequest => ({
  id,
  scope: scope(sessionId),
  status: 'PENDING',
  prompt: 'Which branch?\nThe second line is detail.',
  choices: ['main', 'dev'],
  freeText: true,
  createdAt,
  notify: true,
});

describe('session state', () => {
  const pending = {
    validations: [validation('v1', 's1', '2026-10-09T10:00:00Z', { tool: 'Bash', input: { command: 'ls' } })],
    userInputs: [question('u1', 's1', '2026-10-09T09:00:00Z'), question('u2', 's2', '2026-10-09T11:00:00Z')],
  };
  const waiting = waitingBySession(pending);

  it('lets a permission outrank a question in the same session', () => {
    expect(waiting.get('s1')).toBe('validation');
    expect(waiting.get('s2')).toBe('input');
    expect(waitingCount(pending)).toBe(3);
  });

  it('reads the rest from the active job', () => {
    const at = '2026-10-09T00:00:00Z';
    expect(sessionState(session('s3', at, { activeJobStatus: 'QUEUED' }), waiting)).toBe('queued');
    expect(sessionState(session('s3', at, { activeJobStatus: 'RUNNING' }), waiting)).toBe('running');
    expect(sessionState(session('s3', at, { activeJobStatus: 'WAITING_BACKEND' }), waiting)).toBe('running');
    expect(sessionState(session('s3', at, { activeJobStatus: 'CANCELLING' }), waiting)).toBe('stopping');
    expect(sessionState(session('s3', at), waiting)).toBe('idle');
    expect(sessionState(session('s1', at, { activeJobStatus: 'RUNNING' }), waiting)).toBe('validation');
  });

  it('makes only the waiting states loud', () => {
    expect(stateIcon('validation')).toMatchObject({ id: 'shield', color: 'list.warningForeground' });
    expect(stateIcon('input').color).toBe('list.warningForeground');
    expect(stateIcon('running')).toMatchObject({ id: 'loading~spin' });
    expect(stateIcon('running').color).toBeUndefined();
    expect(stateIcon('idle').color).toBeUndefined();
  });

  it('lists what waits first, then the most recent, and leaves archived sessions out', () => {
    const ordered = orderSessions(
      [
        session('old', '2026-01-01T00:00:00Z'),
        session('new', '2026-10-09T00:00:00Z'),
        session('asks', '2025-01-01T00:00:00Z'),
        session('gone', '2026-10-09T00:00:00Z', { status: 'ARCHIVED' }),
        session('bad', 'not a date'),
      ],
      new Map([['asks', 'input' as const]]),
    );
    expect(ordered.map((s) => s.id)).toEqual(['asks', 'new', 'old', 'bad']);
  });
});

describe('projects', () => {
  const projects = [project('p1', 'Infra'), project('p2', 'Web'), project('p3', 'Old', { status: 'ARCHIVED' })];

  it('puts the workspace project first, named by name or id', () => {
    expect(orderProjects(projects, 'web').map((p) => p.id)).toEqual(['p2', 'p1']);
    expect(orderProjects(projects, 'p1').map((p) => p.id)).toEqual(['p1', 'p2']);
    expect(orderProjects(projects, '').map((p) => p.id)).toEqual(['p1', 'p2']);
  });

  it('names the project of a pinned session only when it is not the workspace one', () => {
    const pinned = session('s', '2026-10-09T00:00:00Z', { projectId: 'p2' });
    expect(pinnedDescription(pinned, projects, 'Web')).toBeUndefined();
    expect(pinnedDescription(pinned, projects, 'Infra')).toBe('Web');
    expect(pinnedDescription(pinned, projects, '')).toBe('Web');
  });
});

describe('waiting items', () => {
  it('says what each request asks, oldest first, and which session asks it', () => {
    const items = waitingItems({
      validations: [
        validation('v1', 's1', '2026-10-09T10:00:00Z', { tool: 'Bash', input: { command: 'git push\nmore' } }),
        validation('v2', 's1', '2026-10-09T08:00:00Z', { tool: 'mcp__threavia__task_create', input: {} }),
      ],
      userInputs: [question('u1', 's2', '2026-10-09T09:00:00Z')],
    });
    expect(items.map((item) => [item.id, item.label])).toEqual([
      ['v2', 'Run a command'],
      ['u1', 'Which branch?'],
      ['v1', 'Bash: git push'],
    ]);
    expect(items[2].description).toBe('Title of s1');
    expect(items[2].detail).toContain('Infra · Title of s1');
    expect(items[1].description).toBe('Untitled session');
    expect(items[1].detail).toContain('Choices: main, dev');
  });
});

describe('repository', () => {
  const repo = (extra: Partial<Repository>): Repository => ({
    directory: '/src/puppet',
    tracked: true,
    branch: 'main',
    upstream: 'origin/main',
    ahead: 0,
    behind: 0,
    upstreamGone: false,
    staged: 0,
    unstaged: 0,
    untracked: 0,
    conflicted: 0,
    checkedAt: '2026-10-09T00:00:00Z',
    ...extra,
  });

  it('fits in a row', () => {
    expect(repositoryDescription(repo({}))).toBe('main');
    expect(repositoryDescription(repo({ ahead: 1, behind: 2, unstaged: 3 }))).toBe('main ↑1 ↓2 ●');
    expect(repositoryDescription(repo({ conflicted: 1 }))).toBe('main ⚠');
    expect(repositoryDescription(repo({ upstreamGone: true, ahead: 4 }))).toBe('main');
    expect(repositoryDescription(repo({ tracked: false }))).toBeUndefined();
  });

  it('says each part in words in the tooltip', () => {
    const now = Date.parse('2026-10-09T01:00:00Z');
    const lines = repositoryLines(repo({ ahead: 1, staged: 2, fetchedAt: '2026-10-09T00:55:00Z' }), now);
    expect(lines).toEqual([
      'Branch main',
      '1 to push (origin/main)',
      '2 staged',
      'Fetched 5 min ago',
      '/src/puppet',
    ]);
    expect(repositoryLines(repo({ upstream: undefined }), now)[1]).toBe('Tracks nothing');
  });
});
