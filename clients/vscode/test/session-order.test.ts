import { describe, expect, it } from 'vitest';

import { sessionsByTitle } from '../../shared/sessions';

describe('alphabetical session lists shared by Web and VS Code', () => {
  it('compares case and accents together, and orders numbered titles naturally', () => {
    const sessions = [
      { id: 'z', title: 'zèbre' },
      { id: '10', title: 'Session 10' },
      { id: 'e', title: 'Évolution' },
      { id: 'a', title: 'alpha' },
      { id: '2', title: 'session 2' },
      { id: 'b', title: 'Beta' },
    ];
    expect(sessionsByTitle(sessions).map((s) => s.id)).toEqual(['a', 'b', 'e', '2', '10', 'z']);
  });

  it('uses the displayed fallback and stable ids for equal titles without changing the cache', () => {
    const sessions = Object.freeze([
      Object.freeze({ id: 'b', title: 'ÉCHO' }),
      Object.freeze({ id: 'untitled', title: '' }),
      Object.freeze({ id: 'a', title: 'echo' }),
      Object.freeze({ id: 'last', title: 'Zulu' }),
    ]);
    expect(sessionsByTitle(sessions).map((s) => s.id)).toEqual(['a', 'b', 'untitled', 'last']);
    expect(sessions.map((s) => s.id)).toEqual(['b', 'untitled', 'a', 'last']);
    expect(sessionsByTitle([...sessions].reverse()).map((s) => s.id)).toEqual(['a', 'b', 'untitled', 'last']);
    expect(sessionsByTitle([{ id: 'missing' }, { id: 'a', title: 'Alpha' }]).map((s) => s.id)).toEqual(['a', 'missing']);
  });

  it('reorders a renamed session while retaining archived and pinned entries supplied by the caller', () => {
    const sessions = [
      { id: 'a', title: 'Alpha', status: 'ARCHIVED' },
      { id: 'z', title: 'Zulu', pinnedAt: '2026-10-23T00:00:00Z' },
    ];
    expect(sessionsByTitle(sessions).map((s) => s.id)).toEqual(['a', 'z']);
    const renamed = sessions.map((s) => s.id === 'z' ? { ...s, title: 'Aardvark' } : s);
    expect(sessionsByTitle(renamed).map((s) => s.id)).toEqual(['z', 'a']);
    expect(sessions[1].title).toBe('Zulu');
  });
});
