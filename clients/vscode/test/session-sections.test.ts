import { describe, expect, it } from 'vitest';

import { matchesSearch, sessionSections, snoozeChoices } from '../../shared/sessions';

const at = (minutes: number) => new Date(Date.UTC(2026, 9, 31, 12, minutes)).toISOString();
const session = (id: string, minutes: number, extra: Partial<{ title: string; activeJobStatus: string; unread: boolean }> = {}) =>
  ({ id, title: id, updatedAt: at(minutes), ...extra });

describe('session sections', () => {
  const sessions = [
    session('old-idle', 1),
    session('asks', 5, { activeJobStatus: 'WAITING_INPUT' }),
    session('approves', 2, { activeJobStatus: 'RUNNING' }),
    session('works', 8, { activeJobStatus: 'RUNNING' }),
    session('queued', 3, { activeJobStatus: 'QUEUED' }),
    session('finished', 7, { unread: true }),
    session('new-idle', 9),
  ];

  it('puts what waits first, then what works, then what finished unseen, then the rest by activity', () => {
    const sections = sessionSections(sessions, { waiting: (id) => id === 'approves' });
    expect(sections.map(({ key, sessions }) => [key, sessions.map((entry) => entry.id)])).toEqual([
      // The longest wait first.
      ['needs', ['approves', 'asks']],
      ['running', ['works', 'queued']],
      ['unseen', ['finished']],
      ['recent', ['new-idle', 'old-idle']],
      ['snoozed', []],
    ]);
  });

  it('counts the open session as read and keeps empty sections', () => {
    const sections = sessionSections([session('finished', 7, { unread: true })], { current: 'finished' });
    expect(sections.map(({ key, sessions }) => [key, sessions.length])).toEqual([['needs', 0], ['running', 0], ['unseen', 0], ['recent', 1], ['snoozed', 0]]);
  });

  it('searches titles by every word, without case or accents', () => {
    expect(matchesSearch('Déploiement du chart Ingress', 'ingress deploiement')).toBe(true);
    expect(matchesSearch('Déploiement du chart Ingress', 'ingress backup')).toBe(false);
    expect(matchesSearch(undefined, 'untitled')).toBe(true);
    const found = sessionSections([session('Fix the ingress', 1), session('Backups', 2)], { query: 'INGRESS' });
    expect(found.flatMap((section) => section.sessions.map((entry) => entry.id))).toEqual(['Fix the ingress']);
  });
});

describe('snooze', () => {
  const now = Date.UTC(2026, 9, 31, 12, 30);
  it('puts a snoozed session last, brings it back unread, and never hides a decision', () => {
    const sessions = [
      session('later', 1, { snoozed: true, snoozedUntil: new Date(now + 3600_000).toISOString() } as never),
      session('change', 2, { snoozed: true, snoozeUntilChange: true } as never),
      session('back', 3, { snoozed: true, snoozedUntil: new Date(now - 60_000).toISOString() } as never),
      session('woke', 4, { snoozeUntilChange: true } as never),
      session('asks', 5, { snoozed: true, snoozeUntilChange: true, activeJobStatus: 'WAITING_VALIDATION' } as never),
    ];
    const sections = sessionSections(sessions, { now });
    expect(sections.map(({ key, sessions }) => [key, sessions.map((entry) => entry.id)])).toEqual([
      ['needs', ['asks']],
      ['running', []],
      ['unseen', ['woke', 'back']],
      ['recent', []],
      ['snoozed', ['later', 'change']],
    ]);
  });

  it('offers the evening only while there is one left, and next Monday a week away on a Monday', () => {
    const afternoon = new Date(2026, 10, 2, 14, 0); // a Monday
    const choices = snoozeChoices(afternoon);
    expect(choices.map((choice) => choice.id)).toEqual(['hour', 'evening', 'tomorrow', 'week', 'change']);
    expect(choices.find((choice) => choice.id === 'week')?.until?.getDate()).toBe(9);
    expect(choices.find((choice) => choice.id === 'evening')?.until?.getHours()).toBe(18);
    expect(snoozeChoices(new Date(2026, 10, 4, 19, 0)).map((choice) => choice.id)).toEqual(['hour', 'tomorrow', 'week', 'change']);
    expect(snoozeChoices(new Date(2026, 10, 4, 19, 0)).find((choice) => choice.id === 'week')?.until?.getDate()).toBe(9);
  });
});

describe('a job held by its backend', () => {
  it('shows as a warning in the VS Code tree, not as work under way', async () => {
    const { sessionState, stateIcon } = await import('../src/tree/model');
    const state = sessionState({ id: 's', activeJobStatus: 'WAITING_BACKEND' } as never, new Map());
    expect(state).toBe('blocked');
    expect(stateIcon(state)).toMatchObject({ id: 'warning', color: 'list.warningForeground' });
  });
});

describe('a job waiting for its backend', () => {
  it('can be interrupted, and says Next step cannot be taken now', async () => {
    const { deliveriesFor, heldDeliveriesFor } = await import('../src/conversation/state');
    const backend = { features: ['JOB_INPUT_NEXT', 'JOB_INPUT_NOW'] } as never;
    const waiting = { status: 'WAITING_BACKEND' } as never;
    expect(deliveriesFor(waiting, backend)).toEqual(['NOW']);
    expect(heldDeliveriesFor(waiting, backend)).toEqual(['NEXT']);
    expect(heldDeliveriesFor({ status: 'RUNNING' } as never, backend)).toEqual([]);
  });
});
