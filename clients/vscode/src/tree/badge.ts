import * as vscode from 'vscode';

import type { Cores } from '../cores/registry';
import { activityBadge, waitingCount } from './model';
import type { SidebarProvider } from './provider';

/**
 * The count on the Threavia icon of the activity bar: what waits, and the
 * Sessions holding something unread, on every Core. It matters most while the
 * view is closed, so the Projects are read again when a Job moves or a Session
 * is read, whether or not the tree is drawn.
 */
export class ActivityBadge implements vscode.Disposable {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly subscriptions: vscode.Disposable[];

  constructor(
    private readonly cores: Cores,
    private readonly sidebar: SidebarProvider,
    private readonly view: vscode.TreeView<unknown>,
  ) {
    this.subscriptions = [
      cores.onAttention(() => this.schedule()),
      cores.onDidChange(() => this.schedule()),
      cores.on('effect', (_core, effect) => {
        if (effect.kind === 'sessions' || effect.kind === 'projects') this.schedule();
      }),
    ];
    this.schedule();
  }

  /** Coalesces a burst, and lets the sidebar read the lists first when it is open. */
  private schedule() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.update(), 300);
  }

  private async update() {
    const activity = await Promise.all(this.cores.list().map(async (core) => ({
      name: core.name,
      waiting: waitingCount(core.attention.current),
      // A Core that cannot be asked counts what waits, as the status bar does.
      unread: core.ready ? await this.sidebar.unread(core).catch(() => 0) : 0,
    })));
    this.view.badge = activityBadge(activity);
  }

  dispose() {
    clearTimeout(this.timer);
    for (const subscription of this.subscriptions) subscription.dispose();
  }
}
