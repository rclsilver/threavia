import { expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import { EventStream } from '../../../web/ui/src/api/stream';
import { demoLifecycleEvents, demoRequest } from '../../../web/ui/src/demo/server';
import { knowledgeAudit } from '../../../web/ui/src/lib/knowledge-audit';
import type { AuditEntry } from '../../../web/ui/src/api/types';
import type { Event } from '../src/api/types';
import { effectsOf } from '../src/api/events';

const event = (type: string, sequence = 1): Event => ({ id: `${type}-${sequence}`, sequence, type, timestamp: new Date().toISOString(), projectId: 'project', payload: {} });

it('refreshes audit and project knowledge after changes from another client', () => {
  const queries = new QueryClient();
  const stream = new EventStream(queries);
  for (const type of ['project.created', 'project.updated', 'project.archived', 'project.restored', 'project.deleted', 'task.created', 'task.updated', 'task.deleted', 'decision.created', 'decision.updated', 'decision.superseded', 'decision.deleted']) {
    queries.setQueryData(['audit', 'project'], []);
    stream.deliver(event(type));
    expect(queries.getQueryState(['audit', 'project'])?.isInvalidated, type).toBe(true);
  }
  for (const type of ['project.created', 'project.archived', 'project.restored']) expect(effectsOf(event(type))).toEqual([{ kind: 'projects' }]);
  queries.clear();
});

it.each([
  ['project.created', { name: 'Homelab' }, 'PROJECT', 'Created project “Homelab”'],
  ['project.updated', { name: 'New', previousName: 'Old', changedFields: ['name', 'instructions'] }, 'PROJECT', 'Renamed project “Old” to “New”'],
  ['project.archived', { name: 'Homelab' }, 'PROJECT', 'Archived project “Homelab”'],
  ['project.restored', { name: 'Homelab' }, 'PROJECT', 'Restored project “Homelab”'],
  ['project.deleted', { name: 'Gone' }, 'PROJECT', 'Deleted project “Gone”'],
  ['task.created', { title: 'Investigate' }, 'TASK', 'Created task “Investigate”'],
  ['task.updated', { title: 'Investigate', previousStatus: 'TODO', status: 'IN_PROGRESS' }, 'TASK', 'Updated task “Investigate”'],
  ['task.deleted', { title: 'Gone task' }, 'TASK', 'Deleted task “Gone task”'],
  ['decision.created', { title: 'Global fact', previousProjectId: 'old' }, 'MEMORY', 'Made memory “Global fact” global'],
  ['decision.superseded', { title: 'Old fact' }, 'MEMORY', 'Moved memory “Old fact” to history'],
  ['decision.updated', { title: 'Fact', importance: 'IMPORTANT' }, 'MEMORY', 'Pinned memory “Fact”'],
  ['decision.updated', { title: 'Fact', restored: true }, 'MEMORY', 'Restored memory “Fact”'],
  ['decision.deleted', { title: 'Forgotten' }, 'MEMORY', 'Deleted memory “Forgotten”'],
] as const)('names an audited subject after deletion: %s', (action, detail, kind, title) => {
  expect(knowledgeAudit({ action, detail, subjectId: 'deleted-id' } as unknown as AuditEntry)).toMatchObject({ kind, title });
});

it('plays audited project and memory mutations without copying their content', async () => {
  const events: Event[] = [];
  const stop = demoLifecycleEvents((value) => events.push(value));
  const request = async (method: string, path: string, body: unknown = null) => demoRequest(method, path, body);
  try {
    const project = await request('POST', '/api/v1/projects', { name: 'Audited demo', description: 'private description' }) as { id: string };
    const path = '/api/v1/projects/' + project.id;
    await request('PATCH', path, { name: 'Renamed demo', instructions: 'private project instructions' });
    await request('POST', path + '/archive');
    await expect(request('POST', path + '/archive')).rejects.toMatchObject({ status: 409 });
    await request('POST', path + '/restore');
    const task = await request('POST', path + '/tasks', { title: 'First', description: 'private task' }) as { id: string };
    const blocker = await request('POST', path + '/tasks', { title: 'Blocker' }) as { id: string };
    await request('PATCH', '/api/v1/tasks/' + task.id, { status: 'IN_PROGRESS' });
    const dependency = '/api/v1/tasks/' + task.id + '/dependencies';
    await request('POST', dependency, { dependsOn: blocker.id });
    const count = events.length;
    await request('POST', dependency, { dependsOn: blocker.id });
    expect(events).toHaveLength(count);
    await expect(request('POST', '/api/v1/tasks/' + blocker.id + '/dependencies', { dependsOn: task.id })).rejects.toMatchObject({ status: 400 });
    await request('DELETE', dependency + '/' + blocker.id);
    await request('DELETE', '/api/v1/tasks/' + task.id);
    const old = await request('POST', path + '/decisions', { title: 'Original fact', content: 'private memory', importance: 'NORMAL' }) as { id: string };
    const next = await request('POST', path + '/decisions', { title: 'Replacement fact', content: 'private replacement', supersedes: old.id }) as { id: string };
    await request('PATCH', '/api/v1/decisions/' + next.id, { importance: 'IMPORTANT' });
    await request('DELETE', '/api/v1/decisions/' + next.id);
    expect(events.some((value) => value.type === 'decision.updated' && value.payload?.restored)).toBe(true);
    const global = await request('POST', '/api/v1/decisions/' + old.id + '/global') as { id: string };
    expect(events.some((value) => value.type === 'decision.created' && value.payload?.previousProjectId === project.id && !value.projectId)).toBe(true);
    await request('DELETE', '/api/v1/decisions/' + global.id);
    await request('DELETE', path);
    const audit = await request('GET', '/api/v1/me/audit?projectId=' + project.id) as { items: AuditEntry[] };
    expect(audit.items.some((entry) => entry.action === 'project.deleted' && entry.detail?.name === 'Renamed demo')).toBe(true);
    expect(audit.items.some((entry) => entry.action === 'task.deleted' && entry.detail?.title === 'First')).toBe(true);
    expect(events.at(-1)).toMatchObject({ type: 'project.deleted', projectId: undefined });
    expect(JSON.stringify(audit)).not.toContain('private');
    expect(JSON.stringify(events)).not.toContain('private');
  } finally { stop(); }
});
