import { describe, expect, it } from 'vitest';

import {
  draftKey,
  onSend,
  restoredTarget,
  sending,
  startBody,
  startFailed,
  started,
  targetCore,
  targetKey,
  type DraftStart,
  type Target,
} from '../src/conversation/draft';

const start: DraftStart = {
  coreId: 'c1',
  projectId: 'p1',
  projectName: 'threavia',
  backendInstanceId: 'b1',
  backendName: 'laptop',
  workingDirectoryId: 'd1',
  directoryName: 'threavia',
};

describe('startBody', () => {
  it('is what the web client sends: the ids chosen and the message, trimmed', () => {
    expect(startBody(start, '  Look at the build.\n')).toEqual({
      projectId: 'p1',
      backendInstanceId: 'b1',
      workingDirectoryId: 'd1',
      message: 'Look at the build.',
    });
  });

  it('sends a null directory when none was chosen, and never the names', () => {
    const body = startBody({ ...start, workingDirectoryId: null, directoryName: undefined }, 'Go');
    expect(body.workingDirectoryId).toBeNull();
    expect(Object.keys(body).sort()).toEqual(['backendInstanceId', 'message', 'projectId', 'workingDirectoryId']);
  });
});

describe('a draft becoming a Session', () => {
  const draft: Target = { kind: 'draft', start };

  it('starts the Session with its first message', () => {
    expect(onSend(draft)).toBe('start');
    const going = sending(draft);
    expect(going).toEqual({ kind: 'starting', start });
    // Typed while it starts: waits for the Session rather than starting another.
    expect(onSend(going)).toBe('wait');
    const session = started(going, 's1');
    expect(session).toEqual({ kind: 'session', coreId: 'c1', sessionId: 's1' });
    expect(onSend(session)).toBe('post');
  });

  it('stays a draft, with its choice, when the start is refused', () => {
    expect(startFailed(sending(draft))).toEqual(draft);
  });

  it('leaves a Session as it is', () => {
    const session: Target = { kind: 'session', coreId: 'c1', sessionId: 's1' };
    expect(sending(session)).toBe(session);
    expect(started(session, 's2')).toBe(session);
    expect(startFailed(session)).toBe(session);
  });

  it('is found by its Project while a draft, by its Session after, each with its Core', () => {
    expect(targetKey(draft)).toBe(draftKey('c1', 'p1'));
    expect(targetKey(sending(draft))).toBe(draftKey('c1', 'p1'));
    expect(targetKey(started(sending(draft), 's1'))).toBe('c1/s1');
    expect(draftKey('c1', 'p1')).not.toBe(draftKey('c2', 'p1'));
    expect(targetKey({ kind: 'session', coreId: 'c2', sessionId: 's1' })).not.toBe('c1/s1');
    expect(targetCore(draft)).toBe('c1');
  });
});

describe('restoredTarget', () => {
  it('brings a Session back as itself, on its Core', () => {
    expect(restoredTarget({ coreId: 'c2', sessionId: 's1', start }, 'c1')).toEqual({
      kind: 'session',
      coreId: 'c2',
      sessionId: 's1',
    });
  });

  it('brings a draft back with its choice', () => {
    expect(restoredTarget({ sessionId: '', start }, 'c9')).toEqual({ kind: 'draft', start });
  });

  it('puts what an earlier version saved, which names no Core, on the first Core', () => {
    expect(restoredTarget({ sessionId: 's1' }, 'c1')).toEqual({ kind: 'session', coreId: 'c1', sessionId: 's1' });
    const old: Partial<DraftStart> = { ...start };
    delete old.coreId;
    expect(restoredTarget({ sessionId: '', start: old }, 'c1')).toEqual({ kind: 'draft', start: { ...old, coreId: 'c1' } });
    expect(restoredTarget({ sessionId: 's1' }, undefined)).toBeUndefined();
  });

  it('has nothing to show for an empty or older state', () => {
    expect(restoredTarget(undefined, 'c1')).toBeUndefined();
    expect(restoredTarget({ sessionId: '' }, 'c1')).toBeUndefined();
    expect(restoredTarget({ sessionId: '', start: { ...start, backendInstanceId: '' } }, 'c1')).toBeUndefined();
  });
});
