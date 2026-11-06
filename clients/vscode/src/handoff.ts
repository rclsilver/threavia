import * as vscode from 'vscode';

import { backendTargets } from '../../shared/composer';
import type { CoreClient } from './api/client';
import type { HandoffPreview } from './api/types';
import { currentRun } from './conversation/state';
import { providerIcon } from './conversation/provider-icon';

/**
 * Moves a Session to another backend from its row: the destination is picked,
 * Core's preview read, and the move confirmed with what it costs — the same
 * checks as the Web session settings and `/backend`. Resolves true once moved.
 */
export async function moveSessionToBackend(client: CoreClient, sessionId: string): Promise<boolean> {
  const [snapshot, backends] = await Promise.all([client.snapshot(sessionId, 1), client.backends()]);
  const currentId = currentRun(snapshot.runs)?.backendInstanceId;
  const here = backends.find((backend) => backend.id === currentId);
  const targets = backendTargets(backends, currentId);
  const title = `Move “${snapshot.session.title || 'Untitled session'}”`;
  if (targets.length === 0) {
    void vscode.window.showInformationMessage('No other backend can take this session: they are revoked, full, draining or not set up for code.');
    return false;
  }
  const picked = await vscode.window.showQuickPick(
    targets.map((target) => ({
      label: target.name,
      description: target.detail,
      iconPath: providerIcon(backends.find((backend) => backend.id === target.id)?.backend),
      target,
    })),
    { title, placeHolder: here ? `It runs on ${here.name}. Which backend takes it?` : 'Which backend takes it?' },
  );
  if (!picked) return false;
  const { target } = picked;
  const preview = await client.handoffPreview(sessionId, target.id);
  if (!preview.canMove) {
    void vscode.window.showWarningMessage(`Not moved to ${target.name}: ${preview.reasons.join(' ')}`);
    return false;
  }
  const answer = await vscode.window.showInformationMessage(
    `Move to ${target.name}?`,
    { modal: true, detail: handoffNotes(preview).join('\n\n') },
    'Move',
  );
  if (answer !== 'Move') return false;
  const moved = await client.moveSessionToBackend(sessionId, target.id, preview.fromRunId);
  if (moved.missingSkills?.length) {
    void vscode.window.showWarningMessage(`Moved to ${target.name}. These local skills are not there: ${moved.missingSkills.join(', ')}.`);
  }
  return true;
}

/** What the confirmation says the move keeps, loses and changes. */
export function handoffNotes(preview: HandoffPreview): string[] {
  const directory = preview.workingDirectory;
  return [
    'The timeline stays. The provider session does not travel: the next message starts a fresh one there, with the recent exchanges, work summaries and changed-file names. Workspace files must already be on that machine.',
    directory.resolution === 'bound'
      ? `Working directory: ${directory.name} · ${directory.path}`
      : directory.resolution === 'resolve'
        ? `Working directory: ${directory.name} — the backend must find or confirm its path before work starts.`
        : 'Working directory: the backend’s default.',
    ...(preview.missingSkills.length ? [`Local skills missing there: ${preview.missingSkills.join(', ')}.`] : []),
    ...preview.warnings,
  ];
}
