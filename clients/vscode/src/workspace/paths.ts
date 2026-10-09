/**
 * Finding, in the open workspace, a file the agent named.
 *
 * The agent works on its backend's machine, so the paths it writes are its
 * own: absolute under a home directory that is not this one, or relative to a
 * directory the editor may have opened at another level. This turns what it
 * wrote into the places in the workspace it may designate, most specific
 * first; the editor then opens the first that exists, and does nothing when
 * none does. Nothing here imports `vscode`.
 */

export interface PathRef {
  path: string;
  line?: number;
  column?: number;
}

/** A location inside one workspace folder, by its index and a relative path. */
export interface Candidate {
  folder: number;
  relative: string;
}

/** Something written as a file name, possibly with a line: `src/a.ts:12:3`. */
const REFERENCE = /^(.+?)(?::(\d+)(?::(\d+))?|#L(\d+)(?:-L?\d+)?)?$/;

/**
 * Reads a path reference out of a span of text, or nothing when it does not
 * look like one. Deliberately loose — `console.log` passes — because the
 * editor only offers to open what it actually finds on disk.
 */
export function parsePathRef(raw: string): PathRef | undefined {
  let text = raw.trim().replace(/^[`'"(<]+|[`'")>,;.]+$/g, '');
  if (!text || text.length > 300 || /\s/.test(text) || /^[a-z][a-z0-9+.-]*:\/\//i.test(text)) return undefined;
  const match = REFERENCE.exec(text);
  if (!match) return undefined;
  text = match[1].replace(/\\/g, '/');
  const name = text.split('/').pop() ?? '';
  // A path has a directory in it, or a file name with an extension that
  // starts with a letter: `1.2.3` is a version, not a file.
  if (!text.includes('/') && !/^[^.].*\.[A-Za-z][A-Za-z0-9_-]{0,9}$/.test(name)) return undefined;
  if (!name || name === '.' || name === '..') return undefined;
  const line = Number(match[2] ?? match[4]) || undefined;
  const column = line && match[3] ? Number(match[3]) : undefined;
  return { path: text, line, column };
}

/** The segments of a path, refusing one that climbs out with `..`. */
function segmentsOf(path: string): string[] | undefined {
  const segments = path.split('/').filter((segment) => segment && segment !== '.');
  return segments.includes('..') ? undefined : segments;
}

/**
 * Where a path may be in the workspace, most specific first.
 *
 * An absolute path inside a folder is that file. Otherwise the path is tried
 * against each folder whole, then without its leading directories one at a
 * time: `/home/agent/src/repo/pkg/a.go` finds `pkg/a.go` in a workspace
 * opened on `repo`, as `repo/pkg/a.go` does from the folder above it.
 *
 * `folders` are the folders' paths with forward slashes, as a URI's path.
 */
export function candidatesFor(path: string, folders: readonly string[]): Candidate[] {
  const normalised = path.replace(/\\/g, '/');
  const out: Candidate[] = [];
  const seen = new Set<string>();
  const add = (folder: number, segments: string[]) => {
    if (segments.length === 0) return;
    const relative = segments.join('/');
    const key = `${folder}:${relative}`;
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ folder, relative });
  };

  const absolute = normalised.startsWith('/') || /^[A-Za-z]:\//.test(normalised);
  if (absolute) {
    const comparable = normalised.replace(/^\/?([A-Za-z]):\//, (_, drive: string) => `/${drive.toLowerCase()}:/`);
    folders.forEach((folder, index) => {
      const root = folder.replace(/\/+$/, '').replace(/^\/?([A-Za-z]):\//, (_, drive: string) => `/${drive.toLowerCase()}:/`);
      if (comparable.startsWith(`${root}/`)) {
        const segments = segmentsOf(comparable.slice(root.length + 1));
        if (segments) add(index, segments);
      }
    });
  }

  const segments = segmentsOf(normalised.replace(/^[A-Za-z]:\//, '/'));
  if (!segments) return out;
  // A directory is kept to the end: a bare `index.ts` at the root of the
  // workspace is a guess, not the file `web/src/index.ts` named.
  const last = segments.length > 1 ? segments.length - 1 : segments.length;
  for (let drop = 0; drop < last; drop++) {
    folders.forEach((_, index) => add(index, segments.slice(drop)));
  }
  return out;
}
