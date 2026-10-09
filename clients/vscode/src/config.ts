import * as vscode from 'vscode';

/**
 * Where Core is. Empty until the person says: there is no sensible default, a
 * laptop Core and a deployed one being equally likely.
 */
export function coreUrl(): string {
  return normaliseUrl(vscode.workspace.getConfiguration('threavia').get<string>('coreUrl', ''));
}

/** The URL as requests are built from it: trimmed, without a trailing slash. */
export function normaliseUrl(raw: string): string {
  return raw.trim().replace(/\/+$/, '');
}

/** Says what is wrong with a URL, or nothing when it will do. */
export function validateUrl(raw: string): string | undefined {
  const value = normaliseUrl(raw);
  if (!value) return 'Enter the address Core answers on.';
  try {
    const url = new URL(value);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'Use an http:// or https:// address.';
  } catch {
    return 'That is not an address, such as https://threavia.example.com.';
  }
  return undefined;
}

/** Asks for the Core URL and saves it. Resolves to the URL, or undefined when cancelled. */
export async function promptCoreUrl(): Promise<string | undefined> {
  const value = await vscode.window.showInputBox({
    title: 'Threavia Core URL',
    prompt: 'Where Threavia Core answers. http://localhost:8080 for a local Core.',
    placeHolder: 'https://threavia.example.com',
    value: coreUrl(),
    ignoreFocusOut: true,
    validateInput: validateUrl,
  });
  if (value === undefined) return undefined;
  const url = normaliseUrl(value);
  await vscode.workspace.getConfiguration('threavia').update('coreUrl', url, vscode.ConfigurationTarget.Global);
  return url;
}

/** The Project this workspace works in, by name or id; empty when none is named. */
export function currentProjectSetting(): string {
  return vscode.workspace.getConfiguration('threavia').get<string>('project', '');
}

export function notifyAttention(): boolean {
  return vscode.workspace.getConfiguration('threavia').get<boolean>('notifications.attention', true);
}

export function notifyJobEnded(): boolean {
  return vscode.workspace.getConfiguration('threavia').get<boolean>('notifications.jobEnded', true);
}
