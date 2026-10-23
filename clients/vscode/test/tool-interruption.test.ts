import { describe, expect, it } from 'vitest';
import type { Event } from '../src/api/types';
import { foldFinished, rowsOf } from '../src/conversation/timeline';

const event = (sequence: number, type: string, jobId: string, payload: object = {}): Event => ({
  id: String(sequence), sequence, type, jobId, sessionId: 'session',
  timestamp: '2026-10-24T07:15:00Z', payload,
} as Event);
const start = (sequence: number, id: string, job = 'job') => event(sequence, 'tool.started', job, { toolCallId: id, name: 'mock_slow_read' });
const tools = (events: Event[]) => rowsOf(events).filter(row => row.kind === 'tool');

describe('tool calls when their work ends', () => {
  it.each(['cancelled', 'failed', 'completed'] as const)('settles a missing tool result after job.%s', ending => {
    const events = [start(1, 'pending'), event(2, 'job.' + ending, 'job')];
    const [row] = tools(events);
    expect(row.call).toMatchObject({ done: true, interruption: ending });
    expect(row.call.output).toBeUndefined();
    expect(row.call.error).toBeUndefined();
    // Reopening the recorded history must give the same state as live delivery.
    expect(tools([...events])[0].call).toEqual(row.call);
  });

  it('preserves actual results and failures and leaves another job running', () => {
    const rows = tools([
      start(1, 'finished'),
      event(2, 'tool.completed', 'job', { toolCallId: 'finished', output: { output: 'actual result' } }),
      start(3, 'failed'),
      event(4, 'tool.failed', 'job', { toolCallId: 'failed', error: 'actual failure' }),
      start(5, 'interrupted'), start(6, 'other', 'other-job'),
      event(7, 'job.cancelled', 'job'),
    ]);
    expect(rows[0].call).toMatchObject({ done: true, output: 'actual result' });
    expect(rows[0].call.interruption).toBeUndefined();
    expect(rows[1].call).toMatchObject({ done: true, error: 'actual failure' });
    expect(rows[1].call.interruption).toBeUndefined();
    expect(rows[2].call).toMatchObject({ done: true, interruption: 'cancelled' });
    expect(rows[3].call).toMatchObject({ done: false });
    expect(rows[3].call.interruption).toBeUndefined();
  });

  it.each(['tool.completed', 'tool.failed'])('keeps a late %s rather than inventing an interruption', type => {
    const [row] = tools([
      start(1, 'late'), event(2, 'job.cancelled', 'job'),
      event(3, type, 'job', { toolCallId: 'late', output: { output: 'late result' }, error: 'late error' }),
    ]);
    expect(row.call.done).toBe(true);
    expect(row.call.interruption).toBeUndefined();
    if (type === 'tool.completed') expect(row.call.output).toBe('late result');
    else expect(row.call.error).toBe('late error');
  });

  it('keeps a tool live while its job is waiting for backend recovery', () => {
    expect(tools([start(1, 'pending'), event(2, 'job.waiting_backend', 'job')])[0].call.done).toBe(false);
  });

  it('does not count interrupted calls as reported tool failures', () => {
    const rows = foldFinished(rowsOf([start(1, 'a'), start(2, 'b'), event(3, 'job.cancelled', 'job')]), new Set());
    const fold = rows.find(row => row.kind === 'steps');
    expect(fold?.steps).toMatchObject({ tools: 2, failed: 0 });
  });
});
