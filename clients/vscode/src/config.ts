import * as vscode from 'vscode';

import { hostOf, normaliseUrl, readCores, validateUrl, type CoreSetting, type LegacySettings } from './cores/settings';

export { normaliseUrl, validateUrl } from './cores/settings';

const CORES = 'cores';

/** The Cores the settings list, made usable (see readCores). */
export function coreSettings(): { cores: CoreSetting[]; changed: boolean } {
  return readCores(vscode.workspace.getConfiguration('threavia').get<unknown>(CORES));
}

/**
 * Saves the list of Cores, in the user settings: a Core and its sign-in are
 * the person's, whichever folder is open.
 */
export async function saveCores(cores: CoreSetting[]): Promise<void> {
  await vscode.workspace.getConfiguration('threavia').update(CORES, cores, vscode.ConfigurationTarget.Global);
}

/** What an earlier version kept, for the one migration to `threavia.cores`. */
export function legacySettings(): LegacySettings {
  const config = vscode.workspace.getConfiguration('threavia');
  return {
    cores: config.inspect<unknown>(CORES)?.globalValue,
    coreUrl: config.get<string>('coreUrl', ''),
    project: config.get<string>('project', ''),
  };
}

/** Asks for a Core's address. Resolves to it normalised, or undefined when cancelled. */
export async function promptUrl(title: string, value = ''): Promise<string | undefined> {
  const input = await vscode.window.showInputBox({
    title,
    prompt: 'Where Threavia Core answers. http://localhost:8080 for a local Core.',
    placeHolder: 'https://threavia.example.com',
    value,
    ignoreFocusOut: true,
    validateInput: validateUrl,
  });
  return input === undefined ? undefined : normaliseUrl(input);
}

/** Asks what a Core is called, its host being the answer that needs no typing. */
export async function promptName(title: string, url: string, value?: string): Promise<string | undefined> {
  const input = await vscode.window.showInputBox({
    title,
    prompt: 'What the sidebar and the notifications call this Core.',
    value: value ?? hostOf(url),
    ignoreFocusOut: true,
    validateInput: (text) => (text.trim() ? undefined : 'A Core needs a name.'),
  });
  return input === undefined ? undefined : input.trim();
}

export function notifyAttention(): boolean {
  return vscode.workspace.getConfiguration('threavia').get<boolean>('notifications.attention', true);
}

export function notifyJobEnded(): boolean {
  return vscode.workspace.getConfiguration('threavia').get<boolean>('notifications.jobEnded', true);
}
