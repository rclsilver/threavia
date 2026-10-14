import * as vscode from 'vscode';

/** A fresh conversation owns its group, rather than becoming another tab. */
export async function newConversationGroup(): Promise<vscode.ViewColumn> {
  if (vscode.window.tabGroups.activeTabGroup.tabs.length) {
    await vscode.commands.executeCommand('workbench.action.newGroupRight');
  }
  return vscode.window.tabGroups.activeTabGroup.viewColumn;
}

/** Also applies to restored conversations and conversations moved by the user. */
export async function protectConversationGroup(panel: vscode.WebviewPanel): Promise<void> {
  if (!panel.active || panel.viewColumn !== vscode.window.tabGroups.activeTabGroup.viewColumn) return;
  if (vscode.window.tabGroups.activeTabGroup.tabs.length > 1) {
    const column = await newConversationGroup();
    panel.reveal(column);
  }
  // Lock after opening: VS Code routes new editors away from an empty locked
  // group. Never toggle, since revealing a conversation must not unlock it.
  await vscode.commands.executeCommand('workbench.action.lockEditorGroup');
}

/** Explicit editor destinations must avoid every conversation's locked group. */
export async function documentGroup(conversationColumns: ReadonlySet<vscode.ViewColumn>): Promise<vscode.ViewColumn> {
  const available = vscode.window.tabGroups.all.find((group) => !conversationColumns.has(group.viewColumn));
  if (available) return available.viewColumn;
  await vscode.commands.executeCommand('workbench.action.newGroupRight');
  return vscode.window.tabGroups.activeTabGroup.viewColumn;
}
