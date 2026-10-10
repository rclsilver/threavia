import * as vscode from 'vscode';

import { parseRecordPath, recordPath } from '../cores/refs';

/**
 * The read-only markdown a Task or a Decision opens as, kept current.
 *
 * Each record has one URI, `<scheme>:/<core>/<id>/<Title>.md`: the Core first,
 * since two Cores may hold the same id; the file name gives the preview its
 * title and its language, and the Core and id find the text. The views hand
 * their records over every time they read them, and a document whose text
 * changed says so, so a preview left open follows what agents do to the
 * record.
 */
export class LiveDocuments implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly texts = new Map<string, string>();
  private readonly uris = new Map<string, vscode.Uri>();
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  readonly onDidChange = this.changed.event;
  private readonly registration: vscode.Disposable;

  constructor(
    readonly scheme: string,
    /** What a document says once its record is gone, or after a reload before it is read. */
    private readonly gone: string,
  ) {
    this.registration = vscode.workspace.registerTextDocumentContentProvider(scheme, this);
  }

  /** The URI of a record, the same one each time it is asked for. */
  uri(coreId: string, id: string, name: string): vscode.Uri {
    const key = `${coreId}/${id}`;
    let uri = this.uris.get(key);
    if (!uri) {
      uri = vscode.Uri.from({ scheme: this.scheme, path: recordPath(coreId, id, name) });
      this.uris.set(key, uri);
    }
    return uri;
  }

  /**
   * Records of a Core as just read, an undefined text for one that is gone. A
   * document open on one that changed is told; records of another Project,
   * not in `texts`, keep what they said.
   */
  update(coreId: string, texts: Map<string, string | undefined>) {
    for (const [id, text] of texts) {
      const key = `${coreId}/${id}`;
      if (this.texts.get(key) === text) continue;
      if (text === undefined) this.texts.delete(key);
      else this.texts.set(key, text);
      const uri = this.uris.get(key);
      if (uri) this.changed.fire(uri);
    }
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    const record = parseRecordPath(uri.path);
    return (record && this.texts.get(`${record.coreId}/${record.id}`)) ?? this.gone;
  }

  /** Opens a record as a rendered preview, or as plain text if previews are off. */
  async open(coreId: string, id: string, name: string): Promise<void> {
    const uri = this.uri(coreId, id, name);
    try {
      await vscode.commands.executeCommand('markdown.showPreview', uri);
    } catch {
      // The built-in markdown extension can be disabled; the text still reads.
      await vscode.window.showTextDocument(uri, { preview: true });
    }
  }

  dispose() {
    this.registration.dispose();
    this.changed.dispose();
  }
}
