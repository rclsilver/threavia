/** The tools whose input is a change to a file, and nothing else. */
const FILE_TOOLS = new Set(['Edit', 'MultiEdit', 'Write']);

/** One line of a diff: kept, removed or added. */
export type Line = { kind: ' ' | '-' | '+'; text: string };

/** One replacement inside a file, as Edit and MultiEdit describe it. */
type Replacement = { before: string; after: string; all: boolean };

/** What a file tool is about to do, read from its input. */
export type FileChange =
  | { kind: 'edit'; path: string; edits: Replacement[] }
  | { kind: 'write'; path: string; content: string };

/**
 * Reads a file change out of a tool input, or nothing when the call is not one.
 *
 * The input is the provider's, so every field is checked rather than assumed:
 * a shape this client does not know falls back to the plain rendering instead
 * of a diff of empty strings.
 */
export function fileChangeOf(tool: string, input: Record<string, unknown>): FileChange | null {
  if (!FILE_TOOLS.has(tool)) return null;
  const path = typeof input.file_path === 'string' ? input.file_path : '';
  if (!path) return null;

  if (tool === 'Write') {
    return typeof input.content === 'string' ? { kind: 'write', path, content: input.content } : null;
  }

  const raw = tool === 'MultiEdit' ? input.edits : [input];
  if (!Array.isArray(raw)) return null;
  const edits: Replacement[] = [];
  for (const edit of raw as Record<string, unknown>[]) {
    if (typeof edit?.old_string !== 'string' || typeof edit.new_string !== 'string') return null;
    edits.push({ before: edit.old_string, after: edit.new_string, all: edit.replace_all === true });
  }
  return { kind: 'edit', path, edits };
}

/** Lines added and removed, for a one-line summary. */
export function changeCounts(change: FileChange): { added: number; removed: number } {
  if (change.kind === 'write') return { added: lines(change.content).length, removed: 0 };
  let added = 0;
  let removed = 0;
  for (const edit of change.edits) {
    for (const line of diffLines(edit.before, edit.after)) {
      if (line.kind === '+') added++;
      if (line.kind === '-') removed++;
    }
  }
  return { added, removed };
}

function lines(text: string): string[] {
  if (text === '') return [];
  return text.replace(/\n$/, '').split('\n');
}

/** Beyond this many cells the table costs more than the diff is worth. */
const MAX_CELLS = 250_000;

/**
 * A line diff by longest common subsequence.
 *
 * The strings of an edit are a few dozen lines at most, which is what makes
 * the plain quadratic table the right tool rather than a library. A pair too
 * large for it is shown as everything removed, then everything added: still
 * correct, only less precise.
 */
export function diffLines(before: string, after: string): Line[] {
  const a = lines(before);
  const b = lines(after);
  if (a.length * b.length > MAX_CELLS) {
    return [
      ...a.map((text) => ({ kind: '-' as const, text })),
      ...b.map((text) => ({ kind: '+' as const, text })),
    ];
  }

  // common[i][j]: length of the longest common subsequence of a[i:] and b[j:].
  const common: number[][] = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      common[i][j] =
        a[i] === b[j] ? common[i + 1][j + 1] + 1 : Math.max(common[i + 1][j], common[i][j + 1]);
    }
  }

  const out: Line[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: ' ', text: a[i] });
      i++;
      j++;
    } else if (common[i + 1][j] >= common[i][j + 1]) {
      out.push({ kind: '-', text: a[i++] });
    } else {
      out.push({ kind: '+', text: b[j++] });
    }
  }
  while (i < a.length) out.push({ kind: '-', text: a[i++] });
  while (j < b.length) out.push({ kind: '+', text: b[j++] });
  return out;
}

/** The language a path is written in, as the highlighter names it. */
export function languageOf(path: string): string {
  const name = path.split('/').pop() ?? '';
  if (name === 'Dockerfile') return 'docker';
  if (name === 'Makefile') return 'make';
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1) : '';
}
