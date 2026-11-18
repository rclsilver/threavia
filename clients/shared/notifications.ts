/** What the clients need of a notification an agent raised, to sort and count it. */
export interface NotificationLike {
  id: string;
  level: 'INFO' | 'WARNING';
  updatedAt: string;
  readAt?: string | null;
}

/** WARNING first, then the most recently raised; the input is left as it was. */
export function sortNotifications<T extends NotificationLike>(notifications: readonly T[]): T[] {
  return [...notifications].sort(
    (a, b) =>
      Number(b.level === 'WARNING') - Number(a.level === 'WARNING') ||
      Date.parse(b.updatedAt) - Date.parse(a.updatedAt) ||
      (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
}

/** What is still to be read. */
export function unreadNotifications<T extends NotificationLike>(notifications: readonly T[]): T[] {
  return notifications.filter((notification) => !notification.readAt);
}

/** Whether something unread asks for attention, not only for a glance. */
export function hasUnreadWarning(notifications: readonly NotificationLike[]): boolean {
  return notifications.some((notification) => !notification.readAt && notification.level === 'WARNING');
}
