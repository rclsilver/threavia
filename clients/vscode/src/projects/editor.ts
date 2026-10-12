import * as vscode from 'vscode';

import type { Project } from '../api/types';
import { nonce } from '../conversation/html';
import type { Core } from '../cores/core';
import { projectPatch, readProjectFields } from './model';
import { projectPage } from './page';

/** One form per Project (and one creation form per Core). */
export class ProjectEditors implements vscode.Disposable {
  private readonly panels = new Map<string, vscode.WebviewPanel>();

  constructor(private readonly saved: (core: Core, project: Project, created: boolean) => void) {}

  open(core: Core, project?: Project) {
    const key = `${core.id}/${project?.id ?? 'new'}`;
    const existing = this.panels.get(key);
    if (existing) { existing.reveal(); return; }
    const title = `${project ? `Edit ${project.name}` : 'New Project'} — ${core.name}`;
    const panel = vscode.window.createWebviewPanel('threavia.project', title, vscode.ViewColumn.Active, {
      enableScripts: true,
      localResourceRoots: [],
      retainContextWhenHidden: true,
    });
    this.panels.set(key, panel);
    panel.webview.html = projectPage(nonce(), project);
    let saving = false;
    let disposed = false;
    const received = panel.webview.onDidReceiveMessage((message: unknown) => {
      if (saving) return;
      if (message && typeof message === 'object' && (message as { type?: unknown }).type === 'cancel') {
        panel.dispose();
        return;
      }
      const fields = readProjectFields(message);
      if (!fields) return;
      if (!fields.name) {
        void panel.webview.postMessage({ type: 'error', message: 'Enter a project name.' });
        return;
      }
      saving = true;
      void (async () => {
        try {
          if (!core.ready) throw new Error(`${core.name} is not connected. Sign in or refresh, then try again.`);
          let result: Project;
          if (project) {
            const patch = projectPatch(project, fields);
            result = Object.keys(patch).length ? await core.client.updateProject(project.id, patch) : project;
          } else {
            result = await core.client.createProject({ name: fields.name, description: fields.description });
          }
          this.saved(core, result, !project);
          panel.dispose();
        } catch (error) {
          const reason = error instanceof Error ? error.message : String(error);
          if (!disposed) await panel.webview.postMessage({ type: 'error', message: `Project not ${project ? 'updated' : 'created'}: ${reason}` });
        } finally {
          saving = false;
        }
      })();
    });
    const closed = panel.onDidDispose(() => {
      disposed = true;
      this.panels.delete(key);
      received.dispose();
      closed.dispose();
    });
  }

  closeProject(coreId: string, projectId: string) {
    this.panels.get(`${coreId}/${projectId}`)?.dispose();
  }

  closeCore(coreId: string) {
    for (const [key, panel] of this.panels) if (key.startsWith(`${coreId}/`)) panel.dispose();
  }

  dispose() {
    for (const panel of this.panels.values()) panel.dispose();
    this.panels.clear();
  }
}
