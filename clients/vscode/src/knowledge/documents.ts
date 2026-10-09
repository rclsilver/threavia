import * as vscode from 'vscode';

/**
 * The read-only markdown a Task or a Decision opens as, kept current.
 *
 * Each record has one URI, `<scheme>:/<id>/<Title>.md`: the file name gives
 * the preview its title and its language, and the id finds the text. The
 * views hand their records over every time they read them, and a document
 * whose text changed says so, so a preview left open follows what agents do
 * to the record.
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
  uri(id: string, name: string): vscode.Uri {
    let uri = this.uris.get(id);
    if (!uri) {
      uri = vscode.Uri.from({ scheme: this.scheme, path: `/${id}/${name}` });
      this.uris.set(id, uri);
    }
    return uri;
  }

  /**
   * Records as just read, an undefined text for one that is gone. A document
   * open on one that changed is told; records of another Project, not in
   * `texts`, keep what they said.
   */
  update(texts: Map<string, string | undefined>) {
    for (const [id, text] of texts) {
      if (this.texts.get(id) === text) continue;
      if (text === undefined) this.texts.delete(id);
      else this.texts.set(id, text);
      const uri = this.uris.get(id);
      if (uri) this.changed.fire(uri);
    }
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    const id = uri.path.split('/')[1] ?? '';
    return this.texts.get(id) ?? this.gone;
  }

  /** Opens a record as a rendered preview, or as plain text if previews are off. */
  async open(id: string, name: string): Promise<void> {
    const uri = this.uri(id, name);
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
