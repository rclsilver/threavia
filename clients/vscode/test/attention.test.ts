import { afterEach, expect, it } from 'vitest';

import type { CoreClient } from '../src/api/client';
import { EventBus } from '../src/api/events';
import type { Event, UserInputRequest, ValidationRequest } from '../src/api/types';
import { AttentionStore } from '../src/attention/store';
import { applyToAttention, applyToJobs } from '../src/conversation/state';
import { withoutJobAttention, dropInterruptedAttention } from '../../../web/ui/src/api/attention-cache';

const at = '2026-10-13T12:00:00Z';
const input = (id: string, jobId = 'job', createdAt = '2026-10-13T11:59:00Z'): UserInputRequest => ({
  id, scope: { jobId, sessionId: 'session', projectId: 'project', runId: 'run' }, createdAt, status: 'PENDING', prompt: 'Where?', freeText: true, notify: true,
});
const validation = (id: string, jobId = 'job'): ValidationRequest => ({
  id, scope: { jobId, sessionId: 'session', projectId: 'project', runId: 'run' }, createdAt: '2026-10-13T11:59:00Z', status: 'PENDING', title: 'Write?', requestPayload: {}, payloadSha256: 'hash', notify: true,
});
const interrupt: Event = { id: 'interrupt', sequence: 3, timestamp: at, type: 'user.message', sessionId: 'session', jobId: 'job', payload: { text: 'Inspect instead', delivery: 'NOW' } };
const pending = () => ({ validations: [validation('old-v'), validation('other-v', 'other')], userInputs: [input('old'), input('new', 'job', '2026-10-13T12:01:00Z'), input('other', 'other')] });
const stores: AttentionStore[] = [];
afterEach(() => { for (const store of stores.splice(0)) store.dispose(); });

it('removes only the interrupted turn’s older questions and approvals in both clients', () => {
  const expected = { validations: [validation('other-v', 'other')], userInputs: [input('new', 'job', '2026-10-13T12:01:00Z'), input('other', 'other')] };
  expect(applyToAttention(pending(), interrupt)).toEqual(expected);
  expect(withoutJobAttention(pending(), 'job', at)).toEqual(expected);
  const jobs = [{ id: 'job', runId: 'run', status: 'WAITING_INPUT' as const, createdAt: at, updatedAt: at }];
  expect(applyToJobs(jobs, interrupt)[0].status).toBe('RUNNING');
  expect(applyToAttention(pending(), { ...interrupt, payload: { text: 'Later', delivery: 'NEXT' } })).toEqual(pending());
});

it('cancels stale web reads and clears both the global list and the session snapshot', () => {
  const cache = new Map<string, unknown>([
    [JSON.stringify(['attention']), pending()],
    [JSON.stringify(['snapshot', 'session']), { attention: pending() }],
  ]);
  const cancelled: unknown[] = [];
  const queries = {
    cancelQueries: (key: unknown) => { cancelled.push(key); return Promise.resolve(); },
    setQueryData: (key: unknown, change: (data: unknown) => unknown) => cache.set(JSON.stringify(key), change(cache.get(JSON.stringify(key)))),
  } as unknown as Parameters<typeof dropInterruptedAttention>[0];
  dropInterruptedAttention(queries, 'session', 'job', at);
  const expected = withoutJobAttention(pending(), 'job', at);
  expect(cache.get(JSON.stringify(['attention']))).toEqual(expected);
  expect(cache.get(JSON.stringify(['snapshot', 'session']))).toEqual({ attention: expected });
  expect(cancelled).toEqual([{ queryKey: ['attention'] }, { queryKey: ['snapshot', 'session'] }]);
});

it('does not resurrect an interrupted prompt when an older attention fetch completes', async () => {
  let finish!: (data: ReturnType<typeof pending>) => void;
  const bus = new EventBus();
  const client = { attention: () => new Promise<ReturnType<typeof pending>>((resolve) => { finish = resolve; }) } as unknown as CoreClient;
  const store = new AttentionStore(client, bus);
  stores.push(store);
  const refresh = store.refresh();
  bus.publish(interrupt);
  finish(pending());
  await refresh;
  expect(store.current).toEqual(withoutJobAttention(pending(), 'job', at));
});

it('does not resurrect an answer given in VS Code while a refresh was running', async () => {
  let finish!: (data: ReturnType<typeof pending>) => void;
  const bus = new EventBus();
  const client = { attention: () => new Promise<ReturnType<typeof pending>>((resolve) => { finish = resolve; }) } as unknown as CoreClient;
  const store = new AttentionStore(client, bus);
  stores.push(store);
  const refresh = store.refresh();
  store.drop(undefined, 'old');
  finish(pending());
  await refresh;
  expect(store.findUserInput('old')).toBeUndefined();
  expect(store.findUserInput('new')).toBeDefined();
});
