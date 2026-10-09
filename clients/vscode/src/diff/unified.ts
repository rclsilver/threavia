/**
 * Turning the unified diff a backend computed into the two documents the
 * editor's diff view compares.
 *
 * Core keeps no file, and the backend answers with a diff of one file, not
 * the file: the documents are rebuilt from the hunks. A file added or deleted
 * is whole in its diff. A file modified is only whole when the copy open in
 * this workspace is one of its two sides, which is checked line by line before
 * it is trusted; otherwise only the changed regions are shown, the lines
 * between them stated rather than invented. Nothing here imports `vscode`.
 */

export interface DiffLine {
  kind: ' ' | '-' | '+';
  text: string;
}

export interface Hunk {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}

export interface ParsedDiff {
  hunks: Hunk[];
  added: boolean;
  deleted: boolean;
  /** Set when git said the file is binary: it has no lines to compare. */
  binary: boolean;
  /** Whether each side ends without a newline, as "\ No newline at end of file" says. */
  oldNoNewline: boolean;
  newNoNewline: boolean;
}

const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

/**
 * Reads a unified diff as git prints it.
 *
 * Inside a hunk, lines are counted against its header rather than recognised
 * by their first characters: a removed line that read `-- note` is printed
 * `--- note`, which is also how a file header starts.
 */
export function parseDiff(text: string): ParsedDiff {
  const parsed: ParsedDiff = {
    hunks: [],
    added: false,
    deleted: false,
    binary: false,
    oldNoNewline: false,
    newNoNewline: false,
  };
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  if (lines.at(-1) === '') lines.pop();

  let hunk: Hunk | undefined;
  let oldLeft = 0;
  let newLeft = 0;
  let last: DiffLine['kind'] | undefined;

  for (const line of lines) {
    if (hunk && (oldLeft > 0 || newLeft > 0)) {
      if (line.startsWith('\\')) {
        markNoNewline(parsed, last);
        continue;
      }
      // Some tools strip the space of an empty context line.
      const kind = (line === '' ? ' ' : line[0]) as DiffLine['kind'];
      if (kind !== ' ' && kind !== '-' && kind !== '+') {
        // The hunk ends early: a truncated diff. What was read stays.
        hunk = undefined;
      } else {
        hunk.lines.push({ kind, text: line.slice(1) });
        if (kind !== '+') oldLeft--;
        if (kind !== '-') newLeft--;
        last = kind;
        continue;
      }
    }
    if (line.startsWith('\\')) {
      markNoNewline(parsed, last);
      continue;
    }
    const header = HUNK.exec(line);
    if (header) {
      hunk = {
        oldStart: Number(header[1]),
        oldLines: header[2] === undefined ? 1 : Number(header[2]),
        newStart: Number(header[3]),
        newLines: header[4] === undefined ? 1 : Number(header[4]),
        lines: [],
      };
      parsed.hunks.push(hunk);
      oldLeft = hunk.oldLines;
      newLeft = hunk.newLines;
      continue;
    }
    if (line.startsWith('new file mode') || line === '--- /dev/null') parsed.added = true;
    else if (line.startsWith('deleted file mode') || line === '+++ /dev/null') parsed.deleted = true;
    else if (line.startsWith('Binary files ') || line === 'GIT binary patch') parsed.binary = true;
  }

  // A diff without its headers still says it in its only hunk.
  const only = parsed.hunks.length === 1 ? parsed.hunks[0] : undefined;
  if (only?.oldStart === 0 && only.oldLines === 0) parsed.added = true;
  if (only?.newStart === 0 && only.newLines === 0) parsed.deleted = true;
  return parsed;
}

function markNoNewline(parsed: ParsedDiff, last: DiffLine['kind'] | undefined) {
  if (last === '-' || last === ' ') parsed.oldNoNewline = true;
  if (last === '+' || last === ' ') parsed.newNoNewline = true;
}

export type Reconstruction =
  /** Both sides whole: the editor shows the file. */
  | { kind: 'complete'; before: string; after: string }
  /** Only the changed regions, the lines between them said, not shown. */
  | { kind: 'partial'; before: string; after: string }
  /** Nothing to compare; `reason` says why, for the person reading the raw diff. */
  | { kind: 'none'; reason: string };

/** The line standing for unchanged lines a diff does not carry. */
export function gapLine(count: number): string {
  return `⋯ ${count} unchanged ${count === 1 ? 'line' : 'lines'} not shown`;
}

/**
 * The two sides of a diff.
 *
 * `local` is the file as this workspace has it, when it has it: if it is the
 * side after the change (the work is pulled) or the side before (it is not
 * yet), the other side is derived from it and both are whole.
 */
export function reconstruct(diff: string, options: { truncated?: boolean; binary?: boolean; local?: string } = {}): Reconstruction {
  const parsed = parseDiff(diff);
  if (options.binary || parsed.binary) return { kind: 'none', reason: 'A binary file: there are no lines to compare.' };
  if (options.truncated) return { kind: 'none', reason: 'Cut short: the diff is too long to compare whole.' };
  if (parsed.hunks.length === 0) return { kind: 'none', reason: 'No difference in content.' };

  if (parsed.added || parsed.deleted) {
    const side = parsed.added ? 'new' : 'old';
    const text = sideOf(parsed.hunks, side).join('\n');
    const whole = text + (text && !(parsed.added ? parsed.newNoNewline : parsed.oldNoNewline) ? '\n' : '');
    return parsed.added
      ? { kind: 'complete', before: '', after: whole }
      : { kind: 'complete', before: whole, after: '' };
  }

  if (options.local !== undefined) {
    const before = apply(parsed, options.local, 'new');
    if (before !== undefined) return { kind: 'complete', before, after: options.local };
    const after = apply(parsed, options.local, 'old');
    if (after !== undefined) return { kind: 'complete', before: options.local, after };
  }

  return { kind: 'partial', ...regions(parsed) };
}

/** The lines one side of the hunks holds, in order. */
function sideOf(hunks: Hunk[], side: 'old' | 'new'): string[] {
  const drop = side === 'old' ? '+' : '-';
  return hunks.flatMap((hunk) => hunk.lines.filter((line) => line.kind !== drop).map((line) => line.text));
}

function splitLines(text: string): { lines: string[]; newline: boolean } {
  const newline = text.endsWith('\n');
  const lines = text.split('\n').map((line) => line.replace(/\r$/, ''));
  if (newline) lines.pop();
  return { lines: text === '' ? [] : lines, newline };
}

/**
 * Derives the other side from a file that is one side of the diff, or nothing
 * when the file does not match it exactly where the hunks say.
 *
 * `from` names the side `text` is: 'new' gives the file before the change,
 * 'old' the file after it.
 */
export function apply(parsed: ParsedDiff, text: string, from: 'old' | 'new'): string | undefined {
  const { lines, newline } = splitLines(text);
  const keep = from === 'new' ? '+' : '-';
  const out: string[] = [];
  let position = 0;
  let reachesEnd = false;

  for (const hunk of parsed.hunks) {
    const start = from === 'new' ? hunk.newStart : hunk.oldStart;
    const count = from === 'new' ? hunk.newLines : hunk.oldLines;
    // A side with no lines in a hunk names the line it comes after.
    const at = count === 0 ? start : start - 1;
    if (at < position || at > lines.length) return undefined;
    out.push(...lines.slice(position, at));
    let cursor = at;
    for (const line of hunk.lines) {
      if (line.kind === ' ' || line.kind === keep) {
        if (lines[cursor] !== line.text.replace(/\r$/, '')) return undefined;
        cursor++;
      }
      if (line.kind !== keep) out.push(line.text);
    }
    position = cursor;
    reachesEnd = cursor === lines.length;
  }
  out.push(...lines.slice(position));

  const fromNoNewline = from === 'new' ? parsed.newNoNewline : parsed.oldNoNewline;
  const toNoNewline = from === 'new' ? parsed.oldNoNewline : parsed.newNoNewline;
  if (reachesEnd) {
    // The diff speaks about the end of the file: it must agree with the file.
    if (fromNoNewline === newline && lines.length > 0) return undefined;
    return out.join('\n') + (out.length > 0 && !toNoNewline ? '\n' : '');
  }
  return out.join('\n') + (out.length > 0 && newline ? '\n' : '');
}

/** The changed regions only, with a line where unchanged ones were left out. */
function regions(parsed: ParsedDiff): { before: string; after: string } {
  const before: string[] = [];
  const after: string[] = [];
  let oldNext = 1;
  for (const hunk of parsed.hunks) {
    const oldFirst = hunk.oldLines === 0 ? hunk.oldStart + 1 : hunk.oldStart;
    const gap = oldFirst - oldNext;
    if (gap > 0) {
      // The same on both sides, so the diff view reads it as unchanged.
      before.push(gapLine(gap));
      after.push(gapLine(gap));
    }
    for (const line of hunk.lines) {
      if (line.kind !== '+') before.push(line.text);
      if (line.kind !== '-') after.push(line.text);
    }
    oldNext = oldFirst + hunk.oldLines;
  }
  return { before: `${before.join('\n')}\n`, after: `${after.join('\n')}\n` };
}
