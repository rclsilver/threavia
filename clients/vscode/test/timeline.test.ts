import { describe, expect, it } from 'vitest';

import type { Event } from '../src/api/types';
import {
  describe as describeEvent,
  foldFinished,
  rowsOf,
  stepsLabel,
  stickyPrompt,
  type Row,
} from '../src/conversation/timeline';

const NOW = new Date('2026-10-09T12:00:00');
let sequence = 0;

const event = (type: string, payload: object = {}, extra: Partial<Event> = {}): Event => {
  sequence++;
  return {
    id: `e${sequence}`,
    sequence,
    timestamp: `2026-10-09T10:${String(sequence).padStart(2, '0')}:00`,
    type,
    sessionId: 's1',
    payload,
    ...extra,
  } as Event;
};

/** A finished Job: a message, three tool calls, a note, an answer, an ending. */
function finishedJob(jobId: string): Event[] {
  return [
    event('user.message', { text: 'Fix the build' }, { jobId }),
    event('job.started', {}, { jobId }),
    event('tool.started', { toolCallId: `${jobId}-a`, name: 'Bash', input: { command: 'make' } }, { jobId }),
    event('tool.completed', { toolCallId: `${jobId}-a`, name: 'Bash', output: { output: 'ok' } }, { jobId }),
    event('agent.message', { text: 'Looking at the tests' }, { jobId }),
    event('tool.started', { toolCallId: `${jobId}-b`, name: 'Read', input: { file_path: 'a.go' } }, { jobId }),
    event('tool.failed', { toolCallId: `${jobId}-b`, name: 'Read', error: 'nope' }, { jobId }),
    event('agent.message', { text: 'Fixed.' }, { jobId }),
    event('job.completed', { usage: { inputTokens: 1, outputTokens: 2, cacheReadTokens: 0, cacheWriteTokens: 0 } }, { jobId }),
  ];
}

const kinds = (rows: Row[]) =>
  rows.map((row) => (row.kind === 'event' ? row.event.type : row.kind === 'tool' ? `tool:${row.call.name}` : row.kind));

describe('rowsOf', () => {
  it('pairs a tool call with its result, and opens the day once', () => {
    sequence = 0;
    const rows = rowsOf(finishedJob('j1'), NOW);
    expect(kinds(rows)).toEqual([
      'day',
      'user.message',
      'tool:Bash',
      'agent.message',
      'tool:Read',
      'agent.message',
      'job.completed',
    ]);
    const bash = rows[2] as Extract<Row, { kind: 'tool' }>;
    expect(bash.call).toMatchObject({ done: true, output: 'ok', input: { command: 'make' } });
    const read = rows[4] as Extract<Row, { kind: 'tool' }>;
    expect(read.call).toMatchObject({ done: true, error: 'nope' });
    expect((rows[0] as Extract<Row, { kind: 'day' }>).label).toBe('Today');
  });

  it('gives a Job ending when it started, for its duration', () => {
    sequence = 0;
    const rows = rowsOf(finishedJob('j1'), NOW);
    const ending = rows.at(-1) as Extract<Row, { kind: 'event' }>;
    expect(ending.startedAt).toBe('2026-10-09T10:02:00');
  });

  it('shows a failure whose start is before the loaded window', () => {
    sequence = 0;
    const rows = rowsOf([event('tool.failed', { toolCallId: 'x', name: 'Bash', error: 'boom' }, { jobId: 'j' })], NOW);
    expect(kinds(rows)).toEqual(['day', 'tool:Bash']);
  });

  it('separates days, and names yesterday', () => {
    sequence = 0;
    const rows = rowsOf(
      [
        event('user.message', { text: 'a' }, { timestamp: '2026-10-08T09:00:00' }),
        event('user.message', { text: 'b' }, { timestamp: '2026-10-09T09:00:00' }),
      ],
      NOW,
    );
    expect(rows.filter((row) => row.kind === 'day').map((row) => (row as { label: string }).label)).toEqual([
      'Yesterday',
      'Today',
    ]);
  });

  it('leaves machinery out', () => {
    sequence = 0;
    expect(rowsOf([event('job.created'), event('run.created')], NOW)).toEqual([]);
  });
});

describe('foldFinished', () => {
  it('folds the steps of a finished Job behind one row, keeping what was asked and answered', () => {
    sequence = 0;
    const rows = foldFinished(rowsOf(finishedJob('j1'), NOW), new Set());
    expect(kinds(rows)).toEqual(['day', 'user.message', 'steps', 'agent.message', 'job.completed']);
    const steps = rows[2] as Extract<Row, { kind: 'steps' }>;
    expect(steps.steps).toEqual({ tools: 2, failed: 1, notes: 1 });
    expect(stepsLabel(steps)).toBe('2 steps · 1 message');
  });

  it('opens a fold on demand, keyed just after its first step', () => {
    sequence = 0;
    const all = rowsOf(finishedJob('j1'), NOW);
    const folded = foldFinished(all, new Set());
    const key = folded[2].key;
    expect(key).toBe(all[2].key + 0.5);
    const open = foldFinished(all, new Set([key]));
    expect(kinds(open)).toEqual([
      'day',
      'user.message',
      'steps',
      'tool:Bash',
      'agent.message',
      'tool:Read',
      'agent.message',
      'job.completed',
    ]);
    expect(stepsLabel(open[2] as Extract<Row, { kind: 'steps' }>)).toBe('Hide 2 steps · 1 message');
  });

  it('leaves a running Job step by step', () => {
    sequence = 0;
    const running = finishedJob('j1').slice(0, -1);
    expect(kinds(foldFinished(rowsOf(running, NOW), new Set()))).not.toContain('steps');
  });

  it('does not fold a single step', () => {
    sequence = 0;
    const events = [
      event('user.message', { text: 'hi' }, { jobId: 'j' }),
      event('tool.started', { toolCallId: 'a', name: 'Bash', input: {} }, { jobId: 'j' }),
      event('agent.message', { text: 'done' }, { jobId: 'j' }),
      event('job.completed', {}, { jobId: 'j' }),
    ];
    expect(kinds(foldFinished(rowsOf(events, NOW), new Set()))).toEqual([
      'day',
      'user.message',
      'tool:Bash',
      'agent.message',
      'job.completed',
    ]);
  });

  it('lets results out of the run of steps, in their order', () => {
    sequence = 0;
    const j = { jobId: 'j' };
    const events = [
      event('user.message', { text: 'chart it' }, j),
      event('tool.started', { toolCallId: 'a', name: 'Bash', input: {} }, j),
      event('artifact.created', { artifactId: 'x', filename: 'a.png', mimeType: 'image/png', size: 1 }, j),
      event('tool.started', { toolCallId: 'b', name: 'Bash', input: {} }, j),
      event('workspace.changed', { additions: 1, deletions: 0, files: [] }, j),
      event('agent.message', { text: 'here' }, j),
      event('job.completed', {}, j),
    ];
    expect(kinds(foldFinished(rowsOf(events, NOW), new Set()))).toEqual([
      'day',
      'user.message',
      'steps',
      'artifact.created',
      'workspace.changed',
      'agent.message',
      'job.completed',
    ]);
  });
});

describe('stickyPrompt', () => {
  const geometry = [
    { user: true, top: 0, bottom: 50 },
    { user: false, top: 50, bottom: 400 },
    { user: false, top: 400, bottom: 900 },
    { user: true, top: 900, bottom: 950 },
    { user: false, top: 950, bottom: 1500 },
  ];

  it('is nothing while the message is still on screen', () => {
    expect(stickyPrompt(geometry, 0, 300)).toBe(-1);
    expect(stickyPrompt(geometry, 30, 300)).toBe(-1);
  });

  it('is the last message sent above the view once it scrolled out', () => {
    expect(stickyPrompt(geometry, 60, 300)).toBe(0);
    expect(stickyPrompt(geometry, 500, 300)).toBe(0);
    expect(stickyPrompt(geometry, 1000, 300)).toBe(3);
  });

  it('is nothing without a message above', () => {
    expect(stickyPrompt([{ user: false, top: 0, bottom: 500 }], 200, 300)).toBe(-1);
    expect(stickyPrompt([], 0, 300)).toBe(-1);
  });

  it('is nothing once a later message is on screen', () => {
    // Just sent: the new message sits in the view, the old one above it.
    expect(stickyPrompt(geometry, 800, 300)).toBe(-1);
    // Its answer pushed it out: it is the one held now.
    expect(stickyPrompt(geometry, 1000, 300)).toBe(3);
  });
});

describe('describe', () => {
  it('says what was allowed, with the command as a command', () => {
    sequence = 0;
    const line = describeEvent(event('validation.resolved', { validationId: 'v', approved: true, title: 'Bash: go test' }));
    expect(line).toMatchObject({ text: 'Allowed: Bash ', code: 'go test', tone: 'ok' });
    expect(describeEvent(event('validation.resolved', { validationId: 'v', approved: false }))).toMatchObject({
      text: 'Denied.',
    });
    expect(describeEvent(event('job.failed', { error: 'boom' })).text).toBe('Failed: boom');
  });
});
