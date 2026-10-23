import * as vscode from 'vscode';
import type { Core } from '../cores/core';
import type { components } from '../api/schema';
import { pickBackend, pickProject } from '../start/pickers';

type Server = components['schemas']['MCPServer'];
type Binding = components['schemas']['MCPBinding'];
type Restriction = components['schemas']['MCPRestriction'];

/** Native selection and diagnostics. Complete configuration stays in Web. */
export async function manageMCP(core: Core) {
  for (;;) {
    const action = await vscode.window.showQuickPick(['List servers and test a connection', 'Activate servers in a project', 'Restrict servers for a session', 'Open MCP configuration in Web'], { title: 'MCP servers', ignoreFocusOut: true });
    if (!action) return;
    if (action === 'Open MCP configuration in Web') { await vscode.env.openExternal(vscode.Uri.parse(core.setting.url + '/settings/mcp')); continue; }
    const { items: servers } = await core.client.get<{ items: Server[] }>('/api/v1/me/mcp');
    if (action === 'List servers and test a connection') {
      const selected = await vscode.window.showQuickPick(servers.map(s => ({ label: s.name, description: `${s.transport} · ${s.enabled ? 'enabled' : 'disabled'}`, detail: `${s.tools.length} selected tools · credentials belong to each backend`, server: s })), { title: 'MCP server to inspect', ignoreFocusOut: true });
      if (!selected) continue;
      const backend = await pickBackend(core.client, 'Test MCP connection'); if (!backend) continue;
      const result = await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: `Testing ${selected.server.name} on ${backend.name}` }, () => core.client.post<Record<string, unknown>>(`/api/v1/backends/${backend.id}/mcp`, { operation: 'probe', serverId: selected.server.id }));
      const document = await vscode.workspace.openTextDocument({ language: 'json', content: JSON.stringify(result, null, 2) }); await vscode.window.showTextDocument(document, { preview: true }); continue;
    }
    const project = await pickProject(core.client, '', 'MCP selection', 'Choose the project whose MCP selection to manage', core.id); if (!project) continue;
    const { items: bindings } = await core.client.get<{ items: Binding[] }>(`/api/v1/projects/${project.id}/mcp`);
    if (action === 'Activate servers in a project') {
      const selected = await vscode.window.showQuickPick(servers.map(s => ({ label: s.name, picked: bindings.some(b => b.serverId === s.id && b.enabled), server: s })), { title: `MCP servers in ${project.name}`, canPickMany: true, ignoreFocusOut: true }); if (!selected) continue;
      const required = await vscode.window.showQuickPick(selected.map(s => ({ ...s, picked: bindings.some(b => b.serverId === s.server.id && b.required) })), { title: 'Which selected servers are required for startup?', canPickMany: true, ignoreFocusOut: true }); if (!required) continue;
      for (const s of servers) { const enabled = selected.some(v => v.server.id === s.id); const mustRun = required.some(v => v.server.id === s.id); const previous = bindings.find(b => b.serverId === s.id); if (enabled !== (previous?.enabled ?? false) || mustRun !== (previous?.required ?? false)) await core.client.request('PUT', `/api/v1/projects/${project.id}/mcp`, { serverId: s.id, enabled, required: mustRun }); }
    } else {
      const sessions = await core.client.sessions(project.id);
      const picked = await vscode.window.showQuickPick(sessions.map(s => ({ label: s.title || 'Untitled session', session: s })), { title: 'Session MCP selection', ignoreFocusOut: true }); if (!picked) continue;
      const sessionId = picked.session.id;
      const current = await core.client.get<Restriction>(`/api/v1/sessions/${sessionId}/mcp`);
      const mode = await vscode.window.showQuickPick(['Use project selection', 'Restrict this session'], { title: picked.label, ignoreFocusOut: true }); if (!mode) continue;
      let serverIds: string[] | null = null;
      if (mode === 'Restrict this session') {
        const selected = await vscode.window.showQuickPick(servers.filter(s => bindings.some(b => b.serverId === s.id && b.enabled)).map(s => ({ label: s.name, id: s.id || '', picked: !current.restricted || (current.serverIds || []).includes(s.id || '') })), { title: 'Servers available to subsequent Jobs', canPickMany: true, ignoreFocusOut: true }); if (!selected) continue; serverIds = selected.map(s => s.id);
      }
      await core.client.request('PUT', `/api/v1/sessions/${sessionId}/mcp`, { serverIds });
    }
    void vscode.window.showInformationMessage('MCP selection saved. Changes apply to the next Job.');
  }
}
