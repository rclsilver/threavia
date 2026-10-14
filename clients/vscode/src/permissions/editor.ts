import * as vscode from 'vscode';

import type { ExecutionPolicy } from '../api/types';
import { nonce } from '../conversation/html';
import type { Core } from '../cores/core';
import { permissionsPage } from './page';
import { registerSessionPanel } from '../session-views';

export class PermissionEditors implements vscode.Disposable {
  private readonly panels = new Map<string, vscode.WebviewPanel>();

  async open(core: Core, path: string, label: string) {
    const key = `${core.id}/${path}`;
    const existing = this.panels.get(key);
    if (existing) { existing.reveal(); return; }
    const view = await core.client.policy(path);
    const opened = this.panels.get(key);
    if (opened) { opened.reveal(); return; }
    if (!core.ready) throw new Error('Core is not connected. Sign in or refresh, then try again.');
    const panel = vscode.window.createWebviewPanel('threavia.permissions', `Permissions: ${label} — ${core.name}`, vscode.ViewColumn.Active, { enableScripts: true, localResourceRoots: [], retainContextWhenHidden: true });
    this.panels.set(key, panel);
    panel.webview.html = permissionsPage(nonce(), label, view);
    let busy = false;
    let disposed = false;
    const received = panel.webview.onDidReceiveMessage((message: unknown) => {
      if (busy || !message || typeof message !== 'object') return;
      const data = message as { type?: string; policy?: ExecutionPolicy };
      if (data.type !== 'save' && data.type !== 'reset' && data.type !== 'reload') return;
      busy = true;
      void (async () => {
        try {
          if (!core.ready) throw new Error('Core is not connected. Sign in or refresh, then try again.');
          if (data.type === 'save' && (!data.policy || typeof data.policy.mode !== 'string')) throw new Error('Choose a mode.');
          const result = data.type === 'reload' ? await core.client.policy(path) : await core.client.setPolicy(path, data.type === 'reset' ? null : data.policy!);
          if (!disposed) await panel.webview.postMessage({ type: 'saved', view: result, reloaded: data.type === 'reload' });
        } catch (error) {
          if (!disposed) await panel.webview.postMessage({ type: 'error', message: error instanceof Error ? error.message : String(error) });
        } finally { busy = false; }
      })();
    });
    const closed = panel.onDidDispose(() => { disposed = true; this.panels.delete(key); received.dispose(); closed.dispose(); });
    const sessionId = /^\/api\/v1\/sessions\/([^/]+)\/policy$/.exec(path)?.[1];
    if (sessionId) registerSessionPanel({ coreId: core.id, sessionId }, panel);
  }

  closeCore(coreId: string) { for (const [key, panel] of this.panels) if (key.startsWith(`${coreId}/`)) panel.dispose(); }
  dispose() { for (const panel of this.panels.values()) panel.dispose(); this.panels.clear(); }
}
