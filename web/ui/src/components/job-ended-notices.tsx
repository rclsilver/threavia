import { useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { AlertTriangle, CheckCircle2, X } from 'lucide-react';
import { useEffect, useState } from 'react';

import { keys } from '@/api/keys';
import type { Session, Snapshot } from '@/api/types';
import { Button } from '@/components/ui/button';
import { onJobEnded, type JobEnded } from '@/lib/job-ended';
import { cn } from '@/lib/utils';

interface Notice extends JobEnded {
  id: number;
  title: string;
}

/** How long a notice stays, unless the pointer rests on it. */
const SHOWN_FOR = 10_000;

let next = 0;

/**
 * Says when work ends somewhere the person is not looking.
 *
 * On the Session itself the timeline already says it, so nothing more is
 * shown. Elsewhere in the client, a notice in the corner says which Session
 * finished and leads there. With the client in the background — another tab,
 * another window — the system says it, if the person allowed notifications,
 * under the same tag as Core's push for that Session, so one replaces the other
 * rather than ringing twice.
 */
export function JobEndedNotices() {
  const queries = useQueryClient();
  const { sessionId: open } = useParams({ strict: false });
  const [notices, setNotices] = useState<Notice[]>([]);

  useEffect(
    () =>
      onJobEnded((ended) => {
        const watching = document.visibilityState === 'visible' && document.hasFocus();
        if (watching && ended.sessionId === open) return;

        const title = `${ended.failed ? 'Failed' : 'Done'} · ${titleOf(queries, ended.sessionId)}`;
        if (!watching) notifySystem(title, ended);
        setNotices((current) => [...current.slice(-2), { ...ended, id: next++, title }]);
      }),
    [open, queries],
  );

  const dismiss = (id: number) => setNotices((current) => current.filter((notice) => notice.id !== id));

  if (notices.length === 0) return null;
  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed inset-x-3 top-16 z-50 flex flex-col gap-2 md:inset-x-auto md:top-auto md:right-6 md:bottom-6 md:w-96"
    >
      {notices.map((notice) => (
        <NoticeCard key={notice.id} notice={notice} onDismiss={() => dismiss(notice.id)} />
      ))}
    </div>
  );
}

function NoticeCard({ notice, onDismiss }: { notice: Notice; onDismiss: () => void }) {
  const [held, setHeld] = useState(false);
  useEffect(() => {
    if (held) return;
    const timer = setTimeout(onDismiss, SHOWN_FOR);
    return () => clearTimeout(timer);
  }, [held, onDismiss]);

  const Icon = notice.failed ? AlertTriangle : CheckCircle2;
  return (
    <div
      onPointerEnter={() => setHeld(true)}
      onPointerLeave={() => setHeld(false)}
      onFocus={() => setHeld(true)}
      className="bg-surface border-border flex items-start gap-3 rounded-xl border p-3 shadow-lg"
    >
      <Icon className={cn('mt-0.5 size-4 shrink-0', notice.failed ? 'text-danger' : 'text-ok')} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{notice.title}</p>
        {notice.text && <p className="text-muted mt-0.5 line-clamp-2 text-sm">{notice.text}</p>}
        <Link
          to="/sessions/$sessionId"
          params={{ sessionId: notice.sessionId }}
          onClick={onDismiss}
          className="text-accent mt-1.5 inline-flex min-h-11 items-center text-sm font-medium hover:underline sm:min-h-0"
        >
          Open the session
        </Link>
      </div>
      <Button variant="ghost" size="icon" className="-mt-1 -mr-1 size-11 sm:size-7" aria-label="Dismiss" onClick={onDismiss}>
        <X />
      </Button>
    </div>
  );
}

/** A Session's title, from whatever the client already holds about it. */
function titleOf(queries: ReturnType<typeof useQueryClient>, sessionId: string): string {
  const snapshot = queries.getQueryData<Snapshot>(keys.snapshot(sessionId));
  if (snapshot?.session.title) return snapshot.session.title;
  for (const [, list] of queries.getQueriesData<Session[]>({ queryKey: ['sessions'] })) {
    const found = list?.find((session) => session.id === sessionId);
    if (found?.title) return found.title;
  }
  return 'a session';
}

/** Through the service worker when there is one, the way Core's push arrives. */
function notifySystem(title: string, ended: JobEnded) {
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
  const options: NotificationOptions = {
    body: ended.text.slice(0, 240),
    icon: '/icon-192.png',
    tag: ended.sessionId,
    data: { url: `/sessions/${ended.sessionId}` },
  };
  if ('serviceWorker' in navigator) {
    void navigator.serviceWorker.getRegistration().then((registration) => {
      if (registration) void registration.showNotification(title, options);
      else new Notification(title, options);
    });
    return;
  }
  new Notification(title, options);
}
