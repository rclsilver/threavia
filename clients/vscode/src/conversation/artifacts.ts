import * as vscode from 'vscode';

import type { CoreClient } from '../api/client';
import type { VirtualDocuments } from '../diff/documents';
import { artifactKind } from './format';
import type { ArtifactRef } from './protocol';

/**
 * Showing and saving what an agent published.
 *
 * Core serves an artifact's bytes as an attachment, never as a page in its own
 * origin; the editor does the same: an image or a page opens in a webview of
 * its own, with no access to the extension and no network, and text opens as
 * a read-only document.
 */
export class Artifacts {
  constructor(
    private readonly client: CoreClient,
    private readonly documents: VirtualDocuments,
  ) {}

  async open(artifact: ArtifactRef, column: vscode.ViewColumn): Promise<void> {
    const kind = artifactKind(artifact.mimeType, artifact.filename);
    const bytes = await this.read(artifact);
    if (!bytes) return;
    const title = artifact.title || artifact.filename;

    if (kind === 'text' || kind === 'other') {
      const uri = this.documents.put(`/${artifact.artifactId}/${artifact.filename}`, new TextDecoder().decode(bytes));
      await vscode.window.showTextDocument(uri, { viewColumn: column, preview: true });
      return;
    }

    const panel = vscode.window.createWebviewPanel('threavia.artifact', title, column, {
      // A page an agent wrote runs its scripts — a chart needs them — in a
      // webview that can reach nothing: no network by its policy, no files,
      // and nobody listening to its messages.
      enableScripts: kind === 'html',
      localResourceRoots: [],
    });
    if (kind === 'image') {
      const type = artifact.mimeType.startsWith('image/')
        ? artifact.mimeType
        : artifact.filename.endsWith('.svg')
          ? 'image/svg+xml'
          : 'image/png';
      panel.webview.html = imagePage(title, `data:${type};base64,${Buffer.from(bytes).toString('base64')}`);
    } else {
      panel.webview.html = sandboxed(new TextDecoder().decode(bytes));
    }
  }

  async save(artifact: ArtifactRef): Promise<void> {
    const folder = vscode.workspace.workspaceFolders?.[0]?.uri;
    const target = await vscode.window.showSaveDialog({
      title: `Save ${artifact.filename}`,
      defaultUri: folder ? vscode.Uri.joinPath(folder, artifact.filename) : undefined,
      saveLabel: 'Save',
    });
    if (!target) return;
    const bytes = await this.read(artifact);
    if (!bytes) return;
    try {
      await vscode.workspace.fs.writeFile(target, bytes);
      vscode.window.setStatusBarMessage(`$(check) Saved ${artifact.filename}`, 4000);
    } catch (error) {
      void vscode.window.showErrorMessage(`Not saved: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  private async read(artifact: ArtifactRef): Promise<Uint8Array | undefined> {
    try {
      return await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Window, title: `Reading ${artifact.filename}…` },
        () => this.client.artifactContent(artifact.artifactId),
      );
    } catch (error) {
      void vscode.window.showErrorMessage(
        `${artifact.filename} could not be read: ${error instanceof Error ? error.message : String(error)}`,
      );
      return undefined;
    }
  }
}

function escape(text: string): string {
  return text.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
}

function imagePage(title: string, source: string): string {
  return `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline';">
<title>${escape(title)}</title>
<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:var(--vscode-editor-background)}img{max-width:100%;max-height:100vh;object-fit:contain}</style>
</head><body><img src="${source}" alt="${escape(title)}"></body></html>`;
}

/**
 * The page with a policy put in front of it: its own inline scripts and
 * styles run, nothing is fetched from anywhere. A policy the page declares
 * itself only narrows this one further.
 */
function sandboxed(html: string): string {
  const policy = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'">`;
  return /<head[^>]*>/i.test(html) ? html.replace(/<head[^>]*>/i, (head) => `${head}${policy}`) : `${policy}${html}`;
}
