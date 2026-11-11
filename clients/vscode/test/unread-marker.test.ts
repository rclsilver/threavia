import { describe, expect, it } from 'vitest';

import { withUnreadMarker, type Row } from '../src/conversation/timeline';
import { firstUnread, projectsByName } from '../../shared/sessions';

const event = (sequence: number, type: string) => ({ sequence, type });
const row = (key: number): Row => ({ kind: 'event', key, event: { id: `e${key}`, sequence: key, timestamp: '', type: 'agent.message' } as never });

describe('firstUnread', () => {
  it('is the first agent message or failure past what was read', () => {
    const events = [event(1, 'user.message'), event(2, 'agent.message'), event(3, 'tool.started'), event(4, 'agent.message'), event(5, 'job.failed')];
    expect(firstUnread(2, events)).toBe(4);
    expect(firstUnread(0, events)).toBe(2);
  });
  it('is nothing when what the person comes back for was read', () => {
    expect(firstUnread(4, [event(1, 'user.message'), event(4, 'agent.message'), event(5, 'tool.started')])).toBeUndefined();
  });
});

describe('withUnreadMarker', () => {
  const day: Row = { kind: 'day', key: -1, label: 'Today' };
  it('draws the line before the first row at or past the place', () => {
    const out = withUnreadMarker([day, row(1), row(2), row(5)], 4);
    expect(out.map((r) => r.kind)).toEqual(['day', 'event', 'event', 'unread', 'event']);
  });
  it('draws nothing without a place, or when everything is unread', () => {
    const rows = [day, row(2), row(3)];
    expect(withUnreadMarker(rows, undefined)).toBe(rows);
    expect(withUnreadMarker(rows, 2)).toBe(rows);
    expect(withUnreadMarker(rows, 9)).toBe(rows);
  });
});

describe('projectsByName', () => {
  it('lists them alphabetically, ignoring case and accents, without touching the input', () => {
    const projects = [{ id: '1', name: 'zeta' }, { id: '2', name: 'Éclair' }, { id: '3', name: 'alpha' }, { id: '4', name: 'Alpha' }];
    expect(projectsByName(projects).map((p) => p.id)).toEqual(['3', '4', '2', '1']);
    expect(projects[0].id).toBe('1');
  });
});
