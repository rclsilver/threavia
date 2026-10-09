import { describe, expect, it } from 'vitest';

import { apply, gapLine, parseDiff, reconstruct } from '../src/diff/unified';

const lines = (count: number, prefix = 'line') => Array.from({ length: count }, (_, index) => `${prefix} ${index + 1}`);
const text = (items: string[]) => `${items.join('\n')}\n`;

const ADDED = `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3b18e51
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`;

const DELETED = `diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-goodbye
-world
`;

/** Lines 1–20, with line 3 changed and line 15 removed, in two hunks. */
const before20 = lines(20);
const after20 = before20.map((line, index) => (index === 2 ? 'line three' : line)).filter((_, index) => index !== 14);
const TWO_HUNKS = `--- a/f.txt
+++ b/f.txt
@@ -1,6 +1,6 @@
 line 1
 line 2
-line 3
+line three
 line 4
 line 5
 line 6
@@ -12,7 +12,6 @@
 line 12
 line 13
 line 14
-line 15
 line 16
 line 17
 line 18
`;

describe('parseDiff', () => {
  it('reads hunks by their counts, so a removed "-- x" line is not a header', () => {
    const parsed = parseDiff(`--- a/x.sql
+++ b/x.sql
@@ -1,2 +1,2 @@
--- a comment
+-- another comment
 select 1;
`);
    expect(parsed.hunks).toHaveLength(1);
    expect(parsed.hunks[0].lines).toEqual([
      { kind: '-', text: '-- a comment' },
      { kind: '+', text: '-- another comment' },
      { kind: ' ', text: 'select 1;' },
    ]);
  });

  it('notes a side that ends without a newline', () => {
    const parsed = parseDiff(`@@ -1 +1 @@
-a
\\ No newline at end of file
+a
`);
    expect(parsed.oldNoNewline).toBe(true);
    expect(parsed.newNoNewline).toBe(false);
  });

  it('knows a binary file', () => {
    expect(parseDiff('Binary files a/x.png and b/x.png differ\n').binary).toBe(true);
  });
});

describe('reconstruct', () => {
  it('rebuilds an added file whole', () => {
    expect(reconstruct(ADDED)).toEqual({ kind: 'complete', before: '', after: 'hello\nworld\n' });
  });

  it('rebuilds a deleted file whole', () => {
    expect(reconstruct(DELETED)).toEqual({ kind: 'complete', before: 'goodbye\nworld\n', after: '' });
  });

  it('keeps an added file without a final newline as it is', () => {
    const diff = `--- /dev/null
+++ b/n.txt
@@ -0,0 +1,2 @@
+a
+b
\\ No newline at end of file
`;
    expect(reconstruct(diff)).toEqual({ kind: 'complete', before: '', after: 'a\nb' });
  });

  it('rebuilds the file before the change from the workspace copy after it', () => {
    expect(reconstruct(TWO_HUNKS, { local: text(after20) })).toEqual({
      kind: 'complete',
      before: text(before20),
      after: text(after20),
    });
  });

  it('rebuilds the file after the change from a workspace copy not yet updated', () => {
    expect(reconstruct(TWO_HUNKS, { local: text(before20) })).toEqual({
      kind: 'complete',
      before: text(before20),
      after: text(after20),
    });
  });

  it('shows the changed regions only when the workspace copy is neither side', () => {
    const result = reconstruct(TWO_HUNKS, { local: text(lines(20, 'other')) });
    expect(result.kind).toBe('partial');
    if (result.kind !== 'partial') return;
    const before = result.before.split('\n');
    const after = result.after.split('\n');
    // Lines 7 to 11 are between the hunks: said, on both sides alike.
    expect(before[6]).toBe(gapLine(5));
    expect(after[6]).toBe(gapLine(5));
    expect(before).toContain('line 15');
    expect(after).not.toContain('line 15');
    expect(after).toContain('line three');
  });

  it('says where the change starts when the first hunk is not at the top', () => {
    const result = reconstruct(`@@ -10,2 +10,2 @@
 keep
-old
+new
`);
    expect(result).toEqual({
      kind: 'partial',
      before: `${gapLine(9)}\nkeep\nold\n`,
      after: `${gapLine(9)}\nkeep\nnew\n`,
    });
  });

  it('handles a change to the last line, without a newline at the end', () => {
    const diff = `@@ -1,2 +1,2 @@
 a
-b
\\ No newline at end of file
+c
\\ No newline at end of file
`;
    expect(reconstruct(diff, { local: 'a\nc' })).toEqual({ kind: 'complete', before: 'a\nb', after: 'a\nc' });
  });

  it('handles a newline added at the end', () => {
    const diff = `@@ -1,2 +1,2 @@
 a
-b
\\ No newline at end of file
+b
`;
    expect(reconstruct(diff, { local: 'a\nb\n' })).toEqual({ kind: 'complete', before: 'a\nb', after: 'a\nb\n' });
  });

  it('does not trust a workspace copy that disagrees about the end of the file', () => {
    const diff = `@@ -1,2 +1,2 @@
 a
-b
+c
\\ No newline at end of file
`;
    const parsed = parseDiff(diff);
    expect(apply(parsed, 'a\nc\n', 'new')).toBeUndefined();
  });

  it('says why it cannot compare', () => {
    expect(reconstruct('', {})).toMatchObject({ kind: 'none', reason: 'No difference in content.' });
    expect(reconstruct(TWO_HUNKS, { truncated: true })).toMatchObject({ kind: 'none' });
    expect(reconstruct('', { binary: true })).toMatchObject({ kind: 'none' });
  });
});
