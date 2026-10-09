import * as vscode from 'vscode';

import { candidatesFor, parsePathRef, type PathRef } from './paths';

/**
 * The editor half of finding a file the agent named: tries the candidates
 * `candidatesFor` lists, in order, and keeps the first that is a file on disk.
 * Answers are remembered for the window's life, since the same paths come
 * back on every redraw; a change of workspace folders forgets them.
 */
export class PathResolver implements vscode.Disposable {
  private readonly known = new Map<string, Promise<{ uri: vscode.Uri; ref: PathRef } | undefined>>();
  private readonly subscription = vscode.workspace.onDidChangeWorkspaceFolders(() => this.known.clear());

  resolve(text: string): Promise<{ uri: vscode.Uri; ref: PathRef } | undefined> {
    let found = this.known.get(text);
    if (!found) {
      found = this.find(text);
      this.known.set(text, found);
    }
    return found;
  }

  private async find(text: string): Promise<{ uri: vscode.Uri; ref: PathRef } | undefined> {
    const ref = parsePathRef(text);
    const folders = vscode.workspace.workspaceFolders ?? [];
    if (!ref || folders.length === 0) return undefined;
    for (const candidate of candidatesFor(ref.path, folders.map((folder) => folder.uri.path))) {
      const uri = vscode.Uri.joinPath(folders[candidate.folder].uri, candidate.relative);
      try {
        const stat = await vscode.workspace.fs.stat(uri);
        if (stat.type & vscode.FileType.File) return { uri, ref };
      } catch {
        // Not there: the next candidate is.
      }
    }
    return undefined;
  }

  /** Opens what the agent named, at its line, or does nothing when it is not in the workspace. */
  async open(text: string, column: vscode.ViewColumn): Promise<boolean> {
    const found = await this.resolve(text);
    if (!found) return false;
    const line = Math.max(0, (found.ref.line ?? 1) - 1);
    const character = Math.max(0, (found.ref.column ?? 1) - 1);
    const position = new vscode.Position(line, character);
    await vscode.window.showTextDocument(found.uri, {
      viewColumn: column,
      selection: found.ref.line ? new vscode.Range(position, position) : undefined,
      preview: true,
    });
    return true;
  }

  dispose() {
    this.subscription.dispose();
  }
}
