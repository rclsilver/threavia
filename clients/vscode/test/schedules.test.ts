import { expect, it } from 'vitest';
import { EventStream } from '../../../web/ui/src/api/stream';
import { describe, rowsOf } from '../src/conversation/timeline';
import type { Event } from '../src/api/types';

it('refreshes schedule and audit data when a schedule is changed by an agent', () => {
  const invalidated: (readonly unknown[])[] = [];
  const queries = { setQueryData: () => undefined, invalidateQueries: ({ queryKey }: { queryKey: readonly unknown[] }) => {
    invalidated.push(queryKey);
    return Promise.resolve();
  } } as unknown as ConstructorParameters<typeof EventStream>[0];
  const stream = new EventStream(queries);
  for (const type of ['schedule.created', 'schedule.updated', 'schedule.deleted']) {
    const event: Event = { id: type, type, sessionId: 'session', jobId: 'job', sequence: 1, timestamp: new Date().toISOString(),
      payload: { scheduleId: 'schedule', cron: '0 8 * * *', timezone: 'Europe/Paris', message: 'Check backups.', enabled: false, actorJobId: 'job' } };
    stream.deliver(event);
    const description = describe(event);
    expect(rowsOf([event]).some((row) => row.kind === 'event')).toBe(true);
    expect(description.text).toContain('Agent');
    expect(description.text).toContain('Europe/Paris');
    expect(description.text).toContain('Check backups.');
    expect(description.text.includes('paused')).toBe(type !== 'schedule.deleted');
  }
  expect(invalidated.filter((key) => key[0] === 'audit')).toHaveLength(3);
  expect(invalidated.filter((key) => key.includes('schedules'))).toHaveLength(3);
});
