import { describe, expect, it } from 'vitest';

import { CoreClient } from '../src/api/client';
import { effectsOf } from '../src/api/events';
import type { Decision, Event, Project, Task } from '../src/api/types';
import {
  blockersOf,
  decisionMarkdown,
  dependencyChanges,
  dependencyOptions,
  documentName,
  groupDecisions,
  groupTasks,
  newDecisionBody,
  newTaskBody,
  nextStep,
  replacementOf,
  resolveCurrentProject,
  standingOf,
  taskEdit,
  taskMarkdown,
  toggledImportance,
  wouldCycle,
} from '../src/knowledge/model';

const task = (id: string, extra: Partial<Task> = {}): Task => ({
  id,
  projectId: 'p1',
  title: id,
  status: 'TODO',
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  ...extra,
});

const decision = (id: string, extra: Partial<Decision> = {}): Decision => ({
  id,
  projectId: 'p1',
  title: id,
  importance: 'NORMAL',
  status: 'ACTIVE',
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  ...extra,
});

const project = (id: string, name: string, extra: Partial<Project> = {}): Project => ({
  id,
  ownerId: 'u',
  name,
  status: 'ACTIVE',
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  ...extra,
});

describe('the current Project', () => {
  const projects = [project('p1', 'threavia'), project('p2', 'website'), project('p3', 'old', { status: 'ARCHIVED' })];

  it('is the one last followed, whatever the setting says', () => {
    expect(resolveCurrentProject(projects, 'threavia', 'p2')?.id).toBe('p2');
  });

  it('is the one the setting names, by name or id, until something is followed', () => {
    expect(resolveCurrentProject(projects, 'Website', undefined)?.id).toBe('p2');
    expect(resolveCurrentProject(projects, 'p2', undefined)?.id).toBe('p2');
  });

  it('falls back when what was followed is gone, then on the first active Project', () => {
    expect(resolveCurrentProject(projects, 'website', 'deleted')?.id).toBe('p2');
    expect(resolveCurrentProject(projects, '', undefined)?.id).toBe('p1');
    expect(resolveCurrentProject([projects[2]], '', undefined)).toBeUndefined();
    expect(resolveCurrentProject([], 'threavia', 'p1')).toBeUndefined();
  });
});

describe('the Tasks view', () => {
  const tasks = [
    task('a', { status: 'DONE' }),
    task('b'),
    task('c', { dependsOn: ['b'] }),
    task('d', { status: 'IN_PROGRESS' }),
    task('e', { dependsOn: ['a'] }),
  ];
  const ready = new Set(['b', 'e']);

  it('reads ready from Core, not from the graph', () => {
    expect(standingOf(tasks[1], ready)).toBe('READY');
    expect(standingOf(tasks[2], ready)).toBe('WAITING');
    expect(standingOf(task('x'), new Set())).toBe('WAITING');
  });

  it('groups like the web page, keeping Core’s order, Done always last', () => {
    expect(groupTasks(tasks, ready).map((group) => [group.title, group.tasks.map((entry) => entry.id)])).toEqual([
      ['In progress', ['d']],
      ['Ready to start', ['b', 'e']],
      ['Waiting on other work', ['c']],
      ['Done', ['a']],
    ]);
  });

  it('leaves empty open groups out, but keeps Done to say there is none', () => {
    expect(groupTasks([task('b')], new Set(['b'])).map((group) => group.standing)).toEqual(['READY', 'DONE']);
    expect(groupTasks([], new Set()).map((group) => group.tasks.length)).toEqual([0]);
  });

  it('offers one next step per standing', () => {
    expect(nextStep('READY')).toEqual({ status: 'IN_PROGRESS', label: 'Start' });
    expect(nextStep('IN_PROGRESS')).toEqual({ status: 'DONE', label: 'Mark done' });
    expect(nextStep('DONE')).toEqual({ status: 'TODO', label: 'Reopen' });
    expect(nextStep('WAITING')).toBeUndefined();
  });

  it('names what holds a Task back, leaving out what is done', () => {
    const waiting = task('w', { dependsOn: ['a', 'b', 'elsewhere'] });
    expect(blockersOf(waiting, tasks)).toEqual(['b', 'a task outside this project']);
  });

  it('never offers a dependency that closes a loop', () => {
    const chain = [task('x', { dependsOn: ['y'] }), task('y', { dependsOn: ['z'] }), task('z')];
    expect(wouldCycle(chain, chain[2], chain[0])).toBe(true);
    expect(wouldCycle(chain, chain[0], chain[2])).toBe(false);
    expect(dependencyOptions(chain, chain[2]).map((entry) => entry.id)).toEqual([]);
  });

  it('offers the other open Tasks, and keeps what is already waited on so it can be unticked', () => {
    expect(dependencyOptions(tasks, tasks[4]).map((entry) => entry.id)).toEqual(['a', 'b', 'c', 'd']);
    expect(dependencyOptions(tasks).map((entry) => entry.id)).toEqual(['b', 'c', 'd', 'e']);
  });

  it('turns the ticks into the edges to add and remove', () => {
    expect(dependencyChanges(['a', 'b'], ['b', 'c'])).toEqual({ add: ['c'], remove: ['a'] });
    expect(dependencyChanges(['a'], ['a'])).toEqual({ add: [], remove: [] });
  });

  it('sends what the web form sends', () => {
    expect(newTaskBody('  Ship it ', '', ['b'])).toEqual({ title: 'Ship it', description: '', dependsOn: ['b'] });
  });

  it('patches only a field that changed, and never empties one', () => {
    const current = task('t', { title: 'Old', description: 'Why' });
    expect(taskEdit(current, 'title', ' New ')).toEqual({ title: 'New' });
    expect(taskEdit(current, 'title', 'Old')).toBeUndefined();
    expect(taskEdit(current, 'description', '   ')).toBeUndefined();
    expect(taskEdit(current, 'description', 'Because')).toEqual({ description: 'Because' });
  });

  it('previews a Task with where it stands and what it waits on', () => {
    const text = taskMarkdown(task('w', { title: 'Walls', description: 'Paint', dependsOn: ['a', 'b'] }), tasks, ready);
    expect(text).toContain('# Walls');
    expect(text).toContain('**Waiting on other work**');
    expect(text).toContain('Paint');
    expect(text).toContain('- ✓ a (done)');
    expect(text).toContain('- b');
  });
});

describe('the Memory view', () => {
  const decisions = [
    decision('n1'),
    decision('i1', { importance: 'IMPORTANT', createdByJobId: 'j1' }),
    decision('old', { status: 'SUPERSEDED', importance: 'IMPORTANT' }),
    decision('new', { supersedes: 'old' }),
    decision('i2', { importance: 'IMPORTANT' }),
  ];

  it('lists what travels with every Job, then what is on record, then what was superseded', () => {
    expect(groupDecisions(decisions).map((group) => [group.title, group.decisions.map((entry) => entry.id)])).toEqual([
      ['Travels with every Job', ['i1', 'i2']],
      ['On record', ['n1', 'new']],
      ['Superseded', ['old']],
    ]);
  });

  it('leaves empty groups out', () => {
    expect(groupDecisions([decision('n1')]).map((group) => group.kind)).toEqual(['NORMAL']);
    expect(groupDecisions([])).toEqual([]);
  });

  it('toggles importance with the pin', () => {
    expect(toggledImportance(decisions[0])).toBe('IMPORTANT');
    expect(toggledImportance(decisions[1])).toBe('NORMAL');
  });

  it('finds what replaced a superseded decision', () => {
    expect(replacementOf(decisions[2], decisions)?.id).toBe('new');
    expect(replacementOf(decisions[0], decisions)).toBeUndefined();
  });

  it('sends what the web form sends, and names what it supersedes only when it does', () => {
    expect(newDecisionBody(' Use Go ', ' Fast ', 'NORMAL')).toEqual({ title: 'Use Go', content: 'Fast', importance: 'NORMAL' });
    expect(newDecisionBody('Use Rust', '', 'IMPORTANT', 'd1')).toEqual({
      title: 'Use Rust',
      content: '',
      importance: 'IMPORTANT',
      supersedes: 'd1',
    });
  });

  it('previews a decision with who recorded it and what it replaced', () => {
    const text = decisionMarkdown(decisions[1], decisions);
    expect(text).toContain('# i1');
    expect(text).toContain('Travels with every Job · recorded by an agent');
    expect(decisionMarkdown(decisions[3], decisions)).toContain('Supersedes **old**.');
    expect(decisionMarkdown(decisions[2], decisions)).toContain('Superseded by **new**.');
  });

  it('names a preview after its title, without what a path cannot hold', () => {
    expect(documentName('Use a/b: #1?')).toBe('Use a b 1.md');
    expect(documentName('  ')).toBe('Untitled.md');
  });
});

describe('project memory events', () => {
  const event = (type: string, projectId?: string): Event => ({
    id: 'e',
    sequence: 1,
    timestamp: new Date().toISOString(),
    type,
    projectId,
  });

  it('refresh the Tasks of the Project they name', () => {
    for (const type of ['task.created', 'task.updated', 'task.deleted']) {
      expect(effectsOf(event(type, 'p1'))).toEqual([{ kind: 'tasks', projectId: 'p1' }]);
    }
  });

  it('refresh the Decisions of the Project they name', () => {
    for (const type of ['decision.created', 'decision.superseded', 'decision.updated', 'decision.deleted']) {
      expect(effectsOf(event(type, 'p1'))).toEqual([{ kind: 'decisions', projectId: 'p1' }]);
    }
  });
});

describe('the project memory routes', () => {
  function recording() {
    const calls: { method: string; url: string; body?: unknown }[] = [];
    const client = new CoreClient({
      baseUrl: () => 'http://core',
      identity: { clientId: 'c', clientName: () => 'test' },
      authorization: () => Promise.resolve(undefined),
      fetch: ((url: string, init?: RequestInit) => {
        calls.push({
          method: init?.method ?? 'GET',
          url,
          body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
        });
        const status = init?.method === 'DELETE' && !url.includes('/dependencies/') ? 204 : 200;
        return Promise.resolve(new Response(status === 204 ? null : '{"items":[]}', { status }));
      }) as typeof fetch,
    });
    return { client, calls };
  }

  it('asks for what the views show', async () => {
    const { client, calls } = recording();
    await client.tasks('p1', true);
    await client.readyTasks('p1');
    await client.decisions('p1', true);
    await client.decisions('p1', false);
    expect(calls.map((call) => `${call.method} ${call.url}`)).toEqual([
      'GET http://core/api/v1/projects/p1/tasks?includeDone=true',
      'GET http://core/api/v1/projects/p1/tasks/ready',
      'GET http://core/api/v1/projects/p1/decisions?includeSuperseded=true',
      'GET http://core/api/v1/projects/p1/decisions',
    ]);
  });

  it('changes Tasks and Decisions where and as the web client does', async () => {
    const { client, calls } = recording();
    await client.createTask('p1', newTaskBody('T', 'D', ['t2']));
    await client.updateTask('t1', { status: 'DONE' });
    await client.addTaskDependency('t1', 't2');
    await client.removeTaskDependency('t1', 't2');
    await client.deleteTask('t1');
    await client.createDecision('p1', newDecisionBody('X', 'Y', 'IMPORTANT', 'd0'));
    await client.setDecisionImportance('d1', 'NORMAL');
    await client.deleteDecision('d1');
    expect(calls.map((call) => [call.method, call.url.replace('http://core/api/v1', ''), call.body])).toEqual([
      ['POST', '/projects/p1/tasks', { title: 'T', description: 'D', dependsOn: ['t2'] }],
      ['PATCH', '/tasks/t1', { status: 'DONE' }],
      ['POST', '/tasks/t1/dependencies', { dependsOn: 't2' }],
      ['DELETE', '/tasks/t1/dependencies/t2', undefined],
      ['DELETE', '/tasks/t1', undefined],
      ['POST', '/projects/p1/decisions', { title: 'X', content: 'Y', importance: 'IMPORTANT', supersedes: 'd0' }],
      ['PATCH', '/decisions/d1', { importance: 'NORMAL' }],
      ['DELETE', '/decisions/d1', undefined],
    ]);
  });
});
