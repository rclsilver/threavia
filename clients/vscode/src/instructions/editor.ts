import * as vscode from 'vscode';

import type { Instructions, ProjectInstructionsUpdate } from '../api/types';
import { nonce } from '../conversation/html';
import type { Core } from '../cores/core';
import { pickBackend } from '../start/pickers';
import { instructionsPage, type InstructionsScope } from './page';

const scopeOf = (path: string): InstructionsScope => path.startsWith('/api/v1/projects/') ? 'project' : path.startsWith('/api/v1/backends/') ? 'backend' : 'global';

export class InstructionEditors implements vscode.Disposable {
  private readonly panels = new Map<string, vscode.WebviewPanel>();

  async open(core: Core, path: string, label: string) {
    const key = `${core.id}/${path}`;
    const existing = this.panels.get(key);
    if (existing) { existing.reveal(); return; }
    const view = await core.client.instructions(path);
    const opened = this.panels.get(key);
    if (opened) { opened.reveal(); return; }
    if (!core.ready) throw new Error('Core is not connected. Sign in or refresh, then try again.');
    const panel = vscode.window.createWebviewPanel('threavia.instructions', `Instructions: ${label} — ${core.name}`, vscode.ViewColumn.Active, { enableScripts: true, localResourceRoots: [], retainContextWhenHidden: true });
    this.panels.set(key, panel);
    panel.webview.html = instructionsPage(nonce(), label, view, scopeOf(path));
    const projectId = /^\/api\/v1\/projects\/([^/]+)\/instructions$/.exec(path)?.[1];
    let busy = false;
    let disposed = false;
    const received = panel.webview.onDidReceiveMessage((message: unknown) => {
      if (busy || !message || typeof message !== 'object') return;
      const data = message as { type?: string; instructions?: Partial<Instructions> };
      if (data.type !== 'save' && data.type !== 'reload' && !(data.type === 'preview' && projectId)) return;
      busy = true;
      void (async () => {
        try {
          if (!core.ready) throw new Error('Core is not connected. Sign in or refresh, then try again.');
          if (data.type === 'preview') {
            // The preview is of what is saved, on the backend a Job would run on.
            const backend = await pickBackend(core.client, 'Instructions preview');
            const preview = backend ? await core.client.instructionsPreview(projectId!, backend.id) : undefined;
            if (!disposed) await panel.webview.postMessage({ type: 'preview', backend: backend?.name, preview });
            return;
          }
          let body: Instructions | ProjectInstructionsUpdate | undefined;
          if (data.type === 'save') {
            const { agent, supervision } = data.instructions ?? {};
            // Only a project may leave out its agent text, which Core then keeps.
            if (typeof supervision !== 'string' || (agent === undefined ? !projectId : typeof agent !== 'string')) throw new Error('Both texts are required.');
            body = agent === undefined ? { supervision } : { agent, supervision };
          }
          const result = body ? await core.client.setInstructions(path, body) : await core.client.instructions(path);
          if (!disposed) await panel.webview.postMessage({ type: 'saved', view: result, reloaded: data.type === 'reload' });
        } catch (error) {
          if (!disposed) await panel.webview.postMessage({ type: 'error', message: error instanceof Error ? error.message : String(error), preview: data.type === 'preview' });
        } finally { busy = false; }
      })();
    });
    const closed = panel.onDidDispose(() => { disposed = true; this.panels.delete(key); received.dispose(); closed.dispose(); });
  }

  /**
   * Tells the editors showing `path` that it was saved elsewhere; the page
   * reloads unless it holds unsaved changes. Every editor shows the global
   * level, as its own or as inherited, so a global change reaches them all.
   */
  changed(coreId: string, path: string) {
    for (const [key, panel] of this.panels) {
      if (key === `${coreId}/${path}` || (path === '/api/v1/me/instructions' && key.startsWith(`${coreId}/`))) void panel.webview.postMessage({ type: 'changed' });
    }
  }

  closeCore(coreId: string) { for (const [key, panel] of this.panels) if (key.startsWith(`${coreId}/`)) panel.dispose(); }
  dispose() { for (const panel of this.panels.values()) panel.dispose(); this.panels.clear(); }
}
