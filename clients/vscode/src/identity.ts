import { randomUUID } from 'node:crypto';
import { hostname } from 'node:os';
import * as vscode from 'vscode';

import type { Identity } from './api/client';

const ID_KEY = 'threavia.clientId';

/**
 * Who this editor is to Core: a stable id and a name a person recognises.
 *
 * The id is kept in global state, so every window of this installation is one
 * client to Core however many streams it holds, and a reinstall is a new one.
 * The name is the hostname, because "VS Code" alone does not say which of the
 * person's machines is asking.
 */
export function createIdentity(context: vscode.ExtensionContext): Identity {
  let id = context.globalState.get<string>(ID_KEY);
  if (!id) {
    id = randomUUID();
    void context.globalState.update(ID_KEY, id);
  }
  return { clientId: id, clientName };
}

function clientName(): string {
  const chosen = vscode.workspace.getConfiguration('threavia').get<string>('clientName', '').trim();
  if (chosen) return chosen;
  return `${editorName(vscode.env.appName)} — ${hostname()}`;
}

/** "Visual Studio Code - Insiders" reads as "VS Code Insiders"; a fork keeps its name. */
export function editorName(appName: string): string {
  if (!appName.startsWith('Visual Studio Code')) return appName;
  return appName.includes('Insiders') ? 'VS Code Insiders' : 'VS Code';
}
