import * as vscode from 'vscode';
import type { BackendInstance, BackendQuota } from '../api/types';
import type { Cores } from '../cores/registry';
import { backendIcon, backendTooltip, quotaSummary, quotaText } from '../conversation/backend';
import { humanise } from '../conversation/format';

type Node = { type: 'core'; coreId: string } | { type: 'backend'; backend: BackendInstance; coreId: string }
  | { type: 'quota'; quota: BackendQuota } | { type: 'message'; text: string; details?: string };

/** Native tree rows keep backend status usable in every editor theme. */
export class BackendsView implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<undefined>();
  readonly onDidChangeTreeData = this.changed.event;
  private readonly subscriptions: vscode.Disposable[] = [];
  constructor(private readonly cores: Cores) {
    this.subscriptions.push(
      vscode.window.createTreeView('threavia.backends', { treeDataProvider: this, showCollapseAll: true }),
      cores.onDidChange(() => this.changed.fire(undefined)),
      cores.on('effect', (_core, effect) => { if (effect.kind === 'backends') this.changed.fire(undefined); }),
      cores.on('connection', () => this.changed.fire(undefined)),
    );
  }
  reload() {
    for (const core of this.cores.list()) core.invalidateBackends();
    this.changed.fire(undefined);
  }
  async getChildren(node?: Node): Promise<Node[]> {
    if (!node) {
      if (this.cores.several) return this.cores.list().map((core) => ({ type: 'core', coreId: core.id }));
      const core = this.cores.list()[0];
      return core ? this.getChildren({ type: 'core', coreId: core.id }) : [{ type: 'message', text: 'Add a Core to see backends.' }];
    }
    if (node.type === 'core') {
      const core = this.cores.get(node.coreId);
      if (!core?.ready) return [{ type: 'message', text: core ? humanise(core.state) : 'Core removed' }];
      try {
        const backends = await core.backends();
        return backends.length ? backends.map((backend) => ({ type: 'backend', backend, coreId: core.id })) : [{ type: 'message', text: 'No backend registered yet.' }];
      } catch { return [{ type: 'message', text: 'Core did not answer. Refresh to try again.' }]; }
    }
    if (node.type === 'backend') {
      const snapshot = node.backend.quotas;
      if (!snapshot) return [{ type: 'message', text: 'No quota report received yet.' }];
      return [
        { type: 'message', text: `Quotas: ${humanise(snapshot.availability)}`, details: quotaText(snapshot) },
        { type: 'message', text: `Observed: ${new Date(snapshot.observedAt).toLocaleString()}` },
        ...(snapshot.message ? [{ type: 'message' as const, text: snapshot.message }] : []),
        ...snapshot.limits.map((quota) => ({ type: 'quota' as const, quota })),
        ...(snapshot.details ? [{ type: 'message' as const, text: 'Provider details', details: JSON.stringify(snapshot.details, null, 2) }] : []),
      ];
    }
    if (node.type === 'quota') {
      const quota = node.quota;
      const children: Node[] = [];
      for (const field of ['used', 'limit', 'remaining'] as const) if (quota[field] !== undefined) children.push({ type: 'message', text: `${humanise(field)}: ${quota[field]} ${quota.unit ?? ''}` });
      if (quota.windowSeconds) children.push({ type: 'message', text: `Window: ${quota.windowSeconds / 60} min` });
      if (quota.resetsAt) children.push({ type: 'message', text: `Resets: ${new Date(quota.resetsAt).toLocaleString()}` });
      for (const threshold of quota.thresholds ?? []) children.push({ type: 'message', text: `${threshold.label}: ${threshold.value ?? 'not reported'} ${threshold.unit ?? ''} ${threshold.status ?? ''}` });
      if (quota.details) children.push({ type: 'message', text: 'Provider details', details: JSON.stringify(quota.details, null, 2) });
      return children;
    }
    return [];
  }
  getTreeItem(node: Node): vscode.TreeItem {
    if (node.type === 'core') return new vscode.TreeItem(this.cores.get(node.coreId)?.name ?? 'Core removed', vscode.TreeItemCollapsibleState.Expanded);
    if (node.type === 'backend') {
      const item = new vscode.TreeItem(node.backend.name, vscode.TreeItemCollapsibleState.Expanded);
      item.id = `backend/${node.coreId}/${node.backend.id}`;
      item.iconPath = new vscode.ThemeIcon(backendIcon(node.backend.backend));
      item.description = humanise(node.backend.operationalStatus);
      item.tooltip = backendTooltip(node.backend);
      return item;
    }
    if (node.type === 'quota') {
      const item = new vscode.TreeItem(node.quota.label, vscode.TreeItemCollapsibleState.Collapsed);
      item.description = quotaSummary(node.quota);
      item.tooltip = JSON.stringify(node.quota, null, 2);
      item.iconPath = new vscode.ThemeIcon('dashboard');
      return item;
    }
    const item = new vscode.TreeItem(node.text);
    item.tooltip = node.details ?? node.text;
    return item;
  }
  dispose() { this.changed.dispose(); for (const subscription of this.subscriptions) subscription.dispose(); }
}
