import { describe, expect, it } from 'vitest';

import { AttentionLedgers } from '../src/attention/ledger';
import type { Project } from '../src/api/types';
import {
  accountSessionId,
  artifactPath,
  coreIdOf,
  coreOfAccountSession,
  coreOfScopes,
  coreScope,
  diffPath,
  parseCorePath,
  parseRecordPath,
  recordPath,
  recordRefOf,
  sessionKey,
  sessionRefOf,
} from '../src/cores/refs';
import { credentialKey, cursorKey, hostOf, legacyKeys, migrateLegacy, readCores } from '../src/cores/settings';
import { readProjectRef, resolveCurrentAcross } from '../src/knowledge/model';
import {
  coreMessage,
  coreSections,
  overallState,
  sidebarRoots,
  waitingSummary,
  withCore,
} from '../src/tree/model';

const ids = (...list: string[]) => {
  const queue = [...list];
  return () => queue.shift() ?? 'spare';
};

describe('migrating the one Core of an earlier version', () => {
  it('makes threavia.coreUrl and threavia.project the first Core, named after its host', () => {
    expect(
      migrateLegacy({ cores: undefined, coreUrl: ' https://threavia.example.com/ ', project: ' threavia ' }, ids('a1')),
    ).toEqual({ id: 'a1', name: 'threavia.example.com', url: 'https://threavia.example.com', project: 'threavia' });
  });

  it('leaves the Project out when none was named', () => {
    expect(migrateLegacy({ cores: undefined, coreUrl: 'http://localhost:8080', project: '' }, ids('a1'))).toEqual({
      id: 'a1',
      name: 'localhost:8080',
      url: 'http://localhost:8080',
    });
  });

  it('happens once: a list set at all, even emptied, is not migrated into again', () => {
    expect(migrateLegacy({ cores: [], coreUrl: 'http://localhost:8080', project: '' })).toBeUndefined();
    expect(
      migrateLegacy({ cores: [{ id: 'x', name: 'x', url: 'http://x' }], coreUrl: 'http://y', project: '' }),
    ).toBeUndefined();
  });

  it('has nothing to migrate without a usable URL', () => {
    expect(migrateLegacy({ cores: undefined, coreUrl: '', project: 'p' })).toBeUndefined();
    expect(migrateLegacy({ cores: undefined, coreUrl: 'ftp://x', project: '' })).toBeUndefined();
  });

  it('moves the sign-in and the cursor from the URL to the id', () => {
    expect(legacyKeys('http://localhost:8080', 'a1')).toEqual([
      { from: 'threavia.credential:http://localhost:8080', to: credentialKey('a1') },
      { from: 'threavia.cursor:http://localhost:8080', to: cursorKey('a1') },
    ]);
    expect(credentialKey('a1')).not.toBe(credentialKey('a2'));
  });
});

describe('reading threavia.cores', () => {
  it('keeps a complete list as it is', () => {
    const cores = [
      { id: 'a', name: 'Work', url: 'https://work.example.com', project: 'api' },
      { id: 'b', name: 'Laptop', url: 'http://localhost:8080' },
    ];
    expect(readCores(cores)).toEqual({ cores, changed: false });
  });

  it('completes a hand-written entry, and says it is worth writing back', () => {
    expect(readCores([{ url: 'http://localhost:8090/' }], ids('n1'))).toEqual({
      cores: [{ id: 'n1', name: 'localhost:8090', url: 'http://localhost:8090' }],
      changed: true,
    });
  });

  it('drops an entry without a usable URL and renames a duplicated id', () => {
    const { cores, changed } = readCores(
      [
        { id: 'a', name: 'One', url: 'http://one' },
        { id: 'a', name: 'Two', url: 'http://two' },
        { id: 'c', name: 'Broken', url: 'not a url' },
        'nonsense',
      ],
      ids('n1'),
    );
    expect(changed).toBe(true);
    expect(cores.map((core) => [core.id, core.name])).toEqual([
      ['a', 'One'],
      ['n1', 'Two'],
    ]);
  });

  it('reads nothing from a missing or malformed setting', () => {
    expect(readCores(undefined)).toEqual({ cores: [], changed: false });
    expect(readCores({ url: 'http://x' }).cores).toEqual([]);
  });

  it('names a Core by its host and port', () => {
    expect(hostOf('https://threavia.example.com')).toBe('threavia.example.com');
    expect(hostOf('http://127.0.0.1:8089')).toBe('127.0.0.1:8089');
  });
});

describe('Core-qualified ids', () => {
  const cores = ['first', 'second'];

  it('reads a Session out of every shape a command is handed', () => {
    expect(sessionRefOf({ coreId: 'second', sessionId: 's1' }, cores)).toEqual({ coreId: 'second', sessionId: 's1' });
    expect(sessionRefOf({ coreId: 'second', session: { id: 's1' } }, cores)).toEqual({
      coreId: 'second',
      sessionId: 's1',
    });
    expect(sessionRefOf({ coreId: 'second', item: { sessionId: 's1' } }, cores)).toEqual({
      coreId: 'second',
      sessionId: 's1',
    });
    expect(sessionRefOf('s1', cores, 'second')).toEqual({ coreId: 'second', sessionId: 's1' });
  });

  it('puts an argument of an earlier version, which names no Core, on the first Core', () => {
    expect(sessionRefOf('s1', cores)).toEqual({ coreId: 'first', sessionId: 's1' });
    expect(sessionRefOf({ sessionId: 's1' }, cores)).toEqual({ coreId: 'first', sessionId: 's1' });
    expect(sessionRefOf({ session: { id: 's1' } }, ['only'])).toEqual({ coreId: 'only', sessionId: 's1' });
  });

  it('has nothing to open without a Session or without a Core', () => {
    expect(sessionRefOf('', cores)).toBeUndefined();
    expect(sessionRefOf(undefined, cores)).toBeUndefined();
    expect(sessionRefOf({}, cores)).toBeUndefined();
    expect(sessionRefOf('s1', [])).toBeUndefined();
  });

  it('keeps the same Session apart on two Cores', () => {
    expect(sessionKey({ coreId: 'first', sessionId: 's1' })).not.toBe(sessionKey({ coreId: 'second', sessionId: 's1' }));
  });

  it('reads the Core of a Core command, or none so the command asks', () => {
    expect(coreIdOf('second')).toBe('second');
    expect(coreIdOf({ type: 'core', coreId: 'second' })).toBe('second');
    expect(coreIdOf(undefined)).toBeUndefined();
    expect(coreIdOf({})).toBeUndefined();
  });

  it('reads a Task or a Decision with its Core, or an id alone on the current Core', () => {
    expect(recordRefOf({ coreId: 'second', id: 't1' }, 'first')).toEqual({ coreId: 'second', id: 't1' });
    expect(recordRefOf('t1', 'first')).toEqual({ coreId: 'first', id: 't1' });
    expect(recordRefOf('t1', undefined)).toBeUndefined();
  });
});

describe('Core-qualified document URIs', () => {
  it('round-trips a Task or a Decision, the title last for the tab', () => {
    const path = recordPath('a b', 't1', 'Fix the build.md');
    expect(path).toBe('/a%20b/t1/Fix the build.md');
    expect(parseRecordPath(path)).toEqual({ coreId: 'a b', id: 't1' });
    expect(parseRecordPath(recordPath('second', 't1', 'x.md'))).not.toEqual(parseRecordPath(path));
  });

  it('puts the Core first in a diff and an artifact, keeping the file name last', () => {
    const diff = diffPath('c1', 's1', 42, 'before', 'src/main.go');
    expect(diff).toBe('/c1/s1/42/before/src/main.go');
    expect(diffPath('c1', 's1', 42, 'after', '/abs/file.ts')).toBe('/c1/s1/42/after/abs/file.ts');
    expect(parseCorePath(diff)).toEqual({ coreId: 'c1', rest: ['s1', '42', 'before', 'src', 'main.go'] });
    expect(artifactPath('c1', 'a1', 'report.md')).toBe('/c1/a1/report.md');
    expect(parseCorePath('/')).toBeUndefined();
  });

  it('names a Core in the Accounts API by a scope', () => {
    expect(coreOfScopes([coreScope('c1')])).toBe('c1');
    expect(coreOfScopes([])).toBeUndefined();
    expect(coreOfScopes(undefined)).toBeUndefined();
    expect(coreOfAccountSession(accountSessionId('c1', 'alice@example.com'))).toBe('c1');
  });
});

const project = (id: string, name: string, status: Project['status'] = 'ACTIVE') =>
  ({ id, name, status }) as Project;

describe('the sidebar with one Core or several', () => {
  it('shows one Core’s sections directly, without a Core row', () => {
    expect(sidebarRoots([{ id: 'a', state: 'ready' }])).toEqual({ kind: 'flat', coreId: 'a' });
  });

  it('leaves the welcome to say what to do when the only Core cannot be asked, or there is none', () => {
    expect(sidebarRoots([{ id: 'a', state: 'signedOut' }])).toEqual({ kind: 'empty' });
    expect(sidebarRoots([{ id: 'a', state: 'unreachable' }])).toEqual({ kind: 'empty' });
    expect(sidebarRoots([])).toEqual({ kind: 'empty' });
  });

  it('has a row per Core when there are several, whatever their state', () => {
    expect(
      sidebarRoots([
        { id: 'a', state: 'ready' },
        { id: 'b', state: 'signedOut' },
      ]),
    ).toEqual({ kind: 'cores', coreIds: ['a', 'b'] });
  });

  it('lists a Core’s Waiting, Pinned when something is, and its Projects, its own first', () => {
    const projects = [project('p1', 'web'), project('p2', 'api'), project('p3', 'old', 'ARCHIVED')];
    expect(coreSections(0, projects, 'api')).toEqual([
      { type: 'waiting' },
      { type: 'project', project: projects[1] },
      { type: 'project', project: projects[0] },
    ]);
    expect(coreSections(2, projects, '').map((section) => section.type)).toEqual([
      'waiting',
      'pinned',
      'project',
      'project',
    ]);
  });

  it('says under a Core’s row why it shows nothing, with the way out', () => {
    expect(coreMessage('ready')).toBeUndefined();
    expect(coreMessage('signedOut')?.command).toBe('signIn');
    expect(coreMessage('unreachable')?.command).toBe('refresh');
    expect(coreMessage('connecting')?.command).toBeUndefined();
  });

  it('is ready overall as soon as one Core is', () => {
    expect(overallState([])).toBe('unconfigured');
    expect(overallState(['unreachable', 'ready'])).toBe('ready');
    expect(overallState(['unreachable', 'signedOut'])).toBe('signedOut');
    expect(overallState(['unreachable'])).toBe('unreachable');
  });
});

describe('what waits, across Cores', () => {
  it('counts the sum, and says where in the tooltip when there are several Cores', () => {
    expect(
      waitingSummary([
        { name: 'Work', count: 2 },
        { name: 'Laptop', count: 0 },
        { name: 'Lab', count: 1 },
      ]),
    ).toEqual({ count: 3, tooltip: '3 waiting for you in Threavia\nWork: 2\nLab: 1' });
  });

  it('keeps the one-Core tooltip as it was', () => {
    expect(waitingSummary([{ name: 'Work', count: 2 }])).toEqual({ count: 2, tooltip: '2 waiting for you in Threavia' });
    expect(waitingSummary([]).count).toBe(0);
  });

  it('names the Core in a notification only when there are several', () => {
    expect(withCore('Done · Fix it', 'Work', true)).toBe('Work · Done · Fix it');
    expect(withCore('Done · Fix it', 'Work', false)).toBe('Done · Fix it');
  });
});

describe('the attention ledgers, one per Core', () => {
  it('announces the same request id once on each Core', () => {
    const ledgers = new AttentionLedgers();
    expect(ledgers.take('a', ['r1'])).toEqual(['r1']);
    expect(ledgers.take('b', ['r1'])).toEqual(['r1']);
    expect(ledgers.take('a', ['r1'])).toEqual([]);
  });

  it('does not let one Core’s read forget what another announced', () => {
    const ledgers = new AttentionLedgers();
    ledgers.take('a', ['r1']);
    ledgers.take('b', []);
    expect(ledgers.take('a', ['r1'])).toEqual([]);
  });

  it('survives a restart per Core, adopts an earlier version’s ledger, and forgets a removed Core', () => {
    const first = new AttentionLedgers();
    first.take('a', ['r1']);
    first.adopt('b', ['r2']);
    first.adopt('a', ['ignored']);
    const restarted = new AttentionLedgers(first.saved);
    expect(restarted.saved).toEqual({ a: ['r1'], b: ['r2'] });
    expect(restarted.take('b', ['r2', 'r3'])).toEqual(['r3']);
    restarted.forget('a');
    expect(restarted.take('a', ['r1'])).toEqual(['r1']);
  });
});

describe('the current Project across Cores', () => {
  const work = { coreId: 'work', projects: [project('w1', 'api'), project('w2', 'web')], setting: '' };
  const lab = { coreId: 'lab', projects: [project('l1', 'bench')], setting: 'bench' };

  it('is the one followed, wherever it is', () => {
    expect(resolveCurrentAcross([work, lab], { coreId: 'work', projectId: 'w2' })).toEqual({
      coreId: 'work',
      project: work.projects[1],
    });
  });

  it('is never a Project of the same id on another Core', () => {
    const twin = { coreId: 'twin', projects: [project('w2', 'twin')], setting: '' };
    expect(resolveCurrentAcross([twin, work], { coreId: 'work', projectId: 'w2' })?.coreId).toBe('work');
  });

  it('falls back to the first Project a Core names, then to the first Project at all', () => {
    expect(resolveCurrentAcross([work, lab], undefined)).toEqual({ coreId: 'lab', project: lab.projects[0] });
    expect(resolveCurrentAcross([work, { ...lab, setting: '' }], { coreId: 'gone', projectId: 'x' })).toEqual({
      coreId: 'work',
      project: work.projects[0],
    });
    expect(resolveCurrentAcross([], undefined)).toBeUndefined();
  });

  it('reads back only a well-formed saved Project', () => {
    expect(readProjectRef({ coreId: 'a', projectId: 'p' })).toEqual({ coreId: 'a', projectId: 'p' });
    expect(readProjectRef({ projectId: 'p' })).toBeUndefined();
    expect(readProjectRef('p')).toBeUndefined();
  });
});
