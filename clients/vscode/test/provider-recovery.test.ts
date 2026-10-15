import { expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import { EventStream } from '../../../web/ui/src/api/stream';
import { keys } from '../../../web/ui/src/api/keys';
import { onJobEnded } from '../../../web/ui/src/lib/job-ended';
import type { Event, Snapshot } from '../../../web/ui/src/api/types';
import { applyToAttention } from '../src/conversation/state';

it('updates web recovery state and clears stale questions without an ending notification', () => {
  const queries = new QueryClient();
  const input = { id: 'q', status: 'PENDING' as const, scope: { projectId: 'p', runId: 'r', jobId: 'j', sessionId: 's' }, prompt: 'Continue?', freeText: true, notify: true, createdAt: '2026-10-15T09:00:00Z' };
  const attention = { validations: [], userInputs: [input] };
  queries.setQueryData(keys.snapshot('s'), { jobs: [{ id: 'j', runId: 'r', status: 'RUNNING' }], events: [], attention, cursor: 0 } as unknown as Snapshot);
  queries.setQueryData(keys.attention(), attention);
  const stream = new EventStream(queries);
  const endings: unknown[] = [];
  const stop = onJobEnded((value) => endings.push(value));
  const backendWait = { reason: 'QUOTA_EXCEEDED', message: 'Quota exhausted', retryAt: '2026-10-15T12:00:00Z' };
  const event: Event = { id: 'e', sequence: 1, type: 'job.waiting_backend', sessionId: 's', jobId: 'j', timestamp: '2026-10-15T09:01:00Z', payload: backendWait };
  try {
    stream.deliver(event);
    const snapshot = queries.getQueryData<Snapshot>(keys.snapshot('s'))!;
    expect(snapshot.jobs[0]).toMatchObject({ status: 'WAITING_BACKEND', backendWait });
    expect(snapshot.attention.userInputs).toEqual([]);
    expect(queries.getQueryData(keys.attention())).toEqual({ validations: [], userInputs: [] });
    expect(applyToAttention(attention, event)).toEqual({ validations: [], userInputs: [] });
    expect(endings).toEqual([]);
    stream.deliver({ ...event, id: 'resumed', sequence: 2, type: 'job.started', payload: {} });
    expect(queries.getQueryData<Snapshot>(keys.snapshot('s'))!.jobs[0]).toMatchObject({ status: 'RUNNING', backendWait: undefined });
  } finally {
    stop();
    queries.clear();
  }
});
