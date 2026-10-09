import { describe, expect, it } from 'vitest';

import { askMessage, fenceFor, lineRange, locationOf } from '../src/ask/message';

describe('lineRange', () => {
  it('is 1-based and leaves out a line the selection only touches at its start', () => {
    expect(lineRange({ line: 11 }, { line: 29, character: 4 })).toEqual({ startLine: 12, endLine: 30 });
    expect(lineRange({ line: 11 }, { line: 30, character: 0 })).toEqual({ startLine: 12, endLine: 30 });
    expect(lineRange({ line: 4 }, { line: 4, character: 0 })).toEqual({ startLine: 5, endLine: 5 });
  });
});

describe('askMessage', () => {
  it('puts the instruction first, then the code fenced with its place', () => {
    const message = askMessage('  Why is this slow?  ', {
      path: 'src/a.ts',
      startLine: 12,
      endLine: 14,
      languageId: 'typescript',
      code: 'for (;;) {\n  work();\n}\n',
    });
    expect(message).toBe('Why is this slow?\n\n`src/a.ts:12-14`:\n\n```typescript\nfor (;;) {\n  work();\n}\n```\n');
  });

  it('names one line once, and a whole file as such', () => {
    expect(locationOf({ path: 'a.go', startLine: 3, endLine: 3 })).toBe('a.go:3');
    const message = askMessage('Review', {
      path: 'Makefile',
      startLine: 1,
      endLine: 2,
      languageId: 'makefile',
      code: 'all:\n\tgo build\n',
      wholeFile: true,
    });
    expect(message).toContain('`Makefile (the whole file)`');
    expect(message).toContain('```make\n');
  });

  it('fences code that holds a fence itself with a longer one', () => {
    expect(fenceFor('no ticks')).toBe('```');
    expect(fenceFor('a ```js\nfence``` and ````four````')).toBe('`````');
    const message = askMessage('Explain', {
      path: 'README.md',
      startLine: 1,
      endLine: 3,
      languageId: 'markdown',
      code: '```sh\nmake\n```',
    });
    expect(message).toContain('````markdown\n```sh\nmake\n```\n````\n');
  });

  it('leaves the language out for plain text', () => {
    expect(askMessage('x', { path: 'a.txt', startLine: 1, endLine: 1, languageId: 'plaintext', code: 'hi' })).toContain(
      '```\nhi\n```',
    );
  });
});
