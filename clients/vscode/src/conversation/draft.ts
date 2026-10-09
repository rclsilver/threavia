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
  projectId: string;
  projectName: string;
  backendInstanceId: string;
  backendName: string;
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
  | { kind: 'session'; sessionId: string };

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
  return target.kind === 'starting' ? { kind: 'session', sessionId } : target;
}

/** The start was refused: the draft stays as it was, to be sent again. */
export function startFailed(target: Target): Target {
  return target.kind === 'starting' ? { kind: 'draft', start: target.start } : target;
}

/** The key a panel is found again by: its Session, or its Project for a draft. */
export function targetKey(target: Target): string {
  return target.kind === 'session' ? target.sessionId : draftKey(target.start.projectId);
}

/** One draft per Project, as the web client keeps one first message per Project. */
export function draftKey(projectId: string): string {
  return `new:${projectId}`;
}

/** What a panel restored after a reload shows, from the state its page kept. */
export function restoredTarget(state: { sessionId?: string; start?: DraftStart } | undefined): Target | undefined {
  if (state?.sessionId) return { kind: 'session', sessionId: state.sessionId };
  if (state?.start?.projectId && state.start.backendInstanceId) return { kind: 'draft', start: state.start };
  return undefined;
}
