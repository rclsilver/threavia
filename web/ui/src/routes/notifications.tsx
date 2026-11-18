import { useNavigate } from '@tanstack/react-router';
import { AlertTriangle, BellOff, Info } from 'lucide-react';

import { useMarkAllNotificationsRead, useMarkNotificationRead, useNotifications } from '@/api/queries';
import type { Notification } from '@/api/types';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { ago, cn } from '@/lib/utils';

/**
 * What agents told the person, WARNING first. A notification stays until it is
 * dismissed or its conversation is joined, which reads it on every device:
 * nothing marks it read by being on screen.
 */
export function NotificationsView() {
  const notifications = useNotifications();
  const markAll = useMarkAllNotificationsRead();
  const list = notifications.data ?? [];

  return (
    <div className="mx-auto flex min-h-0 w-full max-w-reading flex-1 flex-col gap-6 overflow-y-auto p-4 sm:p-6">
      <header className="flex items-start gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <h1 className="text-lg font-semibold">Notifications</h1>
          <p className="text-muted text-sm">
            {notifications.isPending
              ? 'Loading…'
              : notifications.isError
                ? 'Notifications could not be loaded.'
                : list.length === 0
                  ? 'Nothing from your agents.'
                  : `${list.length} unread, warnings first.`}
          </p>
        </div>
        {list.length > 1 && (
          <Button variant="ghost" size="sm" disabled={markAll.isPending} onClick={() => markAll.mutate()}>
            Dismiss all
          </Button>
        )}
      </header>

      {notifications.isError && (
        <div>
          <ActionError error={notifications.error} outcome="Notifications did not load" />
          <Button onClick={() => void notifications.refetch()}>Try again</Button>
        </div>
      )}
      <ActionError error={markAll.error} outcome="The notifications were not dismissed" />

      {notifications.isSuccess && list.length === 0 && (
        <div className="text-muted flex items-center gap-2 text-sm">
          <BellOff className="size-4" />
          Agents raise a notification when something deserves your attention.
        </div>
      )}

      <ul className="space-y-3">
        {list.map((notification) => (
          <NotificationItem key={notification.id} notification={notification} />
        ))}
      </ul>
    </div>
  );
}

function NotificationItem({ notification }: { notification: Notification }) {
  const read = useMarkNotificationRead();
  const navigate = useNavigate();
  const warning = notification.level === 'WARNING';
  const Icon = warning ? AlertTriangle : Info;

  // Joining opens the conversation at once; reading it follows on its own and
  // reaches the other devices through the stream.
  const join = () => {
    read.mutate(notification.id);
    void navigate({ to: '/sessions/$sessionId', params: { sessionId: notification.scope.sessionId } });
  };

  return (
    <li
      className={cn(
        'border-border bg-surface space-y-2 rounded-lg border p-3',
        warning && 'border-warn/60 bg-warn/10',
      )}
    >
      <div className="flex items-start gap-2">
        <Icon className={cn('mt-0.5 size-4 shrink-0', warning ? 'text-warn-text' : 'text-accent')} />
        <div className="min-w-0 flex-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-semibold">
            <span className="min-w-0 break-words">{notification.title}</span>
            <Badge tone={warning ? 'warn' : 'neutral'}>{notification.level}</Badge>
            {notification.count > 1 && <Badge className="figures">×{notification.count}</Badge>}
          </p>
          <p className="text-muted mt-1 text-sm break-words whitespace-pre-wrap">{notification.message}</p>
          <p className="text-muted mt-1 text-xs">{ago(notification.updatedAt)}</p>
        </div>
      </div>
      <div className="flex justify-end gap-2">
        <Button variant="ghost" size="sm" disabled={read.isPending} onClick={() => read.mutate(notification.id)}>
          Dismiss
        </Button>
        <Button size="sm" disabled={read.isPending} onClick={join}>
          Join the conversation
        </Button>
      </div>
      <ActionError error={read.error} outcome="The notification was not dismissed" />
    </li>
  );
}
