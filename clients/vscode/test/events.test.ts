import { describe, expect, it, vi } from 'vitest';

import { EventBus, effectsOf, type Effect } from '../src/api/events';
import type { Event } from '../src/api/types';

const at = Date.parse('2026-10-09T12:00:00Z');
const event = (type: string, extra: Partial<Event> = {}): Event => ({
  id: 'e',
  sequence: 1,
  timestamp: new Date(at).toISOString(),
  type,
  sessionId: 's1',
  ...extra,
});

const kinds = (effects: Effect[]) => effects.map((effect) => effect.kind);

describe('what an event changes', () => {
  it('re-reads what waits when a request arrives', () => {
    expect(kinds(effectsOf(event('validation.requested'), at))).toEqual(['attention', 'sessions', 'snapshot']);
  });

  it('names exactly what a resolution resolved', () => {
    const effects = effectsOf(event('validation.resolved', { payload: { validationId: 'v1', approved: true } }), at);
    expect(effects).toContainEqual({ kind: 'resolved', sessionId: 's1', validationId: 'v1', userInputId: undefined });
    const answered = effectsOf(event('user_input.resolved', { payload: { requestId: 'u1', value: 'yes' } }), at);
    expect(answered).toContainEqual({ kind: 'resolved', sessionId: 's1', validationId: undefined, userInputId: 'u1' });
  });

  it('refreshes the lists on a pin, a rename or an archive', () => {
    for (const type of ['session.pinned', 'session.renamed', 'session.archived']) {
      expect(kinds(effectsOf(event(type), at))).toContain('sessions');
    }
  });

  it('tells an ended Job as news only while it is fresh', () => {
    const done = event('job.completed', { payload: { summary: 'Tests pass' } });
    expect(effectsOf(done, at + 60_000)).toContainEqual({
      kind: 'jobEnded',
      sessionId: 's1',
      failed: false,
      text: 'Tests pass',
    });
    expect(kinds(effectsOf(done, at + 3 * 60_000))).not.toContain('jobEnded');
    expect(kinds(effectsOf(done, at))).toContain('repository');
    const failed = effectsOf(event('job.failed', { payload: { error: 'boom' } }), at);
    expect(failed).toContainEqual({ kind: 'jobEnded', sessionId: 's1', failed: true, text: 'boom' });
  });

  it('ignores what no view shows', () => {
    expect(effectsOf(event('agent.message'), at)).toEqual([]);
  });
});

describe('the bus', () => {
  it('reaches every listener even when one fails, and stops after dispose', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const bus = new EventBus();
    const seen: string[] = [];
    bus.on('effect', () => {
      throw new Error('broken view');
    });
    const subscription = bus.on('effect', (effect) => seen.push(effect.kind));
    bus.publish(event('session.renamed'), at);
    subscription.dispose();
    bus.publish(event('session.renamed'), at);
    expect(seen).toEqual(['sessions', 'snapshot']);
  });
});
