import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';

import { EventStream } from '../../../web/ui/src/api/stream';
import type { Event } from '../../../web/ui/src/api/types';
import { onJobEnded, type JobEnded } from '../../../web/ui/src/lib/job-ended';

let queries: QueryClient;
let announced: JobEnded[];
let stop: () => void;

beforeEach(() => {
  vi.stubGlobal('document', { visibilityState: 'visible', hasFocus: () => true });
  queries = new QueryClient();
  announced = [];
  stop = onJobEnded((ended) => announced.push(ended));
});

afterEach(() => {
  stop();
  queries.clear();
  vi.unstubAllGlobals();
});

const ended = (sequence: number, type: string, payload: Record<string, unknown>): Event => ({
  id: `e${sequence}`, sequence, sessionId: `s${sequence}`, jobId: `j${sequence}`, runId: 'r', type,
  timestamp: new Date().toISOString(), payload,
});

it("announces the end of a person's session, not of one its manager reports", () => {
  const stream = new EventStream(queries);
  stream.deliver(ended(1, 'job.completed', { summary: 'Done' }));
  stream.deliver(ended(2, 'job.completed', { summary: 'Worker done', delegated: true }));
  stream.deliver(ended(3, 'job.failed', { error: 'Worker failed', delegated: true }));
  expect(announced).toEqual([{ sessionId: 's1', failed: false, text: 'Done' }]);
});
