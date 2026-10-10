/**
 * Naming things across Cores.
 *
 * A Session, a Project, a Task or an Artifact is only unique within the Core
 * that holds it, so everything the extension keeps or hands around names its
 * Core too: command arguments, conversation panels, drafts, and the URIs of
 * the documents it makes up.
 *
 * Earlier versions knew one Core, and what they left behind — a command bound
 * to a key, a panel restored after an update — names none. That goes to the
 * first Core, which is the one an earlier version was connected to.
 *
 * Nothing here imports `vscode`.
 */

export interface SessionRef {
  coreId: string;
  sessionId: string;
}

/** The key a Session is found by in a map shared by every Core. */
export function sessionKey(ref: SessionRef): string {
  return `${ref.coreId}/${ref.sessionId}`;
}

/** The Core a command argument names, or the first one when it names none. */
function coreOr(value: unknown, coreIds: readonly string[]): string | undefined {
  if (typeof value === 'string' && value) return value;
  return coreIds[0];
}

/**
 * The Session in whatever a command was handed: a Session id, with or without
 * a Core id after it; a `{ coreId, sessionId }`; a sidebar node.
 */
export function sessionRefOf(target: unknown, coreIds: readonly string[], second?: unknown): SessionRef | undefined {
  if (typeof target === 'string') {
    const coreId = coreOr(second, coreIds);
    return target && coreId ? { coreId, sessionId: target } : undefined;
  }
  if (!target || typeof target !== 'object') return undefined;
  const value = target as {
    coreId?: unknown;
    sessionId?: unknown;
    session?: { id?: unknown };
    item?: { sessionId?: unknown };
  };
  const sessionId =
    typeof value.sessionId === 'string'
      ? value.sessionId
      : typeof value.session?.id === 'string'
        ? value.session.id
        : typeof value.item?.sessionId === 'string'
          ? value.item.sessionId
          : undefined;
  const coreId = coreOr(value.coreId, coreIds);
  return sessionId && coreId ? { coreId, sessionId } : undefined;
}

/**
 * The Core a Core command was run on: a Core node or anything else carrying a
 * `coreId`, or a Core id itself. Undefined when it names none, so the command
 * asks which.
 */
export function coreIdOf(target: unknown): string | undefined {
  if (typeof target === 'string') return target || undefined;
  if (!target || typeof target !== 'object') return undefined;
  const value = (target as { coreId?: unknown }).coreId;
  return typeof value === 'string' && value ? value : undefined;
}

/**
 * A record of the current Project's Core — a Task, a Decision — as its
 * command is handed it: its id alone from an earlier version, or with the
 * Core it belongs to.
 */
export function recordRefOf(target: unknown, fallbackCoreId: string | undefined): { coreId: string; id: string } | undefined {
  if (typeof target === 'string') return target && fallbackCoreId ? { coreId: fallbackCoreId, id: target } : undefined;
  if (!target || typeof target !== 'object') return undefined;
  const value = target as { coreId?: unknown; id?: unknown };
  const coreId = coreOr(value.coreId, fallbackCoreId ? [fallbackCoreId] : []);
  return typeof value.id === 'string' && value.id && coreId ? { coreId, id: value.id } : undefined;
}

// ----------------------------------------------------------------- the URIs

/**
 * The path of a document the extension makes up: the Core first, then what
 * the document is. The last segment stays the file's name, so the editor
 * still picks its language and its tab title from it.
 */
export function corePath(coreId: string, ...segments: string[]): string {
  return `/${[encodeURIComponent(coreId), ...segments].join('/')}`;
}

/** The Core a made-up document belongs to, and the rest of its path. */
export function parseCorePath(path: string): { coreId: string; rest: string[] } | undefined {
  const [, core, ...rest] = path.split('/');
  if (!core) return undefined;
  try {
    return { coreId: decodeURIComponent(core), rest };
  } catch {
    return undefined;
  }
}

/** Where one side of a diff lives: `/<core>/<session>/<sequence>/<side>/<path>`. */
export function diffPath(coreId: string, sessionId: string, sequence: number, side: string, path: string): string {
  return corePath(coreId, sessionId, String(sequence), side, ...path.split('/').filter(Boolean));
}

/** Where an Artifact's text lives: `/<core>/<artifact>/<filename>`. */
export function artifactPath(coreId: string, artifactId: string, filename: string): string {
  return corePath(coreId, artifactId, filename);
}

/** Where a Task or a Decision opens: `/<core>/<id>/<Title>.md`. */
export function recordPath(coreId: string, id: string, name: string): string {
  return corePath(coreId, id, name);
}

/** The Core and the record a Task or Decision document shows. */
export function parseRecordPath(path: string): { coreId: string; id: string } | undefined {
  const parsed = parseCorePath(path);
  const id = parsed?.rest[0];
  return parsed && id ? { coreId: parsed.coreId, id } : undefined;
}

// ------------------------------------------------------------ the accounts

/**
 * The scope that names a Core in the editor's Accounts API. There is one
 * authentication provider for the extension, and each Core signed in to is one
 * of its sessions, asked for and told apart by this scope.
 */
export function coreScope(coreId: string): string {
  return `core:${coreId}`;
}

/** The Core a request for sessions names, or undefined when it names none. */
export function coreOfScopes(scopes: readonly string[] | undefined): string | undefined {
  const scope = scopes?.find((candidate) => candidate.startsWith('core:'));
  return scope ? scope.slice('core:'.length) || undefined : undefined;
}

/** An account session's id: the Core, then who is signed in to it. */
export function accountSessionId(coreId: string, label: string): string {
  return `${coreId}#${label}`;
}

export function coreOfAccountSession(sessionId: string): string {
  const hash = sessionId.indexOf('#');
  return hash === -1 ? sessionId : sessionId.slice(0, hash);
}
