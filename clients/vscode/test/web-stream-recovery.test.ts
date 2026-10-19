import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';

import { keys } from '../../../web/ui/src/api/keys';
import { EventStream } from '../../../web/ui/src/api/stream';
import type { Event, Snapshot } from '../../../web/ui/src/api/types';

let queries: QueryClient;
let stream: EventStream;
let controllers: ReadableStreamDefaultController<Uint8Array>[];
let cancelled: ReturnType<typeof vi.fn>;
let fetcher: ReturnType<typeof vi.fn>;
const encoder = new TextEncoder();

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('document', { visibilityState: 'visible', hasFocus: () => true });
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  queries = new QueryClient();
  stream = new EventStream(queries);
  controllers = [];
  cancelled = vi.fn();
  fetcher = vi.fn(() => Promise.resolve(new Response(new ReadableStream<Uint8Array>({
    start: (controller) => controllers.push(controller),
    cancel: cancelled,
  }))));
  vi.stubGlobal('fetch', fetcher);
});

afterEach(async () => {
  stream.close();
  await vi.advanceTimersByTimeAsync(0);
  queries.clear();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function seed(id: string, cursor = 0) {
  queries.setQueryData(keys.snapshot(id), { cursor, events: [], jobs: [], attention: { validations: [], userInputs: [] } } as unknown as Snapshot);
}

function send(sequence: number, sessionId = 's') {
  const event: Event = { id: `e${sequence}`, sequence, sessionId, jobId: `j${sessionId}`, runId: 'r', type: 'job.started', timestamp: new Date().toISOString(), payload: {} };
  controllers.at(-1)!.enqueue(encoder.encode(`event: event\ndata: ${JSON.stringify(event)}\n\n`));
}

it('reconnects a silent connection and replays from the last consumed global event', async () => {
  seed('s');
  seed('other');
  const state = vi.fn();
  stream.open(state);
  await vi.advanceTimersByTimeAsync(0);
  send(7);
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.position).toBe(7);
  // A more recent Session snapshot cannot acknowledge events for other Sessions.
  seed('s', 100);
  await vi.advanceTimersByTimeAsync(45_000);
  expect(state).toHaveBeenLastCalledWith(false);
  expect(cancelled).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1000);
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(fetcher.mock.calls[1][0]).toContain('after=7&');
  send(8, 'other');
  await vi.advanceTimersByTimeAsync(0);
  expect(queries.getQueryData<Snapshot>(keys.snapshot('other'))!.jobs[0].status).toBe('RUNNING');
  expect(state).toHaveBeenLastCalledWith(true);
});

it('keeps an idle connection alive on comment keepalives and fragmented frames', async () => {
  stream.open(vi.fn());
  await vi.advanceTimersByTimeAsync(0);
  for (let n = 0; n < 5; n++) {
    await vi.advanceTimersByTimeAsync(20_000);
    controllers[0].enqueue(encoder.encode(': keep'));
    controllers[0].enqueue(encoder.encode('alive\n\n'));
    await vi.advanceTimersByTimeAsync(0);
  }
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(cancelled).not.toHaveBeenCalled();
  expect(stream.position).toBe(0);
});

it('does not rewind snapshot Job state while replaying events outside its recent window', async () => {
  seed('s', 100);
  queries.setQueryData<Snapshot>(keys.snapshot('s'), (snapshot) => ({ ...snapshot!, jobs: [{ id: 'js', runId: 'r', status: 'COMPLETED', createdAt: '', updatedAt: '' }] }));
  stream.open(vi.fn());
  await vi.advanceTimersByTimeAsync(0);
  send(7);
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.position).toBe(7);
  const snapshot = queries.getQueryData<Snapshot>(keys.snapshot('s'))!;
  expect(snapshot.jobs[0].status).toBe('COMPLETED');
  expect(snapshot.events).toEqual([]);
  send(101);
  await vi.advanceTimersByTimeAsync(0);
  expect(queries.getQueryData<Snapshot>(keys.snapshot('s'))!.jobs[0].status).toBe('RUNNING');
});

it('bounds a connection that never returns headers', async () => {
  fetcher.mockImplementation((_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
    init.signal!.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
  }));
  const state = vi.fn();
  stream.open(state);
  await vi.advanceTimersByTimeAsync(46_000);
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(state).not.toHaveBeenCalledWith(true);
});

it('replaces the connection on reopen without letting the old reader change state', async () => {
  const oldState = vi.fn();
  const newState = vi.fn();
  stream.open(oldState);
  await vi.advanceTimersByTimeAsync(0);
  oldState.mockClear();
  stream.open(newState);
  await vi.advanceTimersByTimeAsync(0);
  expect(cancelled).toHaveBeenCalledTimes(1);
  expect(oldState).not.toHaveBeenCalled();
  expect(newState).toHaveBeenLastCalledWith(true);
  stream.close();
  await vi.advanceTimersByTimeAsync(60_000);
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(vi.getTimerCount()).toBe(0);
});

it('backs off repeated HTTP failures and stops retrying when closed', async () => {
  fetcher.mockImplementation(() => Promise.resolve(new Response(null, { status: 503 })));
  stream.open(vi.fn());
  await vi.advanceTimersByTimeAsync(0);
  await vi.advanceTimersByTimeAsync(1000);
  expect(fetcher).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(1999);
  expect(fetcher).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(1);
  expect(fetcher).toHaveBeenCalledTimes(3);
  stream.close();
  await vi.advanceTimersByTimeAsync(60_000);
  expect(fetcher).toHaveBeenCalledTimes(3);
  expect(vi.getTimerCount()).toBe(0);
});
