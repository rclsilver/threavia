import { expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import { EventStream } from '../../../web/ui/src/api/stream';
import type { Event } from '../src/api/types';
import { effectsOf } from '../src/api/events';
import { describe, rowsOf, foldFinished } from '../src/conversation/timeline';

it('shows lost and replaced native conversations and refreshes both client snapshots', () => {
  const queries = new QueryClient();
  const web = new EventStream(queries);
  for (const [index, type] of ['run.resume_unavailable', 'run.resume_available', 'run.resume_failed'].entries()) {
    const event: Event = { id: `resume-${index}`, sequence: index + 1, timestamp: '2026-10-16T00:00:00Z', type, sessionId: 's', runId: 'r', jobId: 'j', payload: { reason: type.endsWith('unavailable') ? 'NOT_FOUND' : 'NEW_SESSION' } };
    queries.setQueryData(['snapshot', 's'], { events: [] });
    web.deliver(event);
    expect(queries.getQueryState(['snapshot', 's'])?.isInvalidated).toBe(true);
    expect(effectsOf(event)).toContainEqual({ kind: 'snapshot', sessionId: 's' });
    expect(rowsOf([event]).filter((row) => row.kind === 'event')).toHaveLength(1);
    const steps = [
      { ...event, id: 'note-1', sequence: 10, type: 'agent.message', payload: { text: 'First step' } },
      { ...event, id: 'note-2', sequence: 11, type: 'agent.message', payload: { text: 'Second step' } },
      { ...event, sequence: 12 },
      { ...event, id: 'answer', sequence: 13, type: 'agent.message', payload: { text: 'Last answer' } },
      { ...event, id: 'end', sequence: 14, type: 'job.failed', payload: { error: 'Failed' } },
    ];
    expect(foldFinished(rowsOf(steps), new Set()).some((row) => row.kind === 'event' && row.event.type === type)).toBe(true);
    expect(describe(event).text).toContain(type.endsWith('unavailable') ? 'Core history is preserved' : type.endsWith('failed') ? 'binding is retained' : 'fresh provider conversation');
    if (type.endsWith('unavailable')) expect(describe(event).text).toContain('not retried automatically');
  }
  queries.clear();
});
