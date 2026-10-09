import { describe, expect, it } from 'vitest';

import { candidatesFor, parsePathRef } from '../src/workspace/paths';

describe('parsePathRef', () => {
  it('reads a path, with a line and a column when written', () => {
    expect(parsePathRef('src/a.ts')).toEqual({ path: 'src/a.ts', line: undefined, column: undefined });
    expect(parsePathRef('src/a.ts:12')).toEqual({ path: 'src/a.ts', line: 12, column: undefined });
    expect(parsePathRef('src/a.ts:12:4')).toEqual({ path: 'src/a.ts', line: 12, column: 4 });
    expect(parsePathRef('README.md#L7-L9')).toEqual({ path: 'README.md', line: 7, column: undefined });
    expect(parsePathRef('`go.mod`,')).toEqual({ path: 'go.mod', line: undefined, column: undefined });
    expect(parsePathRef('C:\\work\\a.ts')?.path).toBe('C:/work/a.ts');
  });

  it('refuses what is not a file name', () => {
    for (const text of ['make test', 'https://example.com/a.ts', '1.2.3', 'v0.6', 'hello', '', '..', '.env.']) {
      expect(parsePathRef(text), text).toBeUndefined();
    }
  });
});

describe('candidatesFor', () => {
  const folders = ['/home/me/threavia', '/home/me/other'];

  it('is the file itself when an absolute path is inside a folder', () => {
    expect(candidatesFor('/home/me/threavia/web/ui/a.ts', folders)[0]).toEqual({ folder: 0, relative: 'web/ui/a.ts' });
  });

  it('finds an agent’s absolute path by its tail, most specific first', () => {
    const candidates = candidatesFor('/home/agent/src/threavia/web/a.ts', folders);
    const zero = candidates.filter((candidate) => candidate.folder === 0).map((candidate) => candidate.relative);
    expect(zero).toEqual([
      'home/agent/src/threavia/web/a.ts',
      'agent/src/threavia/web/a.ts',
      'src/threavia/web/a.ts',
      'threavia/web/a.ts',
      'web/a.ts',
    ]);
  });

  it('tries a relative path in every folder, never as a bare file name when it had a directory', () => {
    expect(candidatesFor('./pkg/a.go', folders)).toEqual([
      { folder: 0, relative: 'pkg/a.go' },
      { folder: 1, relative: 'pkg/a.go' },
    ]);
    expect(candidatesFor('a.go', ['/w'])).toEqual([{ folder: 0, relative: 'a.go' }]);
  });

  it('refuses to climb out of a folder', () => {
    expect(candidatesFor('../secrets/key.pem', folders)).toEqual([]);
    expect(candidatesFor('/home/me/threavia/../x/a.ts', folders)).toEqual([]);
  });

  it('matches a Windows drive whatever its case', () => {
    expect(candidatesFor('C:/work/repo/a.ts', ['/c:/work/repo'])[0]).toEqual({ folder: 0, relative: 'a.ts' });
  });
});
