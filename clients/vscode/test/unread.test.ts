import { describe, expect, it } from 'vitest';

import { readUpTo } from '../../shared/sessions';

const event = (sequence: number, type: string) => ({ sequence, type });

describe('how far a shown session is marked read', () => {
  it('marks up to the last event once something the agent said is on screen', () => {
    const events = [event(3, 'user.message'), event(5, 'agent.message'), event(8, 'tool.started')];
    expect(readUpTo(2, events)).toBe(8);
  });

  it('marks a failed job read too, as it is what the person comes back for', () => {
    expect(readUpTo(2, [event(4, 'job.failed')])).toBe(4);
  });

  it('does not call Core for the person\'s own messages or the tools the agent runs', () => {
    const events = [event(5, 'agent.message'), event(6, 'user.message'), event(7, 'tool.completed'), event(9, 'session.read')];
    expect(readUpTo(5, events)).toBeUndefined();
    expect(readUpTo(0, [])).toBeUndefined();
  });
});
