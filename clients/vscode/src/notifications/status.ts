import * as vscode from 'vscode';

import { hasUnreadWarning } from '../../../shared/notifications';
import type { Notification } from '../api/types';
import { notifyAgentWarnings } from '../config';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import { openSession } from '../sessions';
import { notificationSummary, oneLine } from '../tree/model';

/**
 * "$(bell) N" in the status bar while agents told the person something they
 * have not read, on any Core, next to what waits. Hidden when nothing is
 * unread; on a warning background as soon as one of them is a WARNING.
 */
export class NotificationStatus implements vscode.Disposable {
  private readonly item = vscode.window.createStatusBarItem('threavia.notifications', vscode.StatusBarAlignment.Left, 49);
  private readonly subscriptions: vscode.Disposable[];

  constructor(private readonly cores: Cores) {
    this.item.name = 'Threavia: Notifications';
    this.item.command = 'threavia.showNotifications';
    this.subscriptions = [cores.onNotifications(() => this.update()), cores.onDidChange(() => this.update())];
    this.update();
  }

  private update() {
    const { count, warning, tooltip } = notificationSummary(
      this.cores.list().map((core) => ({
        name: core.name,
        count: core.notifications.current.length,
        warning: hasUnreadWarning(core.notifications.current),
      })),
    );
    if (count === 0) {
      this.item.hide();
      return;
    }
    this.item.text = `$(bell-dot) ${count}`;
    this.item.tooltip = tooltip;
    this.item.backgroundColor = warning ? new vscode.ThemeColor('statusBarItem.warningBackground') : undefined;
    this.item.show();
  }

  dispose() {
    for (const subscription of this.subscriptions) subscription.dispose();
    this.item.dispose();
  }
}

/**
 * Says so when an agent raises a new WARNING while the editor is open, the
 * counterpart of the phone's push, which stays silent while a client is in
 * use. Only what arrives after the first read: what was already unread is in
 * the bell, and the same warning raised again only counts once more.
 */
export class WarningAnnouncer implements vscode.Disposable {
  private readonly seen = new Map<string, Set<string>>();
  private readonly subscriptions: vscode.Disposable[];

  constructor(cores: Cores) {
    this.subscriptions = [
      cores.onNotifications((core) => this.announce(core)),
      cores.onDidRemove((coreId) => void this.seen.delete(coreId)),
    ];
  }

  private announce(core: Core) {
    if (!core.notifications.ready) return;
    const known = this.seen.get(core.id);
    const current = core.notifications.current;
    this.seen.set(core.id, new Set([...(known ?? []), ...current.map((notification) => notification.id)]));
    if (!known || !notifyAgentWarnings()) return;
    for (const notification of current) {
      if (notification.level === 'WARNING' && !known.has(notification.id)) void this.show(core, notification);
    }
  }

  private async show(core: Core, notification: Notification) {
    const choice = await vscode.window.showWarningMessage(
      core.label(`${oneLine(notification.title, 120)} — ${oneLine(notification.message, 160)}`),
      'Join the Conversation',
      'Dismiss',
    );
    if (!choice) return;
    await core.notifications.markRead(notification.id).catch(() => undefined);
    if (choice === 'Join the Conversation') await openSession({ coreId: core.id, sessionId: notification.scope.sessionId });
  }

  dispose() {
    for (const subscription of this.subscriptions) subscription.dispose();
  }
}
