import { describe, expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import { EventStream } from '../../../web/ui/src/api/stream';
import { demoLifecycleEvents, demoRequest } from '../../../web/ui/src/demo/server';
import { IDS, skills } from '../../../web/ui/src/demo/data';
import { EventBus, effectsOf } from '../src/api/events';
import type { Event } from '../src/api/types';

function webCache() {
  const queries = new QueryClient();
  const invalidated: unknown[][] = [];
  const original = queries.invalidateQueries.bind(queries);
  queries.invalidateQueries = (filters, options) => {
    invalidated.push([...(filters?.queryKey ?? [])]);
    return original(filters, options);
  };
  return { queries, invalidated, web: new EventStream(queries) };
}

describe('lifecycle events across clients', () => {
  it('refreshes skill contents and audit, including a replacement with the same skill id', () => {
    const { queries, invalidated, web } = webCache();
    const editor = new EventBus();
    const effects: unknown[] = [];
    editor.on('effect', (effect) => effects.push(effect));
    queries.setQueryData(['skills', 'project'], [{ id: 'skill', installedRevision: 'old' }]);
    queries.setQueryData(['skill-files', 'skill'], [{ path: 'SKILL.md', text: 'old instructions' }]);
    queries.setQueryData(['audit', 'project'], []);
    for (const [sequence, type] of ['skill.installed', 'skill.removed'].entries()) {
      const event: Event = { id: type, type, sequence, timestamp: new Date().toISOString(), projectId: 'project',
        payload: { skillId: 'skill', name: 'deploy', installedRevision: 'new', replaced: true } };
      web.deliver(event);
      editor.publish(event);
    }
    expect(queries.getQueryState(['skill-files', 'skill'])?.isInvalidated).toBe(true);
    expect(queries.getQueryState(['audit', 'project'])?.isInvalidated).toBe(true);
    expect(invalidated).toEqual([
      ['skills', 'project'], ['skill-files', 'skill'], ['audit'],
      ['skills', 'project'], ['skill-files', 'skill'], ['audit'],
    ]);
    expect(effects).toEqual(Array(2).fill({ kind: 'skills', projectId: 'project', skillId: 'skill' }));
    queries.clear();
  });

  it('refreshes the deleted session from the durable payload and accepts older envelopes', () => {
    const { queries, invalidated, web } = webCache();
    for (const legacy of [false, true]) {
      const event: Event = { id: String(legacy), sequence: 4, timestamp: new Date().toISOString(), type: 'session.deleted', projectId: 'project',
        ...(legacy ? { sessionId: 'session', payload: {} } : { payload: { sessionId: 'session', title: 'Old session', artifactsDeleted: false } }) };
      web.deliver(event);
      expect(effectsOf(event)).toEqual([{ kind: 'sessions' }, { kind: 'attention' }, { kind: 'snapshot', sessionId: 'session' }]);
    }
    expect(invalidated.filter((key) => key[0] === 'snapshot')).toEqual([['snapshot', 'session'], ['snapshot', 'session']]);
    expect(invalidated.filter((key) => key[0] === 'audit')).toHaveLength(2);
    queries.clear();
  });

  it('refreshes the audit for creation, archival and restoration', () => {
    const { queries, invalidated, web } = webCache();
    for (const type of ['session.created', 'session.archived', 'session.restored']) {
      web.deliver({ id: type, type, sequence: 1, timestamp: new Date().toISOString(), projectId: 'project', sessionId: 'session', payload: {} });
    }
    expect(invalidated.filter((key) => key[0] === 'audit')).toHaveLength(3);
    queries.clear();
  });
});

it('plays session lifecycle and skill removal through the same event path and keeps audit names', async () => {
  const { queries, web } = webCache();
  const events: Event[] = [];
  const stop = demoLifecycleEvents((event) => { events.push(event); web.deliver(event); });
  const request = async (method: string, path: string, body: unknown = null) => demoRequest(method, path, body);
  try {
    const created = await request('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'Remember this deleted session' }) as { session: { id: string } };
    const path = '/api/v1/sessions/' + created.session.id;
    await request('POST', path + '/archive');
    await expect(request('POST', path + '/archive')).rejects.toMatchObject({ status: 409 });
    await request('POST', path + '/restore');
    const snapshot = await request('GET', path) as { events: Event[] };
    for (const streamed of events) {
      expect(snapshot.events.find((saved) => saved.type === streamed.type)?.sequence).toBe(streamed.sequence);
    }
    await request('DELETE', path);
    await expect(request('GET', path)).rejects.toMatchObject({ status: 404 });
    const skill = skills[0];
    await request('DELETE', '/api/v1/skills/' + skill.id);
    const audit = await request('GET', '/api/v1/me/audit?projectId=' + IDS.homelab) as { items: { action: string; sessionId?: string; subjectId?: string; detail: Record<string, unknown> }[] };
    const sessionEntries = audit.items.filter((entry) => entry.sessionId === created.session.id);
    expect(sessionEntries.map((entry) => entry.action)).toEqual(['session.deleted', 'session.restored', 'session.archived', 'session.created']);
    expect(sessionEntries.every((entry) => entry.detail.title === 'Remember this deleted session')).toBe(true);
    expect(audit.items.find((entry) => entry.action === 'skill.removed' && entry.subjectId === skill.id)?.detail.name).toBe(skill.name);
    expect(events.map((event) => event.type)).toEqual(['session.created', 'session.archived', 'session.restored', 'session.deleted', 'skill.removed']);
    expect(events.find((event) => event.type === 'session.deleted')?.sessionId).toBeUndefined();
    stop();
    await request('DELETE', '/api/v1/skills/' + skills[1].id);
    expect(events).toHaveLength(5);
  } finally { stop(); queries.clear(); }
});
