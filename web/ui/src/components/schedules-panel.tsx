import { Pause, Play, Send, Trash2 } from 'lucide-react';
import { useState } from 'react';

import {
  useCreateSchedule,
  useDeleteSchedule,
  useRunSchedule,
  useSchedules,
  useUpdateSchedule,
} from '@/api/queries';
import type { Schedule } from '@/api/types';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { ConfirmAction } from '@/components/ui/confirm';
import { Input, Textarea } from '@/components/ui/input';

/** The zone the browser runs in, which is where the person reading it lives. */
const LOCAL_ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone || 'Europe/Paris';

const OUTCOMES: Record<string, string> = {
  SENT: 'sent',
  SKIPPED_BUSY: 'skipped, the previous job was still running',
  SKIPPED_MISSED: 'skipped, it could not be sent on time',
  FAILED: 'failed',
};

/** A date as a person says it: "tomorrow at 08:00", "Mon 13 Oct at 08:00". */
function spoken(iso: string, zone: string): string {
  const date = new Date(iso);
  const day = new Intl.DateTimeFormat(undefined, { timeZone: zone, dateStyle: 'full' });
  const time = new Intl.DateTimeFormat(undefined, { timeZone: zone, timeStyle: 'short' });
  const today = day.format(new Date());
  const tomorrow = day.format(new Date(Date.now() + 24 * 60 * 60 * 1000));
  const which = day.format(date);
  const label =
    which === today
      ? 'today'
      : which === tomorrow
        ? 'tomorrow'
        : new Intl.DateTimeFormat(undefined, {
            timeZone: zone,
            weekday: 'short',
            day: 'numeric',
            month: 'short',
          }).format(date);
  return `${label} at ${time.format(date)}`;
}

/**
 * Messages this Session receives on a schedule.
 *
 * The expression is cron, because that is what people who schedule things
 * already write; what it means is shown right under it as dates, because that
 * is what they actually want to check.
 */
export function SchedulesPanel({ sessionId }: { sessionId: string }) {
  const schedules = useSchedules(sessionId);
  const create = useCreateSchedule(sessionId);
  const [cron, setCron] = useState('0 8 * * *');
  const [message, setMessage] = useState('');

  return (
    <div className="space-y-3">
      {(schedules.data ?? []).map((schedule) => (
        <ScheduleRow key={schedule.id} sessionId={sessionId} schedule={schedule} />
      ))}

      <form
        className="border-border space-y-2 rounded-md border border-dashed p-3"
        onSubmit={(event) => {
          event.preventDefault();
          create.mutate(
            { cron, message, timezone: LOCAL_ZONE },
            { onSuccess: () => setMessage('') },
          );
        }}
      >
        <div className="flex flex-wrap items-center gap-2">
          <Input
            value={cron}
            onChange={(event) => setCron(event.target.value)}
            className="w-40 font-mono"
            aria-label="When, as a cron expression"
            placeholder="0 8 * * mon-fri"
          />
          <span className="text-muted text-xs">minute hour day month weekday · {LOCAL_ZONE}</span>
        </div>
        <Textarea
          value={message}
          onChange={(event) => setMessage(event.target.value)}
          rows={2}
          placeholder="The message to send, as you would type it"
        />
        <ActionError error={create.error} />
        <Button type="submit" variant="secondary" size="sm" disabled={create.isPending || !message.trim()}>
          Add a schedule
        </Button>
      </form>
    </div>
  );
}

function ScheduleRow({ sessionId, schedule }: { sessionId: string; schedule: Schedule }) {
  const update = useUpdateSchedule(sessionId);
  const remove = useDeleteSchedule(sessionId);
  const run = useRunSchedule(sessionId);

  return (
    <div className="border-border space-y-1.5 rounded-md border p-3">
      <div className="flex items-start gap-2">
        <code className="bg-surface-2 rounded px-1.5 py-0.5 text-xs">{schedule.cron}</code>
        <p className="min-w-0 flex-1 text-sm break-words whitespace-pre-wrap">{schedule.message}</p>
        <div className="flex shrink-0 items-center">
          <Button
            variant="ghost"
            size="icon"
            title="Send it now"
            disabled={run.isPending}
            onClick={() => run.mutate(schedule.id)}
          >
            <Send />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            title={schedule.enabled ? 'Pause' : 'Resume'}
            disabled={update.isPending}
            onClick={() => update.mutate({ id: schedule.id, enabled: !schedule.enabled })}
          >
            {schedule.enabled ? <Pause /> : <Play />}
          </Button>
          <ConfirmAction
            question="Delete?"
            confirm="Delete"
            pending={remove.isPending}
            onConfirm={() => remove.mutate(schedule.id)}
            trigger={(ask) => (
              <Button variant="ghost" size="icon" title="Delete this schedule" onClick={ask}>
                <Trash2 />
              </Button>
            )}
          />
        </div>
      </div>
      <p className="text-muted text-xs">
        {schedule.enabled && schedule.upcoming.length > 0
          ? `Next ${schedule.upcoming.map((at) => spoken(at, schedule.timezone)).join(', then ')} (${schedule.timezone})`
          : 'Paused.'}
      </p>
      {schedule.lastRunAt && schedule.lastOutcome && (
        <p className="text-muted text-xs">
          Last {spoken(schedule.lastRunAt, schedule.timezone)}: {OUTCOMES[schedule.lastOutcome] ?? schedule.lastOutcome}
        </p>
      )}
      <ActionError error={update.error ?? run.error ?? remove.error} />
    </div>
  );
}
