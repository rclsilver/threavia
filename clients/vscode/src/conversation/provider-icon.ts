import * as vscode from 'vscode';
import { join } from 'node:path';

export function providerIcon(provider?: string): vscode.Uri | vscode.ThemeIcon {
  const name = provider?.toLowerCase();
  return name === 'claude' || name === 'codex'
    ? vscode.Uri.file(join(__dirname, '..', 'media', 'providers', name === 'claude' ? 'claude.svg' : 'codex.png'))
    : new vscode.ThemeIcon('server');
}
