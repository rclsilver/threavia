import { afterEach, expect, it, vi } from 'vitest';
import { QueryClient, type MutationObserverOptions, type QueryObserverOptions } from '../../../web/ui/node_modules/@tanstack/react-query';

import { keys } from '../../../web/ui/src/api/keys';
import { usePostMessage, useSnapshot, type Delivery } from '../../../web/ui/src/api/queries';
import { EventStream } from '../../../web/ui/src/api/stream';
import type { Snapshot } from '../../../web/ui/src/api/types';

let queries: QueryClient;

// Exercise the real mutation lifecycle without mounting a browser: only the
// hooks that connect it to React are replaced by their Query core equivalent.
vi.mock('../../../web/ui/node_modules/@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../web/ui/node_modules/@tanstack/react-query')>();
  return {
    ...actual,
    useQueryClient: () => queries,
    useQuery: (options: QueryObserverOptions) => {
      const observer = new actual.QueryObserver(queries, options);
      return { refetch: () => observer.refetch() };
    },
    useMutation: (options: MutationObserverOptions) => {
      const observer = new actual.MutationObserver(queries, options);
      return { mutateAsync: (variables: void) => observer.mutate(variables) };
    },
  };
});

afterEach(() => {
  queries.clear();
  vi.unstubAllGlobals();
});

it.each<Delivery>(['QUEUE', 'NEXT', 'NOW'])('refreshes accepted %s messages even without a stream', async (delivery) => {
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(Response.json({ id: 'j', status: 'RUNNING', updatedAt: new Date().toISOString() }))));
  queries = new QueryClient({ defaultOptions: { mutations: { gcTime: Infinity } } });
  const snapshotKey = keys.snapshot('s');
  const sessionsKey = keys.sessions('p', false);
  queries.setQueryData(snapshotKey, { jobs: [], events: [], attention: { validations: [], userInputs: [] } });
  queries.setQueryData(sessionsKey, []);
  await usePostMessage('s').mutateAsync({ message: 'Continue', delivery });
  expect(queries.getQueryState(snapshotKey)!.isInvalidated).toBe(true);
  expect(queries.getQueryState(sessionsKey)!.isInvalidated).toBe(true);
  expect(queries.getQueryData<Snapshot>(snapshotKey)!.jobs).toEqual([expect.objectContaining({ id: 'j', status: 'RUNNING' })]);
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('does not overwrite a Job that the stream completed before the send response arrived', async () => {
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(Response.json({ id: 'j', status: 'QUEUED' }))));
  queries = new QueryClient({ defaultOptions: { mutations: { gcTime: Infinity } } });
  queries.setQueryData(keys.snapshot('s'), { jobs: [{ id: 'j', status: 'COMPLETED' }], events: [], attention: { validations: [], userInputs: [] } });
  await usePostMessage('s').mutateAsync({ message: 'Continue' });
  expect(queries.getQueryData<Snapshot>(keys.snapshot('s'))!.jobs).toEqual([{ id: 'j', status: 'COMPLETED' }]);
});

it('keeps newer stream state when a delayed snapshot response arrives', async () => {
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  let finish!: (response: Response) => void;
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { finish = resolve; })));
  queries = new QueryClient();
  const before = { cursor: 4, jobs: [], events: [], attention: { validations: [], userInputs: [] } };
  queries.setQueryData(keys.snapshot('s'), before);
  const pending = useSnapshot('s').refetch();
  await vi.waitFor(() => expect(fetch).toHaveBeenCalled());
  new EventStream(queries).deliver({ id: 'e5', sequence: 5, sessionId: 's', runId: 'r', jobId: 'j', type: 'job.started', timestamp: new Date().toISOString(), payload: {} });
  finish(Response.json(before));
  await pending;
  const snapshot = queries.getQueryData<Snapshot>(keys.snapshot('s'))!;
  expect(snapshot.cursor).toBe(5);
  expect(snapshot.jobs[0].status).toBe('RUNNING');
  expect(snapshot.events.map((event) => event.id)).toEqual(['e5']);
});
