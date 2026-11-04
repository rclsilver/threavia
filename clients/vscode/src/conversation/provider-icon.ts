import * as vscode from 'vscode';
import { join } from 'node:path';

const icons: Record<string, string> = { claude: 'claude.svg', codex: 'codex.png', ollama: 'ollama.svg' };

export function providerIcon(provider?: string): vscode.Uri | vscode.ThemeIcon {
  const file = icons[provider?.toLowerCase() ?? ''];
  return file ? vscode.Uri.file(join(__dirname, '..', 'media', 'providers', file)) : new vscode.ThemeIcon('server');
}
