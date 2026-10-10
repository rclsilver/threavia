import * as vscode from 'vscode';

import { sessionKey, sessionRefOf, type SessionRef } from './cores/refs';

/**
 * Opening a Session: one command, `threavia.openSession`, that everything
 * else calls — a sidebar row, a notification, the Waiting list.
 *
 * Its argument is a `{ coreId, sessionId }`, or anything carrying one (a
 * sidebar node), so a caller never needs to know how the conversation is
 * shown. A bare Session id, as an earlier version passed it, is the first
 * Core's. The conversation view registers itself with `setSessionOpener`.
 */

type Opener = (ref: SessionRef) => Promise<void>;

let opener: Opener = () => Promise.resolve();
let coreIds: () => readonly string[] = () => [];

/** Replaces how a Session opens. The conversation view calls this once. */
export function setSessionOpener(next: Opener, cores: () => readonly string[]) {
  opener = next;
  coreIds = cores;
}

/** Runs the command, so a replaced opener is used wherever it is called from. */
export function openSession(ref: SessionRef): Thenable<unknown> {
  return vscode.commands.executeCommand('threavia.openSession', ref);
}

/** What the command runs. */
export function runOpenSession(target: unknown, second?: unknown): Promise<void> {
  const ref = sessionRefOf(target, coreIds(), second);
  if (!ref) return Promise.resolve();
  return opener(ref);
}

const open = new Set<string>();

/**
 * Which Sessions are showing in the editor. The conversation view keeps this
 * current, so a notice about work that ended is not shown over the very
 * conversation that already says it.
 */
export function markSessionOpen(ref: SessionRef, isOpen: boolean) {
  if (isOpen) open.add(sessionKey(ref));
  else open.delete(sessionKey(ref));
}

export function isSessionOpen(ref: SessionRef): boolean {
  return open.has(sessionKey(ref));
}

export function openInBrowser(coreUrl: string, sessionId: string): Thenable<boolean> {
  return vscode.env.openExternal(vscode.Uri.parse(`${coreUrl}/sessions/${sessionId}`));
}
