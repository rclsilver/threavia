import * as vscode from 'vscode';
import type { Core } from './cores/core';

export async function manageMonitors(core: Core, sessionId: string) {
 const monitors = await core.client.sessionMonitors(sessionId);
 if (!monitors.length) { void vscode.window.showInformationMessage('This session has no background monitors.'); return; }
 const selected = await vscode.window.showQuickPick(monitors.map((m) => ({ label: m.description, description: `${m.state.toLowerCase()}${m.confirmed ? '' : ' · activity not confirmed'}`, detail: m.command, monitor: m })), { title: 'Background monitors', placeHolder: 'Inspect a monitor or stop it' });
 if (!selected) return;
 const m = selected.monitor;
 const action = await vscode.window.showQuickPick(['Show output', ...(['STARTING', 'RUNNING', 'STOPPING'].includes(m.state) ? ['Stop monitor'] : [])], { title: m.description });
 if (action === 'Stop monitor') { await core.client.stopMonitor(m.id); return; }
 if (action === 'Show output') {
  const document = await vscode.workspace.openTextDocument({ language: 'plaintext', content: `${m.description}\n${m.state} · ${m.confirmed ? 'confirmed' : 'activity not confirmed'}\nStarted: ${m.startedAt}\nLast activity: ${m.updatedAt}\nExpires: ${m.expiresAt}\nAutomatic resumptions remaining: ${m.automaticResumesRemaining}\nCommand: ${m.command}\n\n${m.output}\n${m.error ?? ''}` });
  await vscode.window.showTextDocument(document);
 }
}
