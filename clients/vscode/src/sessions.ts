import * as vscode from 'vscode';

import { coreUrl } from './config';

/**
 * Opening a Session: one command, `threavia.openSession`, that everything
 * else calls — a sidebar row, a notification, the Waiting list.
 *
 * Its argument is a Session id, or anything carrying one (a sidebar node, a
 * `{ sessionId }`), so a caller never needs to know how the conversation is
 * shown. Until the conversation has a view in the editor, it offers the
 * browser; the view registers itself with `setSessionOpener` and nothing else
 * changes.
 */

type Opener = (sessionId: string) => Promise<void>;

let opener: Opener = async (sessionId) => {
  const choice = await vscode.window.showInformationMessage(
    'The conversation does not open in the editor yet.',
    'Open in Browser',
  );
  if (choice) await openInBrowser(sessionId);
};

/** Replaces how a Session opens. The conversation view calls this once. */
export function setSessionOpener(next: Opener) {
  opener = next;
}

/** Runs the command, so a replaced opener is used wherever it is called from. */
export function openSession(sessionId: string): Thenable<unknown> {
  return vscode.commands.executeCommand('threavia.openSession', sessionId);
}

/** What the command runs. */
export function runOpenSession(target: unknown): Promise<void> {
  const sessionId = sessionIdOf(target);
  if (!sessionId) return Promise.resolve();
  return opener(sessionId);
}

/** The Session id in whatever a command was handed. */
export function sessionIdOf(target: unknown): string | undefined {
  if (typeof target === 'string') return target || undefined;
  if (!target || typeof target !== 'object') return undefined;
  const value = target as { sessionId?: unknown; session?: { id?: unknown }; item?: { sessionId?: unknown } };
  if (typeof value.sessionId === 'string') return value.sessionId;
  if (typeof value.session?.id === 'string') return value.session.id;
  if (typeof value.item?.sessionId === 'string') return value.item.sessionId;
  return undefined;
}

const open = new Set<string>();

/**
 * Which Sessions are showing in the editor. The conversation view keeps this
 * current, so a notice about work that ended is not shown over the very
 * conversation that already says it.
 */
export function markSessionOpen(sessionId: string, isOpen: boolean) {
  if (isOpen) open.add(sessionId);
  else open.delete(sessionId);
}

export function isSessionOpen(sessionId: string): boolean {
  return open.has(sessionId);
}

export function openInBrowser(sessionId: string): Thenable<boolean> {
  return vscode.env.openExternal(vscode.Uri.parse(`${coreUrl()}/sessions/${sessionId}`));
}
