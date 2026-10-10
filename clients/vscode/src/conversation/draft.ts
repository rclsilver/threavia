/**
 * A new Session before it exists: what was chosen to start it, and how the
 * panel showing it becomes the Session's own once the first message is sent.
 *
 * Nothing exists in Core until that message goes out, as in the web client's
 * draft (spec section 34); then Session, Run, Job and message are created at
 * once. Nothing here imports `vscode`, so the transitions are tested alone.
 */

/** What the pickers chose: the ids Core needs, and the names the header shows. */
export interface DraftStart {
  /** The Core the Session will be created on. */
  coreId: string;
  /** Said in the draft's header only when there is more than one Core. */
  coreName?: string;
  projectId: string;
  projectName: string;
  backendInstanceId: string;
  backendName: string;
  backendType?: string;
  backendTooltip?: string;
  workingDirectoryId: string | null;
  directoryName?: string;
}

/** The body of POST /api/v1/sessions/start, as the web client's draft sends it. */
export interface StartBody {
  projectId: string;
  backendInstanceId: string;
  workingDirectoryId: string | null;
  message: string;
}

export function startBody(start: DraftStart, text: string): StartBody {
  return {
    projectId: start.projectId,
    backendInstanceId: start.backendInstanceId,
    workingDirectoryId: start.workingDirectoryId,
    message: text.trim(),
  };
}

/**
 * What a conversation panel shows: a draft, a draft whose first message is on
 * its way, or a Session.
 */
export type Target =
  | { kind: 'draft'; start: DraftStart }
  | { kind: 'starting'; start: DraftStart }
  | { kind: 'session'; coreId: string; sessionId: string };

/**
 * What a message typed in the panel does. A draft starts the Session; a
 * second message typed while it starts waits for it, rather than starting a
 * second Session; a Session takes it as any message.
 */
export function onSend(target: Target): 'start' | 'wait' | 'post' {
  if (target.kind === 'draft') return 'start';
  if (target.kind === 'starting') return 'wait';
  return 'post';
}

export function sending(target: Target): Target {
  return target.kind === 'draft' ? { kind: 'starting', start: target.start } : target;
}

/** Core created the Session: the panel is that Session's from now on. */
export function started(target: Target, sessionId: string): Target {
  return target.kind === 'starting' ? { kind: 'session', coreId: target.start.coreId, sessionId } : target;
}

/** The start was refused: the draft stays as it was, to be sent again. */
export function startFailed(target: Target): Target {
  return target.kind === 'starting' ? { kind: 'draft', start: target.start } : target;
}

/**
 * The key a panel is found again by: its Session, or its Project for a
 * draft, each with its Core, since ids are only unique within one.
 */
export function targetKey(target: Target): string {
  return target.kind === 'session'
    ? `${target.coreId}/${target.sessionId}`
    : draftKey(target.start.coreId, target.start.projectId);
}

/** One draft per Project and Core, as the web client keeps one first message per Project. */
export function draftKey(coreId: string, projectId: string): string {
  return `${coreId}/new:${projectId}`;
}

/** The Core a panel's Session or draft belongs to. */
export function targetCore(target: Target): string {
  return target.kind === 'session' ? target.coreId : target.start.coreId;
}

/**
 * What a panel restored after a reload shows, from the state its page kept.
 * A state saved before there were several Cores names none: it is the first
 * Core's, the one that version was connected to.
 */
export function restoredTarget(
  state: { coreId?: string; sessionId?: string; start?: Partial<DraftStart> } | undefined,
  firstCoreId: string | undefined,
): Target | undefined {
  const coreId = state?.coreId || state?.start?.coreId || firstCoreId;
  if (!coreId) return undefined;
  if (state?.sessionId) return { kind: 'session', coreId, sessionId: state.sessionId };
  const start = state?.start;
  if (start?.projectId && start.backendInstanceId) {
    return { kind: 'draft', start: { ...(start as DraftStart), coreId } };
  }
  return undefined;
}
