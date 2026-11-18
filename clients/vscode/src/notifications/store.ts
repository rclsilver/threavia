import { sortNotifications } from '../../../shared/notifications';
import type { CoreClient } from '../api/client';
import type { EventBus } from '../api/events';
import type { Notification } from '../api/types';

/**
 * What agents told the person and is still unread, on one Core: the one copy
 * the sidebar, the status bar and the badge read. Current state, re-read from
 * /me/notifications whenever the stream says it may have changed.
 */
export class NotificationStore {
  private list: Notification[] = [];
  private loaded = false;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private inflight: Promise<void> | null = null;
  private again = false;
  private readonly listeners = new Set<(list: readonly Notification[]) => void>();

  constructor(
    private readonly client: Pick<CoreClient, 'notifications' | 'markNotificationRead' | 'markNotificationsRead'>,
    bus: Pick<EventBus, 'on'>,
  ) {
    bus.on('effect', (effect) => {
      if (effect.kind === 'notifications') this.schedule();
    });
  }

  /** Unread notifications, WARNING first then the most recent. */
  get current(): readonly Notification[] {
    return this.list;
  }

  /** False until the first answer, so nothing reads an empty list as "nothing new". */
  get ready(): boolean {
    return this.loaded;
  }

  onChange(listener: (list: readonly Notification[]) => void): { dispose: () => void } {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  }

  /** Re-reads soon rather than now: a replay can carry many events, and one read answers them all. */
  schedule(delay = 150) {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.refresh(), delay);
  }

  /** Re-reads now. Concurrent calls share one request, plus one more after it. */
  async refresh(): Promise<void> {
    if (this.inflight) {
      this.again = true;
      return this.inflight;
    }
    this.inflight = (async () => {
      try {
        this.set(await this.client.notifications());
      } catch {
        // Kept as it was: the stream will ask again on the next change.
      } finally {
        this.inflight = null;
      }
      if (this.again) {
        this.again = false;
        await this.refresh();
      }
    })();
    return this.inflight;
  }

  /** Dismissing one, or joining its conversation: it goes at once, the stream confirms it. */
  async markRead(id: string): Promise<void> {
    this.set(this.list.filter((notification) => notification.id !== id), this.loaded);
    try {
      await this.client.markNotificationRead(id);
    } finally {
      this.schedule();
    }
  }

  async markAllRead(): Promise<void> {
    this.set([], this.loaded);
    try {
      await this.client.markNotificationsRead();
    } finally {
      this.schedule();
    }
  }

  /** Forgets everything, for a sign-out or another Core. */
  reset() {
    clearTimeout(this.timer);
    this.set([], false);
  }

  private set(list: readonly Notification[], loaded = true) {
    this.list = sortNotifications(list);
    this.loaded = loaded;
    for (const listener of this.listeners) listener(this.list);
  }

  dispose() {
    clearTimeout(this.timer);
    this.listeners.clear();
  }
}
