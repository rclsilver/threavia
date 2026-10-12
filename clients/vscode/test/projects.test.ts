import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('vscode', () => ({
  ViewColumn: { Active: -1 },
  window: { createWebviewPanel: vi.fn() },
}));

import * as vscode from 'vscode';
import { ApiError, CoreClient } from '../src/api/client';
import type { Project } from '../src/api/types';
import type { Core } from '../src/cores/core';
import { ProjectEditors } from '../src/projects/editor';
import { projectPatch, readProjectFields } from '../src/projects/model';
import { projectPage } from '../src/projects/page';

const project: Project = {
  id: 'p1', ownerId: 'u1', name: 'Website', description: 'A website', instructions: 'Keep it accessible.\nUse semantic HTML.',
  status: 'ACTIVE', createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z',
};

describe('project changes', () => {
  it('rejects malformed messages and trims fields without losing line breaks', () => {
    for (const message of [null, 'save', {}, { type: 'save', name: 'Name' }, { type: 'save', name: 1, description: '', instructions: '' }]) {
      expect(readProjectFields(message)).toBeUndefined();
    }
    expect(readProjectFields({ type: 'save', name: ' Website ', description: ' Details ', instructions: ' One\nTwo ' }))
      .toEqual({ name: 'Website', description: 'Details', instructions: 'One\nTwo' });
  });

  it('leaves untouched fields out so saving a name does not overwrite other clients’ rules', () => {
    expect(projectPatch(project, { name: 'Renamed', description: project.description!, instructions: project.instructions! }))
      .toEqual({ name: 'Renamed' });
    expect(projectPatch(project, { name: project.name, description: project.description!, instructions: project.instructions! }))
      .toEqual({});
  });

  it('clears optional fields using Core’s trim semantics and leaves already empty fields out', () => {
    expect(projectPatch(project, { name: project.name, description: '', instructions: '' }))
      .toEqual({ description: ' ', instructions: ' ' });
    expect(projectPatch({ ...project, description: undefined, instructions: undefined }, { name: project.name, description: '', instructions: '' }))
      .toEqual({});
  });

  it('escapes saved project content before inserting it in a form', () => {
    const html = projectPage('test-nonce', { ...project, name: '" autofocus onfocus="alert(1)', instructions: '</textarea><script>attack()</script>' });
    expect(html).not.toContain('</textarea><script>attack()');
    expect(html).toContain('&#60;/textarea&#62;');
    expect(html).toContain('value="&#34; autofocus onfocus=&#34;alert(1)"');
    expect(html).toContain("default-src 'none'");
    expect(html).toContain("script-src 'nonce-test-nonce'");
    expect(projectPage('n')).not.toContain('id="instructions"');
  });
});

describe('the HTTP project contract', () => {
  it('uses the selected Core and client identity for create, edit and delete, and reports unfinished work', async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    const client = new CoreClient({
      baseUrl: () => 'https://second.example', authorization: () => Promise.resolve('Bearer test-token'),
      identity: { clientId: 'editor', clientName: () => 'VS Code' },
      fetch: (input, init) => {
        const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
        calls.push({ url, init: init! });
        if (init?.method === 'DELETE') return Promise.resolve(Response.json({ error: { code: 'unfinished_work', message: 'Stop all unfinished jobs first.' } }, { status: 409 }));
        return Promise.resolve(Response.json(project, { status: init?.method === 'POST' ? 201 : 200 }));
      },
    });
    await client.createProject({ name: 'Website', description: 'A website' });
    await client.updateProject('p1', { description: 'Updated', instructions: 'One\nTwo' });
    await expect(client.deleteProject('p1')).rejects.toMatchObject({ status: 409, message: 'Stop all unfinished jobs first.' });
    expect(calls.map(({ url, init }) => [url, init.method, init.body])).toEqual([
      ['https://second.example/api/v1/projects', 'POST', JSON.stringify({ name: 'Website', description: 'A website' })],
      ['https://second.example/api/v1/projects/p1', 'PATCH', JSON.stringify({ description: 'Updated', instructions: 'One\nTwo' })],
      ['https://second.example/api/v1/projects/p1', 'DELETE', undefined],
    ]);
    for (const call of calls) {
      const headers = call.init.headers as Headers;
      expect(headers.get('Authorization')).toBe('Bearer test-token');
      expect(headers.get('X-Threavia-Channel')).toBe('vscode');
    }
  });
});

class Panel {
  receive: (message: unknown) => void = () => {};
  closed: () => void = () => {};
  reveal = vi.fn();
  disposed = false;
  webview = {
    html: '', postMessage: vi.fn().mockResolvedValue(true),
    onDidReceiveMessage: (receive: (message: unknown) => void) => { this.receive = receive; return { dispose() {} }; },
  };
  onDidDispose(closed: () => void) { this.closed = closed; return { dispose() {} }; }
  dispose() { if (!this.disposed) { this.disposed = true; this.closed(); } }
}

const save = (name = 'Website') => ({ type: 'save', name, description: 'A website', instructions: project.instructions });

describe('project forms', () => {
  let panels: Panel[];
  let editors: ProjectEditors;
  let saved: ReturnType<typeof vi.fn>;
  let core: Core;
  let create: ReturnType<typeof vi.fn>;
  let update: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    panels = [];
    vi.mocked(vscode.window.createWebviewPanel).mockImplementation(() => {
      const panel = new Panel(); panels.push(panel);
      return panel as unknown as vscode.WebviewPanel;
    });
    create = vi.fn().mockResolvedValue(project);
    update = vi.fn().mockResolvedValue({ ...project, name: 'Renamed' });
    core = { id: 'core-a', name: 'Core A', ready: true, client: { createProject: create, updateProject: update } } as unknown as Core;
    saved = vi.fn();
    editors = new ProjectEditors(saved);
  });
  afterEach(() => { editors.dispose(); vi.clearAllMocks(); });

  it('cancel and invalid input create nothing; valid submit follows the new project only after success', async () => {
    editors.open(core);
    panels[0].receive(save('   '));
    expect(create).not.toHaveBeenCalled();
    panels[0].receive({ type: 'cancel' });
    expect(panels[0].disposed).toBe(true);
    expect(saved).not.toHaveBeenCalled();
    editors.open(core);
    panels[1].receive(save(' Website '));
    await vi.waitFor(() => expect(saved).toHaveBeenCalledWith(core, project, true));
    expect(create).toHaveBeenCalledWith({ name: 'Website', description: 'A website' });
    expect(panels[1].disposed).toBe(true);
  });

  it('keeps the form and its input after failure and permits retry without duplicate in-flight saves', async () => {
    let reject!: (error: Error) => void;
    create.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));
    editors.open(core);
    const html = panels[0].webview.html;
    panels[0].receive(save());
    panels[0].receive(save());
    expect(create).toHaveBeenCalledTimes(1);
    reject(new ApiError(503, 'unavailable', 'Core did not answer.'));
    await vi.waitFor(() => expect(panels[0].webview.postMessage).toHaveBeenCalledWith({ type: 'error', message: 'Project not created: Core did not answer.' }));
    expect(panels[0].disposed).toBe(false);
    expect(panels[0].webview.html).toBe(html);
    expect(saved).not.toHaveBeenCalled();
    panels[0].receive(save());
    await vi.waitFor(() => expect(saved).toHaveBeenCalledOnce());
    expect(create).toHaveBeenCalledTimes(2);
  });

  it('edits just the changed fields and avoids requests for an unchanged form', async () => {
    editors.open(core, project);
    panels[0].receive(save('Renamed'));
    await vi.waitFor(() => expect(saved).toHaveBeenCalledWith(core, expect.objectContaining({ name: 'Renamed' }), false));
    expect(update).toHaveBeenCalledWith(project.id, { name: 'Renamed' });
    editors.open(core, project);
    panels[1].receive(save());
    expect(update).toHaveBeenCalledTimes(1);
    expect(panels[1].disposed).toBe(true);
  });

  it('keeps forms for the same project id on different Cores separate, and closes only the deleted project’s form', () => {
    const other = { ...core, id: 'core-b' } as Core;
    editors.open(core, project);
    editors.open(core, project);
    expect(panels).toHaveLength(1);
    expect(panels[0].reveal).toHaveBeenCalledOnce();
    editors.open(other, project);
    editors.closeProject(core.id, project.id);
    expect(panels[0].disposed).toBe(true);
    expect(panels[1].disposed).toBe(false);
    editors.closeCore(other.id);
    expect(panels[1].disposed).toBe(true);
  });

  it('refuses a save after its Core disconnects, leaving the form ready for retry', () => {
    const offline = { ...core, ready: false } as Core;
    editors.open(offline);
    panels[0].receive(save());
    expect(create).not.toHaveBeenCalled();
    expect(saved).not.toHaveBeenCalled();
    expect(panels[0].webview.postMessage).toHaveBeenCalledWith({ type: 'error', message: 'Project not created: Core A is not connected. Sign in or refresh, then try again.' });
    expect(panels[0].disposed).toBe(false);
  });
});
