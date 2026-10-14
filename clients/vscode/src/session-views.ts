import * as vscode from 'vscode';

import { parseCorePath, sessionKey, type SessionRef } from './cores/refs';

/** Views owned by a Session, including hidden panels and tabs in other groups. */
const panels = new Map<vscode.WebviewPanel, string>();
const archived = new Set<string>();

export function sessionArchived(ref: SessionRef): boolean {
  return archived.has(sessionKey(ref));
}

export function sessionRestored(ref: SessionRef) {
  archived.delete(sessionKey(ref));
}

/** False also prevents an asynchronous open from finishing after archival. */
export function registerSessionPanel(ref: SessionRef, panel: vscode.WebviewPanel): boolean {
  if (sessionArchived(ref)) {
    panel.dispose();
    return false;
  }
  panels.set(panel, sessionKey(ref));
  const closed = panel.onDidDispose(() => { panels.delete(panel); closed.dispose(); });
  return true;
}

function belongsTo(uri: vscode.Uri, ref: SessionRef): boolean {
  // Paths carry their owner, even when restored by VS Code after a reload.
  const path = parseCorePath(uri.path);
  if (!path || path.coreId !== ref.coreId) return false;
  if (uri.scheme === 'threavia-diff') return path.rest[0] === ref.sessionId;
  return uri.scheme === 'threavia-artifact' && path.rest[0] === 'sessions'
    && path.rest[1] === ref.sessionId && path.rest[2] === 'artifacts';
}

export async function closeSessionViews(ref: SessionRef): Promise<void> {
  const key = sessionKey(ref);
  archived.add(key);
  for (const [panel, owner] of panels) if (owner === key) panel.dispose();
  const tabs = vscode.window.tabGroups.all.flatMap((group) => group.tabs).filter((tab) => {
    const input = tab.input;
    if (input instanceof vscode.TabInputText) return belongsTo(input.uri, ref);
    if (input instanceof vscode.TabInputTextDiff) return belongsTo(input.original, ref) || belongsTo(input.modified, ref);
    return false;
  });
  if (tabs.length) {
    try {
      await vscode.window.tabGroups.close(tabs, true);
    } catch (error) {
      void vscode.window.showWarningMessage(`Session archived, but some tabs could not close: ${String(error)}`);
    }
  }
}
