import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';

import { keys } from '../../../web/ui/src/api/keys';
import { EventStream } from '../../../web/ui/src/api/stream';
import type { Event, Project, Session } from '../../../web/ui/src/api/types';

let queries: QueryClient;
let stream: EventStream;

beforeEach(() => {
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  queries = new QueryClient();
  stream = new EventStream(queries);
  queries.setQueryData<Project[]>(keys.projects(), []);
  queries.setQueryData<Session[]>(keys.sessions('p'), []);
  queries.setQueryData<Session[]>(keys.pinned(), []);
});

afterEach(() => {
  queries.clear();
  vi.unstubAllGlobals();
});

const event = (type: string, payload: Record<string, unknown> = {}): Event => ({
  id: type, sequence: 10, timestamp: '2026-10-29T10:00:00Z', type, projectId: 'p', sessionId: 's', payload,
});

const stale = (key: readonly unknown[]) => queries.getQueryState(key)?.isInvalidated;

it('stops a session standing out everywhere once it was read on another client', () => {
  stream.deliver(event('session.read', { readSequence: 9 }));
  expect(stale(keys.sessions('p'))).toBe(true);
  expect(stale(keys.pinned())).toBe(true);
  expect(stale(keys.projects())).toBe(true);
});

it('lets a project the sidebar is not showing point at what its agent just said', () => {
  stream.deliver({ ...event('job.completed'), jobId: 'j', timestamp: '2020-01-01T00:00:00Z' });
  expect(stale(keys.projects())).toBe(true);
});
