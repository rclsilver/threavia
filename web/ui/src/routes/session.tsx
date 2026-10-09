import { Link, useParams } from '@tanstack/react-router';
import { ArrowUp, Pin, Server, Settings2, Square } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';

import {
  useBackends,
  useCancelJob,
  usePostMessage,
  useRenameSession,
  useEarlierEvents,
  usePinSession,
  usePinnedSessions,
  useSnapshot,
  type Delivery,
} from '@/api/queries';
import type { Session } from '@/api/types';
import { AttentionPanel } from '@/components/attention';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { RepositoryStatus } from '@/components/repository-status';
import { Timeline, type Pending } from '@/components/timeline';
import { Button } from '@/components/ui/button';
import { useStream } from '@/use-stream';
import { useDraft } from '@/lib/draft';
import { cn, humanise } from '@/lib/utils';

const FINISHED = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);

export function SessionView() {
  const { sessionId } = useParams({ from: '/sessions/$sessionId' });
  const { seen, activity } = useStream();
  const snapshot = useSnapshot(sessionId);
  const cancel = useCancelJob(sessionId);
  const earlier = useEarlierEvents(sessionId);
  const backends = useBackends();
  const message = useRef<HTMLTextAreaElement>(null);

  // A click on the page with nothing under it means "back to writing", as in a
  // terminal: the field takes the focus. Not on a control, not after selecting
  // text to copy, not in a menu or dialog drawn over the page, and not on a
  // touch screen, where the focus opens a keyboard over what is being read.
  const focusMessage = (event: React.MouseEvent<HTMLElement>) => {
    const target = event.target as Element;
    if (event.defaultPrevented || !event.currentTarget.contains(target)) return;
    if (!window.matchMedia('(pointer: fine)').matches) return;
    if ((event.nativeEvent as PointerEvent).pointerType === 'touch') return;
    if (target.closest('a, button, input, textarea, select, label, summary, [role], [contenteditable="true"]')) return;
    if (window.getSelection()?.toString()) return;
    // A card with a field of its own — a question waiting for an answer —
    // takes the click for that field: it is what the person came to fill.
    const own = target.closest('[data-field-scope]')?.querySelector<HTMLElement>('input, textarea');
    (own ?? message.current)?.focus({ preventScroll: true });
  };

  // The snapshot is a point the stream has already passed, so a reconnection
  // resumes from here rather than replaying what is on screen.
  useEffect(() => {
    if (snapshot.data) seen(snapshot.data.cursor);
  }, [snapshot.data, seen]);

  // Escape stops what is running, as it interrupts Claude Code in a terminal.
  // It stops the same Job as the Stop button: the oldest one still going.
  // A second Escape while that one is stopping does nothing, rather than
  // reaching past it to the Job queued behind.
  const oldest = snapshot.data?.jobs.find((job) => !FINISHED.has(job.status));
  const runningId = oldest?.status === 'CANCELLING' ? undefined : oldest?.id;
  const stop = cancel.mutate;
  useEffect(() => {
    if (!runningId) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || event.isComposing) return;
      // Escape already means something to an open dialog, a menu or a field
      // being renamed: closing it must never also stop the work behind it.
      if (document.querySelector('[role="dialog"], [role="listbox"], [role="menu"]')) return;
      if (event.target instanceof HTMLInputElement) return;
      stop(runningId);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [runningId, stop]);

  if (snapshot.isPending) {
    return <p className="text-muted p-6 text-sm">Loading…</p>;
  }
  if (snapshot.error) {
    return <p className="text-danger p-6 text-sm">{(snapshot.error).message}</p>;
  }

  const data = snapshot.data;
  const active = data.jobs.find((job) => !FINISHED.has(job.status));
  const working = activity(sessionId);

  // A message can reach the running work only if something is running to
  // receive it, and only the way the backend holding the Session announced.
  const run = [...data.runs].sort((a, b) => a.createdAt.localeCompare(b.createdAt)).at(-1);
  const backend = backends.data?.find((candidate) => candidate.id === run?.backendInstanceId);
  const features = backend?.features ?? [];
  const receiving = active && !['QUEUED', 'CANCELLING', 'WAITING_BACKEND'].includes(active.status);
  const deliveries = receiving
    ? (['NEXT', 'NOW'] as const).filter((delivery) => features.includes(`JOB_INPUT_${delivery}`))
    : [];

  // Every Job still going somewhere, so the timeline can offer a stop on the
  // message that started it. A Session has one active Job and may have several
  // queued, and a person wants to drop one of those as much as to interrupt the
  // one running.
  const pending: Pending = {};
  for (const job of data.jobs) {
    if (!FINISHED.has(job.status)) pending[job.id] = job.status;
  }

  return (
    // Not a control itself: every control inside keeps its own click, and the
    // field is reachable from the keyboard as it always was.
    <div className="flex min-h-0 flex-1 flex-col" onClick={focusMessage}>
      {/* The frame keeps the window; only what is read is held to a column. */}
      <header className="border-border/70 flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-b px-3 py-3 sm:px-5">
        {/* The title takes the width the header has: an input sized by its own
            content is about twenty characters wide, which is narrower than
            most of the titles it is there to edit. */}
        <div className="min-w-0 flex-1">
          <SessionTitle sessionId={sessionId} title={data.session.title ?? ''} />
          {/* Indented like the title: the title carries padding so its hover
              target is not glued to the text, and the status below has to
              start at the same place or the two read as misaligned.
              The badge rides here rather than beside the title, which has to
              keep the whole width for the moment it becomes a rename field. */}
          <div className="text-muted flex flex-wrap items-center gap-x-2 gap-y-1 pl-1.5 text-xs">
            {active ? (
              <Badge tone={active.status === 'RUNNING' ? 'accent' : 'warn'}>
                {humanise(active.status)}
              </Badge>
            ) : (
              <Badge>Idle</Badge>
            )}
            {/* Which machine does the work: first-class, never a detail. */}
            {backend && (
              <span className="flex items-center gap-1" title="The backend running this session">
                <Server className="size-3" />
                {backend.name}
              </span>
            )}
            <RepositoryStatus sessionId={sessionId} />
            {active && working && (
              // A liveness signal, not history: it says the agent is still
              // there between two things worth remembering. Shown only while a
              // Job is running, because nothing orders these against the
              // timeline and one can arrive after the Job it describes ended.
              <span className="text-accent">{humanise(working)}…</span>
            )}
            <ActionError error={cancel.error} className="text-xs" />
          </div>
        </div>
        <div className="flex items-center gap-1">
        <PinButton session={data.session} />
        {/* Everything about the Session that is not the conversation is a page
            of its own, one step away. */}
        <Button asChild variant="ghost" size="icon" title="Session settings">
          <Link to="/sessions/$sessionId/settings/$tab" params={{ sessionId, tab: 'permissions' }} aria-label="Session settings">
            <Settings2 />
          </Link>
        </Button>
        </div>
      </header>

      <Timeline
        events={data.events}
        pending={pending}
        onStop={(jobId) => cancel.mutate(jobId)}
        earlier={earlier}
      />

      {/* What is waiting for the user sits between the last thing the agent
          wrote and the field they answer in: that is where the eye already is
          while a Job runs. Above the timeline it was a scroll away from both.
          Held to part of the height, so a burst of requests never pushes the
          composer off the screen. */}
      <AttentionPanel
        validations={data.attention.validations ?? []}
        userInputs={data.attention.userInputs ?? []}
        className="mx-auto max-h-[45vh] w-full max-w-reading shrink-0 overflow-y-auto px-3 pt-2 pb-3 sm:px-6"
      />

      <Composer
        key={sessionId}
        sessionId={sessionId}
        active={active}
        deliveries={deliveries}
        onStop={() => active && cancel.mutate(active.id)}
        field={message}
      />
    </div>
  );
}

/**
 * Pins the Session to the sidebar, under what waits, to be one click away
 * from any Project. Read from the pinned list when it is there, so the button
 * and the sidebar change together.
 */
function PinButton({ session }: { session: Session }) {
  const pinned = usePinnedSessions();
  const pin = usePinSession();
  const isPinned = pinned.data ? pinned.data.some((entry) => entry.id === session.id) : Boolean(session.pinnedAt);
  const label = isPinned ? 'Unpin from the sidebar' : 'Pin to the sidebar';
  return (
    <Button
      variant="ghost"
      size="icon"
      title={label}
      aria-label={label}
      aria-pressed={isPinned}
      onClick={() => pin.mutate({ session, pinned: !isPinned })}
      className={cn(isPinned && 'text-accent hover:text-accent')}
    >
      <Pin className={cn(isPinned && 'fill-current')} />
    </Button>
  );
}

/**
 * The Session title, renamed in place.
 *
 * The title a Session starts with is a guess the agent made from the first
 * message, and it is how the Session is found again in a list of fifty. Editing
 * it where it is read costs one click and no dialog.
 */
function SessionTitle({ sessionId, title }: { sessionId: string; title: string }) {
  const rename = useRenameSession(sessionId);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(title);

  // The title changes under the reader: the agent titles a Session that had
  // none, or another client renames it. Only an open editor is left alone.
  useEffect(() => {
    if (!editing) setDraft(title);
  }, [title, editing]);

  const save = () => {
    setEditing(false);
    const next = draft.trim();
    // An empty title is not a rename, and neither is the same one again.
    if (!next || next === title) {
      setDraft(title);
      return;
    }
    rename.mutate(next, { onError: () => setDraft(title) });
  };

  if (editing) {
    return (
      <input
        autoFocus
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={save}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            save();
          }
          if (event.key === 'Escape') {
            setDraft(title);
            setEditing(false);
          }
        }}
        className="border-border w-full rounded-md border bg-transparent px-1.5 py-0.5 text-[0.9375rem] font-semibold outline-none"
      />
    );
  }

  return (
    <>
      <h2 className="truncate text-[0.9375rem] font-semibold">
        <button
          type="button"
          title="Rename this session"
          onClick={() => setEditing(true)}
          className="hover:bg-surface-2 max-w-full truncate rounded-md px-1.5 py-0.5 text-left"
        >
          {title || 'Untitled session'}
        </button>
      </h2>
      <ActionError error={rename.error} className="pl-1.5 text-xs" />
    </>
  );
}

/**
 * Where a message is written.
 *
 * One rounded field holding its own send button, rather than a box and a
 * detached button: the whole thing reads as the single place to type, and it
 * grows with what is being written instead of hiding the top of a long message
 * behind a fixed three rows.
 */
const PLACEHOLDERS: Record<Delivery | 'IDLE', string> = {
  IDLE: 'Send a message…',
  QUEUE: 'Queue a message for when this is done…',
  NEXT: 'Add something it reads at its next step…',
  NOW: 'Interrupt it and say what to do instead…',
};

// Short labels fit a phone's toolbar beside Stop and Send; the full ones say
// more where there is room.
const DELIVERY_LABELS: Record<Delivery, { label: string; short: string; title: string }> = {
  QUEUE: { label: 'Queue', short: 'Queue', title: 'Start a new job once this one is done' },
  NEXT: { label: 'Next step', short: 'Next', title: 'Let the running job read it after what it is doing now' },
  NOW: { label: 'Interrupt', short: 'Now', title: 'Stop what it is doing and redirect it with this message' },
};

/** Where a message sent while the agent works goes. */
function DeliveryChoice({
  value,
  offered,
  onChange,
}: {
  value: Delivery;
  offered: ('NEXT' | 'NOW')[];
  onChange: (delivery: Delivery) => void;
}) {
  const options: Delivery[] = ['QUEUE', ...(['NEXT', 'NOW'] as const).filter((d) => offered.includes(d))];
  return (
    <div role="radiogroup" aria-label="How to send" className="border-border bg-surface-2 text-muted flex rounded-md border p-0.5 text-xs">
      {options.map((option) => (
        <button
          key={option}
          type="button"
          role="radio"
          aria-checked={value === option}
          title={DELIVERY_LABELS[option].title}
          onClick={() => onChange(option)}
          className={cn(
            'min-h-11 rounded px-2.5 whitespace-nowrap transition-colors sm:min-h-7',
            value === option ? 'bg-surface text-text shadow-sm' : 'hover:text-text',
            value === option && option === 'NOW' && 'text-warn-text',
          )}
        >
          <span className="sm:hidden">{DELIVERY_LABELS[option].short}</span>
          <span className="hidden sm:inline">{DELIVERY_LABELS[option].label}</span>
        </button>
      ))}
    </div>
  );
}

function Composer({
  sessionId,
  active,
  deliveries,
  onStop,
  field,
}: {
  sessionId: string;
  active?: { id: string; status: string };
  /** What the running work can take besides a queued message. */
  deliveries: ('NEXT' | 'NOW')[];
  onStop: () => void;
  /** The field, so a click elsewhere on the page can hand it the focus. */
  field: React.RefObject<HTMLTextAreaElement | null>;
}) {
  const send = usePostMessage(sessionId);
  const [message, setMessage] = useDraft(sessionId);

  useLayoutEffect(() => {
    const element = field.current;
    if (!element) return;
    element.style.height = 'auto';
    element.style.height = `${Math.min(element.scrollHeight, 240)}px`;
  }, [message]);

  // How a message reaches the work already running. Queued by default, as
  // decided; the other two only when the backend holding the Session can do
  // them, and only while there is something running to receive them.
  const [delivery, setDelivery] = useState<Delivery>('QUEUE');
  const chosen = deliveries.includes(delivery as 'NEXT' | 'NOW') ? delivery : 'QUEUE';

  const submit = () => {
    const text = message.trim();
    if (!text) return;
    setMessage('');
    send.mutate(
      { message: text, delivery: chosen },
      {
        onError: () => setMessage(text),
        // One message at a time reaches the running work; the next one is
        // queued again unless chosen otherwise.
        onSuccess: () => setDelivery('QUEUE'),
      },
    );
  };

  return (
    <div className="mx-auto w-full max-w-reading px-3 pb-3 sm:px-6 sm:pb-4">
      {/* The stop for whatever is holding the Session, where the hands already
          are. Each message carries its own stop, which is the honest place for
          it — but the message that started the work scrolls away, and the one
          time a person urgently wants to stop something is the one time they
          would have to go looking for it. This stops the oldest Job still
          going, which is the one everything else is queued behind. */}
      <form
        data-tour="composer"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        {/* One panel: the field and its send on one line, then — only while
            something runs — a toolbar with how the message goes, what is
            running and its stop. Idle, it is just a field. */}
        <div className="bg-surface border-border focus-within:border-muted/60 rounded-xl border shadow-xs transition-colors">
          <div className="flex items-end gap-2 py-1.5 pr-1.5 pl-3.5">
            <textarea
              ref={field}
              rows={1}
              value={message}
              onChange={(event) => setMessage(event.target.value)}
              onKeyDown={(event) => {
                // Enter sends, as in every chat. A newline is still reachable
                // with Shift, which is where a person already looks for it. An
                // Enter that closes an input method is composing text, not
                // sending it.
                if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  submit();
                }
              }}
              placeholder={PLACEHOLDERS[active ? chosen : 'IDLE']}
              aria-label="Message"
              className="placeholder:text-muted block max-h-60 min-w-0 flex-1 resize-none bg-transparent py-2.5 text-base outline-none sm:py-1.5 sm:text-sm"
            />
            <Button
              type="submit"
              variant="primary"
              size="icon"
              className="disabled:bg-surface-2 disabled:text-muted size-11 rounded-lg disabled:opacity-100 sm:size-8"
              title="Send (Enter)"
              aria-label="Send"
              disabled={send.isPending || !message.trim()}
            >
              <ArrowUp />
            </Button>
          </div>
          {active && (
            <div className="border-border flex items-center gap-2 border-t px-1.5 py-1.5">
              {active.status !== 'CANCELLING' && deliveries.length > 0 && (
                <DeliveryChoice value={chosen} offered={deliveries} onChange={setDelivery} />
              )}
              <span className="ml-auto" />
              {active.status === 'CANCELLING' ? (
                <span className="text-muted px-2 text-xs whitespace-nowrap">Stopping…</span>
              ) : (
                <>
                  {/* The header already says it on a phone. */}
                  <span className="text-muted hidden text-xs whitespace-nowrap first-letter:uppercase sm:inline">
                    {humanise(active.status)}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="min-h-11 gap-1.5 px-2.5 text-xs whitespace-nowrap sm:min-h-0 [&_svg]:size-3"
                    title="Stop what is running now (Esc)"
                    onClick={onStop}
                  >
                    <Square className="fill-current" />
                    Stop
                  </Button>
                </>
              )}
            </div>
          )}
        </div>
      </form>
      {/* The text is kept in the field when sending fails; this says why. */}
      <ActionError
        error={send.error}
        outcome="Not sent"
        recovery="Your message is still in the field; send it again."
        className="mt-2"
      />
      <p className="text-muted/70 mt-2 hidden text-center text-xs sm:block">
        Enter sends · Shift+Enter for a newline{active && ' · Esc stops'}
      </p>
    </div>
  );
}

