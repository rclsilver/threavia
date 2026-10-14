import type * as vscode from 'vscode';

/** Resolve the destination after loading: opening a Session may have moved the groups. */
export type EditorColumn = vscode.ViewColumn | (() => Promise<vscode.ViewColumn>);

export async function resolveColumn(column: EditorColumn): Promise<vscode.ViewColumn> {
  return typeof column === 'function' ? column() : column;
}
