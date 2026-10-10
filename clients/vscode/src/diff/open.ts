import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import { diffPath } from '../cores/refs';
import type { PathResolver } from '../workspace/resolve';
import type { VirtualDocuments } from './documents';
import { reconstruct } from './unified';

/**
 * Opens the change a Job made to one file in the editor's own diff view.
 *
 * Core keeps no diff: the backend that holds the working directory computes
 * it when asked, from the two trees the workspace.changed event recorded. The
 * two sides are rebuilt from it (see unified.ts); when they cannot be, the
 * raw diff opens instead, with the reason.
 */
export async function openFileDiff(
  client: CoreClient,
  documents: VirtualDocuments,
  paths: PathResolver,
  target: { coreId: string; sessionId: string; sequence: number; path: string; column: vscode.ViewColumn },
): Promise<void> {
  const { coreId, sessionId, sequence, path, column } = target;
  let diff;
  try {
    diff = await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Window, title: 'Asking the backend for the diff…' },
      () => client.diff(sessionId, sequence, path),
    );
  } catch (error) {
    // Said plainly: the backend is away (503), or git has collected the
    // state it was taken from (409).
    const reason = error instanceof ApiError || error instanceof Error ? error.message : String(error);
    void vscode.window.showWarningMessage(`The diff of ${path} could not be read: ${reason}`);
    return;
  }

  // The copy of the file in this workspace, when there is one: if it is one
  // side of the change, the diff shows the whole file rather than its hunks.
  let local: string | undefined;
  const found = await paths.resolve(path);
  if (found) {
    try {
      local = new TextDecoder().decode(await vscode.workspace.fs.readFile(found.uri));
    } catch {
      local = undefined;
    }
  }

  const sides = reconstruct(diff.diff, { binary: diff.binary, truncated: diff.truncated, local });
  const name = path.split('/').pop() ?? path;
  // The Core comes first in every path: two Cores can hold the same ids.
  const at = (side: string, file: string) => diffPath(coreId, sessionId, sequence, side, file);

  if (sides.kind === 'none') {
    if (!diff.diff.trim()) {
      void vscode.window.showInformationMessage(`${path}: ${sides.reason}`);
      return;
    }
    const raw = documents.put(at('raw', `${path}.diff`), diff.diff);
    await vscode.window.showTextDocument(raw, { viewColumn: column, preview: true });
    void vscode.window.showInformationMessage(`Showing the raw diff of ${name}. ${sides.reason}`);
    return;
  }

  const before = documents.put(at('before', path), sides.before);
  const after = documents.put(at('after', path), sides.after);
  const title =
    sides.kind === 'complete' ? `${name} (changed by the agent)` : `${name} (changed by the agent, changed regions only)`;
  await vscode.commands.executeCommand('vscode.diff', before, after, title, { viewColumn: column, preview: true });
}
