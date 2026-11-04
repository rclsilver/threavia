import type { ReactNode } from 'react';
import { AlarmClockOff } from 'lucide-react';

import { useSnoozeSession } from '@/api/queries';
import type { Session } from '@/api/types';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '@/components/ui/menu';
import { isSnoozed, snoozeBack, snoozeChoices } from '../../../../clients/shared/sessions';

/** When a choice brings the Session back, in a word or two. */
function when(until: Date | undefined, now: Date): string {
  if (!until) return 'new message';
  const time = until.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  return until.toDateString() === now.toDateString() ? time : `${until.toLocaleDateString(undefined, { weekday: 'short' })} ${time}`;
}

/**
 * The ways to put a Session out of the way, and to bring it back sooner. It
 * comes back unread, on every device; a request waiting for the person brings
 * it back at once.
 */
export function SnoozeMenu({ session, children, align = 'end' }: { session: Session; children: ReactNode; align?: 'start' | 'end' }) {
  return (
    <Menu>
      <MenuTrigger asChild>{children}</MenuTrigger>
      <MenuContent align={align} className="w-60">
        <SnoozeItems session={session} />
      </MenuContent>
    </Menu>
  );
}

/** The choices themselves, for a menu of their own or a submenu of another. */
export function SnoozeItems({ session }: { session: Session }) {
  const snooze = useSnoozeSession();
  const now = new Date();
  const snoozed = isSnoozed(session);
  return (
    <>
      {snoozed && (
        <>
          <MenuLabel>Back {session.snoozedUntil ? snoozeBack(session) : 'at the next message'}</MenuLabel>
          <MenuItem className="max-md:min-h-11" onSelect={() => snooze.mutate({ sessionId: session.id })}>
            <AlarmClockOff className="size-4" /> Wake up now
          </MenuItem>
          <MenuSeparator />
        </>
      )}
      <MenuLabel className="font-medium tracking-wide uppercase">Snooze until</MenuLabel>
      {snoozeChoices(now).map((choice) => (
        <MenuItem
          key={choice.id}
          className="justify-between max-md:min-h-11"
          onSelect={() => snooze.mutate({ sessionId: session.id, until: choice.until, untilChange: !choice.until })}
        >
          <span>{choice.label}</span>
          <span className="text-muted text-xs">{when(choice.until, now)}</span>
        </MenuItem>
      ))}
    </>
  );
}
