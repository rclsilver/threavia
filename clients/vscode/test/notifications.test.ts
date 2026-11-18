import { afterEach, describe, expect, it, vi } from 'vitest';

import { hasUnreadWarning, sortNotifications, unreadNotifications } from '../../shared/notifications';
import { EventBus, effectsOf } from '../src/api/events';
import type { Event, Notification } from '../src/api/types';
import { NotificationStore } from '../src/notifications/store';
import { activityBadge, coreSections, notificationRow, notificationSummary } from '../src/tree/model';

const now = Date.parse('2026-11-18T12:00:00Z');
const notification = (id: string, level: 'INFO' | 'WARNING', minutesAgo: number, extra: Partial<Notification> = {}): Notification => ({
  id,
  scope: { projectId: 'p', sessionId: 's', runId: 'r', jobId: 'j' },
  level,
  title: `title ${id}`,
  message: `message ${id}`,
  count: 1,
  createdAt: new Date(now - minutesAgo * 60_000).toISOString(),
  updatedAt: new Date(now - minutesAgo * 60_000).toISOString(),
  ...extra,
});
const event = (type: string, extra: Partial<Event> = {}): Event => ({ id: 'e', sequence: 1, timestamp: new Date(now).toISOString(), type, sessionId: 's', ...extra });

describe('notifications, as both clients read them', () => {
  it('puts warnings first, then the most recently raised, without touching the input', () => {
    const list = [notification('old-info', 'INFO', 30), notification('warning', 'WARNING', 60), notification('new-info', 'INFO', 1)];
    expect(sortNotifications(list).map((n) => n.id)).toEqual(['warning', 'new-info', 'old-info']);
    expect(list[0].id).toBe('old-info');
  });

  it('counts only what is unread, and a read warning is no longer loud', () => {
    const list = [notification('w', 'WARNING', 1, { readAt: new Date(now).toISOString() }), notification('i', 'INFO', 1)];
    expect(unreadNotifications(list).map((n) => n.id)).toEqual(['i']);
    expect(hasUnreadWarning(list)).toBe(false);
    expect(hasUnreadWarning([notification('w', 'WARNING', 1)])).toBe(true);
  });
});

describe('what a notification event changes', () => {
  it('re-reads the notifications when one is raised or read, and when a session or project goes', () => {
    for (const type of ['notification.created', 'notification.read', 'session.deleted', 'session.project_moved', 'project.deleted']) {
      expect(effectsOf(event(type), now).map((effect) => effect.kind)).toContain('notifications');
    }
  });
});

describe('the notification store', () => {
  const stores: NotificationStore[] = [];
  afterEach(() => {
    for (const store of stores.splice(0)) store.dispose();
    vi.useRealTimers();
  });
  const make = (list: Notification[]) => {
    const client = {
      notifications: vi.fn(() => Promise.resolve(list)),
      markNotificationRead: vi.fn((id: string) => Promise.resolve(void id)),
      markNotificationsRead: vi.fn(() => Promise.resolve()),
    };
    const bus = new EventBus();
    const store = new NotificationStore(client, bus);
    stores.push(store);
    return { client, bus, store };
  };

  it('is not ready before the first read, then holds the unread list in order', async () => {
    const { store } = make([notification('i', 'INFO', 1), notification('w', 'WARNING', 5)]);
    expect(store.ready).toBe(false);
    await store.refresh();
    expect(store.ready).toBe(true);
    expect(store.current.map((n) => n.id)).toEqual(['w', 'i']);
  });

  it('drops a dismissed one at once and tells Core', async () => {
    const { client, store } = make([notification('i', 'INFO', 1), notification('w', 'WARNING', 5)]);
    await store.refresh();
    const seen: string[][] = [];
    store.onChange((list) => seen.push(list.map((n) => n.id)));
    await store.markRead('w');
    expect(seen[0]).toEqual(['i']);
    expect(client.markNotificationRead).toHaveBeenCalledWith('w');
    await store.markAllRead();
    expect(store.current).toEqual([]);
    expect(client.markNotificationsRead).toHaveBeenCalled();
  });

  it('re-reads when the stream says notifications changed, and forgets them on reset', async () => {
    vi.useFakeTimers();
    const { client, bus, store } = make([notification('i', 'INFO', 1)]);
    bus.emit('effect', { kind: 'notifications' });
    await vi.runAllTimersAsync();
    expect(client.notifications).toHaveBeenCalledTimes(1);
    expect(store.current).toHaveLength(1);
    store.reset();
    expect(store.ready).toBe(false);
    expect(store.current).toEqual([]);
  });
});

describe('notifications in the sidebar and the status bar', () => {
  it('shows a Notifications section right under Waiting', () => {
    expect(coreSections(0, [], '').map((section) => section.type)).toEqual(['waiting', 'notifications']);
  });

  it('says how often and how long ago on a row, and the whole message on hover', () => {
    const row = notificationRow(notification('w', 'WARNING', 5, { count: 3 }), now);
    expect(row).toEqual({ label: 'title w', description: '×3 · 5 min ago', tooltip: 'WARNING · title w\n\nmessage w' });
    expect(notificationRow(notification('i', 'INFO', 0), now).description).toBe('just now');
  });

  it('counts unread notifications in the badge and the bell, and says when one is a warning', () => {
    expect(activityBadge([{ name: 'home', waiting: 0, unread: 0, notifications: 2 }])).toEqual({ value: 2, tooltip: 'Threavia: 2 notifications' });
    expect(notificationSummary([{ name: 'home', count: 1, warning: false }])).toEqual({
      count: 1, warning: false, tooltip: '1 unread notification in Threavia',
    });
    const several = notificationSummary([{ name: 'home', count: 2, warning: true }, { name: 'lab', count: 0, warning: false }]);
    expect(several.warning).toBe(true);
    expect(several.tooltip.split('\n')).toEqual(['2 unread notifications in Threavia, with a warning', 'home: 2']);
  });
});
