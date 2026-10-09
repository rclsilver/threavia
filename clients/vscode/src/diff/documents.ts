import * as vscode from 'vscode';

/**
 * Read-only documents the extension makes up: the two sides of a diff, a raw
 * diff, an artifact's text. Served under a scheme of their own, so the editor
 * opens them with syntax colouring from their path and no way to save over
 * anything.
 */
export class VirtualDocuments implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly contents = new Map<string, string>();
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  readonly onDidChange = this.changed.event;
  private readonly registration: vscode.Disposable;

  constructor(readonly scheme: string) {
    this.registration = vscode.workspace.registerTextDocumentContentProvider(scheme, this);
  }

  /** A URI for `path` (which names the file, for its language) holding `text`. */
  put(path: string, text: string): vscode.Uri {
    const uri = vscode.Uri.from({ scheme: this.scheme, path: path.startsWith('/') ? path : `/${path}` });
    this.contents.set(uri.toString(), text);
    // Bounded: each diff opened adds two documents, and a long day opens many.
    if (this.contents.size > 200) {
      const oldest = this.contents.keys().next().value;
      if (oldest !== undefined) this.contents.delete(oldest);
    }
    this.changed.fire(uri);
    return uri;
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    // A document restored after a reload has nothing behind it any more; it
    // says so rather than showing an empty file as if it were one.
    return this.contents.get(uri.toString()) ?? 'This document is no longer available. Open it again from the conversation.\n';
  }

  dispose() {
    this.registration.dispose();
    this.changed.dispose();
  }
}
