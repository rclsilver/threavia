/**
 * The message "Ask Threavia about this" sends: what the person asked, then the
 * code it is about, said with where it comes from so the agent can find the
 * same lines on its own machine. Nothing here imports `vscode`.
 */

export interface Excerpt {
  /** The path relative to the workspace folder, with forward slashes. */
  path: string;
  /** 1-based and inclusive. */
  startLine: number;
  endLine: number;
  /** The editor's language id, used as the fence's language. */
  languageId?: string;
  code: string;
  /** True when nothing was selected and the whole file is sent. */
  wholeFile?: boolean;
}

/**
 * The lines a selection covers, 1-based. A selection ending at the very start
 * of a line — what a triple click or a selection made with Shift+Down gives —
 * does not include that line.
 */
export function lineRange(
  start: { line: number },
  end: { line: number; character: number },
): { startLine: number; endLine: number } {
  const last = end.character === 0 && end.line > start.line ? end.line - 1 : end.line;
  return { startLine: start.line + 1, endLine: last + 1 };
}

/** Where the excerpt is, as `path:12-30`, or `path:12` for one line. */
export function locationOf(excerpt: Pick<Excerpt, 'path' | 'startLine' | 'endLine'>): string {
  const lines =
    excerpt.startLine === excerpt.endLine ? `${excerpt.startLine}` : `${excerpt.startLine}-${excerpt.endLine}`;
  return `${excerpt.path}:${lines}`;
}

/** A fence longer than any run of backticks in the code, so the code cannot close it. */
export function fenceFor(code: string): string {
  const longest = Math.max(0, ...[...code.matchAll(/`+/g)].map((match) => match[0].length));
  return '`'.repeat(Math.max(3, longest + 1));
}

/** Editor language ids that are not what a markdown fence calls the language. */
const FENCE_LANGUAGE: Record<string, string> = {
  typescriptreact: 'tsx',
  javascriptreact: 'jsx',
  shellscript: 'sh',
  plaintext: '',
  dockerfile: 'dockerfile',
  makefile: 'make',
};

export function askMessage(instruction: string, excerpt: Excerpt): string {
  const fence = fenceFor(excerpt.code);
  const language = excerpt.languageId ? (FENCE_LANGUAGE[excerpt.languageId] ?? excerpt.languageId) : '';
  const where = excerpt.wholeFile ? `${excerpt.path} (the whole file)` : locationOf(excerpt);
  const code = excerpt.code.replace(/\n$/, '');
  return `${instruction.trim()}\n\n\`${where}\`:\n\n${fence}${language}\n${code}\n${fence}\n`;
}
