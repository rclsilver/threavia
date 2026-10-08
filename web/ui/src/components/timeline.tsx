import { useVirtualizer } from '@tanstack/react-virtual';
import {
  AlertTriangle,
  ArrowDown,
  Ban,
  CheckCircle2,
  Circle,
  CornerDownLeft,
  FileDiff,
  Flag,
  ShieldCheck,
  ShieldX,
  Square,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Markdown } from '@/components/markdown';
import { ToolCallEntry, type ToolCall } from '@/components/tool-call';
import { payloadOf, type Event, type JobStatus } from '@/api/types';
import { clock, cn, cost, duration, humanise, tokens } from '@/lib/utils';

/**
 * The Jobs that have not finished, by id.
 *
 * A message whose Job is in here is still going somewhere, so it is the one
 * place a stop control belongs: next to the sentence it would stop.
 */
export type Pending = Record<string, JobStatus>;

/** The events worth a line. The rest is machinery a user never asked to see. */
const RENDERED = new Set([
  'session.created',
  'user.message',
  'agent.message',
  'workspace.changed',
  'validation.resolved',
  'user_input.resolved',
  'job.completed',
  'job.failed',
  'job.cancelled',
]);

/** One line of the timeline: a plain event, a tool call, or a change of day. */
type Row =
  | { kind: 'event'; key: number; event: Event; startedAt?: string }
  | { kind: 'tool'; key: number; call: ToolCall }
  | { kind: 'day'; key: number; label: string };

/**
 * The day an event belongs to, named the way a person would.
 *
 * A Session is long-lived: without this, work from last week and work from this
 * morning are one undivided scroll.
 */
function dayOf(timestamp: string): { key: string; label: string } {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return { key: '', label: '' };

  const key = date.toDateString();
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);

  if (key === today.toDateString()) return { key, label: 'Today' };
  if (key === yesterday.toDateString()) return { key, label: 'Yesterday' };
  return {
    key,
    label: date.toLocaleDateString(undefined, {
      day: 'numeric',
      month: 'long',
      year: date.getFullYear() === today.getFullYear() ? undefined : 'numeric',
    }),
  };
}

/**
 * Folds the event stream into what a reader sees.
 *
 * A tool call arrives as a start and, later, a result. They become one row: a
 * reader thinks "the agent ran this and got that", not "here is a start event
 * and, eleven lines down, its ending". Pairing is by the call id the provider
 * assigned, so two concurrent calls of the same tool stay apart.
 */
function rowsOf(events: Event[]): Row[] {
  const rows: Row[] = [];
  const byCall = new Map<string, { kind: 'tool'; key: number; call: ToolCall }>();
  // When each Job started, so its ending can say how long it took. The timeline
  // already carries both timestamps; asking the server again would be slower
  // and no more true.
  const startedAt = new Map<string, string>();
  let day = '';

  // A separator goes in front of the first row of each day, including the
  // first of all: a reader opening a Session should see when it happened
  // without reading a timestamp off every line.
  const openDay = (event: Event) => {
    const { key, label } = dayOf(event.timestamp);
    if (!key || key === day) return;
    day = key;
    rows.push({ kind: 'day', key: -event.sequence, label });
  };

  for (const event of events) {
    if (event.type === 'job.started' && event.jobId) {
      startedAt.set(event.jobId, event.timestamp);
    }
    const started = payloadOf(event, 'tool.started');
    if (started) {
      openDay(event);
      const row = {
        kind: 'tool' as const,
        key: event.sequence,
        call: {
          id: started.toolCallId || `${event.sequence}`,
          name: started.name,
          at: event.timestamp,
          input: started.input ?? {},
          done: false,
        },
      };
      rows.push(row);
      byCall.set(row.call.id, row);
      continue;
    }

    const completed = payloadOf(event, 'tool.completed');
    if (completed) {
      const row = byCall.get(completed.toolCallId);
      if (row) {
        row.call = { ...row.call, output: completed.output?.output ?? '', done: true };
      }
      continue;
    }

    const failed = payloadOf(event, 'tool.failed');
    if (failed) {
      const row = byCall.get(failed.toolCallId);
      if (row) {
        row.call = { ...row.call, error: failed.error ?? '', done: true };
      } else {
        // A failure with no start, which happens when a Session is opened on a
        // window of history that begins mid-call. Showing it alone beats
        // dropping it.
        openDay(event);
        rows.push({
          kind: 'tool',
          key: event.sequence,
          call: {
            id: failed.toolCallId || `${event.sequence}`,
            name: failed.name,
            at: event.timestamp,
            input: {},
            error: failed.error ?? '',
            done: true,
          },
        });
      }
      continue;
    }

    if (RENDERED.has(event.type)) {
      openDay(event);
      rows.push({
        kind: 'event',
        key: event.sequence,
        event,
        startedAt: event.jobId ? startedAt.get(event.jobId) : undefined,
      });
    }
  }
  return rows;
}

/**
 * The Session timeline.
 *
 * Virtualised because a Session is append-only and unbounded: a long one holds
 * thousands of events, and rendering them all is what turns a conversation into
 * a frozen tab. Only what fits on screen is in the DOM.
 */
export function Timeline({
  events,
  pending,
  onStop,
  earlier,
}: {
  events: Event[];
  pending: Pending;
  onStop: (jobId: string) => void;
  /** History before the window the snapshot opened with, read on demand. */
  earlier?: { more: boolean; loading: boolean; load: () => void };
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const rows = useMemo(() => rowsOf(events), [events]);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 48,
    overscan: 12,
    getItemKey: (index) => rows[index].key,
  });

  // Follow the conversation as it arrives, which is what a chat does.
  const atBottom = useRef(true);
  // Reaching the top asks for what came before. Read through a ref so the
  // listener is attached once and still sees the current state.
  const reachTop = useRef<() => void>(() => {});
  reachTop.current = () => {
    if (earlier?.more && !earlier.loading) earlier.load();
  };
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element) return;
    const onScroll = () => {
      atBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 80;
      if (element.scrollTop < 400) reachTop.current();
    };
    element.addEventListener('scroll', onScroll, { passive: true });
    return () => element.removeEventListener('scroll', onScroll);
  }, []);

  // A page of history lands above what is being read. Without this the reader
  // would be thrown to the top of it; with it, the line they were on stays
  // where it was and the older rows grow above, out of sight until scrolled to.
  // Rows already measured keep their size by key, so the difference in total
  // height is exactly what was added. Recorded on every render rather than on
  // a change of rows, because a row measured late changes the total too, and
  // that growth was not added above anyone.
  const firstKey = rows[0]?.key;
  const previous = useRef<{ firstKey?: number; total: number }>({ total: 0 });
  useLayoutEffect(() => {
    const element = parentRef.current;
    const total = virtualizer.getTotalSize();
    const before = previous.current;
    previous.current = { firstKey, total };
    if (!element || before.firstKey === undefined || before.firstKey === firstKey) return;
    if (!rows.some((row) => row.key === before.firstKey)) return;
    element.scrollTop += total - before.total;
  });

  // The window the timeline is read through changes size under it: a card
  // waiting for an answer opens above the composer, the composer grows with a
  // long message, a phone turns. The browser keeps the scroll offset, so the
  // last lines slid out of view exactly when a request came in — the moment a
  // person most needs to read them. A reader who was at the bottom stays there.
  const [viewport, setViewport] = useState(0);
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element) return;
    const observer = new ResizeObserver(() => {
      setViewport(element.clientHeight);
      if (atBottom.current) element.scrollTop = element.scrollHeight;
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  // A message just sent goes to the top of the window, with the answer
  // unrolling under it, rather than at the bottom edge where it is read past.
  // The room below is made by a spacer that shrinks as the answer fills the
  // screen; once it is gone, the timeline follows the latest line as before.
  const lastSent = useMemo(() => {
    for (let index = rows.length - 1; index >= 0; index--) {
      const row = rows[index];
      if (row.kind === 'event' && row.event.type === 'user.message') return row.key;
    }
    return undefined;
  }, [rows]);
  const seenSent = useRef(lastSent);
  const [anchor, setAnchor] = useState<number>();
  useEffect(() => {
    if (lastSent === undefined || lastSent === seenSent.current) return;
    // Only a message that arrives while reading, not the last one of a
    // Session being opened or a page of history being loaded above.
    const fresh = seenSent.current === undefined || lastSent > seenSent.current;
    seenSent.current = lastSent;
    if (fresh) setAnchor(lastSent);
  }, [lastSent]);

  const anchorIndex = anchor === undefined ? -1 : rows.findIndex((row) => row.key === anchor);
  const anchorStart =
    anchorIndex >= 0 ? (virtualizer.measurementsCache[anchorIndex]?.start ?? 0) : 0;
  const content = virtualizer.getTotalSize();
  // The bottom padding of the scroller counts as content already shown.
  const spacer = anchorIndex >= 0 ? Math.max(0, viewport - 24 - (content - anchorStart) - 8) : 0;
  const height = content + spacer;

  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element || anchorIndex < 0) return;
    element.scrollTop = Math.max(0, anchorStart - 8);
    atBottom.current = true;
    // Placed once per message; the reader is free to scroll away after.
  }, [anchor]);

  // Following the last row means following its height too: a row is measured
  // after it is rendered, and one that grows afterwards — a message that gains
  // a stop control, a tool call being unfolded — would otherwise end up half
  // hidden under the composer. While the spacer is there, the answer still fits
  // under the message, and following would only pull the message back down.
  useEffect(() => {
    if (atBottom.current && rows.length > 0 && spacer === 0) {
      virtualizer.scrollToIndex(rows.length - 1, { align: 'end' });
    }
  }, [rows.length, height, spacer, virtualizer]);

  // A way back to the latest line, for a reader who scrolled up to read and
  // now wants to see what the agent is doing.
  const [away, setAway] = useState(false);
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element) return;
    const onScroll = () => setAway(!atBottom.current);
    element.addEventListener('scroll', onScroll, { passive: true });
    return () => element.removeEventListener('scroll', onScroll);
  }, []);
  const toLatest = () => {
    const element = parentRef.current;
    if (!element) return;
    atBottom.current = true;
    setAway(false);
    element.scrollTo({ top: element.scrollHeight, behavior: 'smooth' });
  };

  const toggle = (id: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  // The scroller is rendered even when empty: the listeners above attach to it
  // once, and a Session that starts empty would otherwise never get them.
  return (
    // The scrollbar belongs to the window, the prose to a column inside it: the
    // conversation stays readable on a wide screen without the page looking
    // cropped.
    <div className="relative flex min-h-0 flex-1 flex-col">
      {away && (
        <Button
          variant="secondary"
          size="icon"
          title="Go to the latest"
          onClick={toLatest}
          className="absolute bottom-3 left-1/2 z-10 size-8 -translate-x-1/2 rounded-full shadow-md"
        >
          <ArrowDown />
        </Button>
      )}
      <div ref={parentRef} className="min-h-0 flex-1 overflow-y-auto pb-6">
        {rows.length === 0 && <p className="text-muted p-4 text-sm">Nothing yet.</p>}
        {/* No height of its own, so the rows below keep the offsets the
            virtualiser computed for them. */}
        {earlier?.loading && (
          <div className="sticky top-0 z-10 h-0">
            <p className="text-muted bg-surface border-border mx-auto mt-2 w-fit rounded-full border px-3 py-1 text-xs">
              Loading earlier messages…
            </p>
          </div>
        )}
        <div className="relative mx-auto w-full max-w-reading" style={{ height }}>
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index];
            return (
              <div
                key={item.key}
                ref={virtualizer.measureElement}
                data-index={item.index}
                className="absolute left-0 top-0 w-full px-3 py-1.5 sm:px-6"
                style={{ transform: `translateY(${item.start}px)` }}
              >
                {row.kind === 'tool' ? (
                  <ToolCallEntry
                    call={row.call}
                    expanded={expanded.has(row.call.id)}
                    onToggle={() => toggle(row.call.id)}
                  />
                ) : row.kind === 'day' ? (
                  <DaySeparator label={row.label} />
                ) : (
                  <Entry
                    event={row.event}
                    startedAt={row.startedAt}
                    pending={pending}
                    onStop={onStop}
                  />
                )}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function Entry({
  event,
  startedAt,
  pending,
  onStop,
}: {
  event: Event;
  startedAt?: string;
  pending: Pending;
  onStop: (jobId: string) => void;
}) {
  // What the person said, marked by a rule rather than sent to the other side
  // of the page. A column that reads top to bottom keeps every line starting at
  // the same margin — which is what makes a wide window readable at all — and
  // one of the two speakers has to carry a mark: unmarked is the agent.
  const user = payloadOf(event, 'user.message');
  if (user) {
    const jobId = event.jobId;
    const status = jobId ? pending[jobId] : undefined;
    return (
      <div className="border-accent bg-surface-2/60 space-y-1 rounded-r-md border-l-2 py-2 pr-3 pl-3">
        <p className="text-sm whitespace-pre-wrap">{user.text}</p>
        {jobId && status && <Stop status={status} onStop={() => onStop(jobId)} />}
      </div>
    );
  }

  // The agent speaks on the page itself. A card around every answer frames the
  // one thing the reader came for, and stacks a border between each paragraph
  // of a long conversation.
  const agent = payloadOf(event, 'agent.message');
  if (agent) {
    return <Markdown>{agent.text}</Markdown>;
  }

  const changed = payloadOf(event, 'workspace.changed');
  if (changed) {
    return <WorkspaceChange change={changed} />;
  }

  // Everything else is the log running between the messages: a mark, the time
  // it happened, and one line. The mark is what makes a long Session scannable
  // without reading it, and the time is what makes it a record.
  const { Icon, tone, text } = describe(event);
  return (
    <div className="text-muted flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.8125rem]">
      <Icon className={cn('size-3.5 shrink-0', tone)} />
      <time dateTime={event.timestamp} className="shrink-0 font-mono text-[0.6875rem] opacity-60">
        {clock(event.timestamp)}
      </time>
      <span className="break-all">{text}</span>
      <JobCost event={event} startedAt={startedAt} />
    </div>
  );
}

/** Where one day of a Session ends and the next begins. */
function DaySeparator({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 py-3">
      <span className="bg-border h-px flex-1" />
      <span className="text-muted text-xs">{label}</span>
      <span className="bg-border h-px flex-1" />
    </div>
  );
}

/**
 * The control that stops one message.
 *
 * It belongs under the sentence it would stop: the only question asked here is
 * "stop that one", and a button in the header cannot answer it, since it never
 * says which message is running nor that another is queued behind it. The
 * status beside it is what makes the button honest, because stopping a message
 * that has not started yet is not the same act as interrupting one.
 */
function Stop({ status, onStop }: { status: JobStatus; onStop: () => void }) {
  // Core moves a Job to CANCELLED only once the backend confirms the stop, so
  // the wait is real and worth showing rather than hiding the control.
  if (status === 'CANCELLING') {
    return <span className="text-muted text-xs">Stopping…</span>;
  }

  return (
    <div className="text-muted flex items-center gap-1.5 text-xs">
      <span className="first-letter:uppercase">{humanise(status)}</span>
      <Button
        variant="ghost"
        size="sm"
        className="h-6 gap-1.5 px-1.5 text-xs [&_svg]:size-3"
        title={
          status === 'QUEUED'
            ? 'Drop this message before it runs'
            : 'Stop what this message started'
        }
        onClick={onStop}
      >
        <Square className="fill-current" />
        Stop
      </Button>
    </div>
  );
}

/**
 * What a Job took and what it consumed.
 *
 * The duration comes from the timeline itself, which already holds both ends.
 * The usage comes from the backend, and is absent when it reported none: a Job
 * whose accounting is unknown must not read as a Job that cost nothing, so this
 * shows nothing rather than zeroes.
 */
function JobCost({ event, startedAt }: { event: Event; startedAt?: string }) {
  const ended = payloadOf(event, 'job.completed') ?? payloadOf(event, 'job.failed');
  if (!ended) return null;

  const elapsed = startedAt ? Date.parse(event.timestamp) - Date.parse(startedAt) : NaN;
  const usage = ended.usage;
  const total = usage ? usage.inputTokens + usage.outputTokens : 0;

  return (
    <span className="text-muted/80 flex flex-wrap items-center gap-x-2.5 font-mono text-xs">
      {Number.isFinite(elapsed) && elapsed >= 0 && <span>{duration(elapsed)}</span>}
      {usage && (
        <span title={`${usage.inputTokens} in, ${usage.outputTokens} out`}>
          {tokens(total)} tokens
        </span>
      )}
      {usage && usage.cacheReadTokens > 0 && (
        <span title="Served from the prompt cache">{tokens(usage.cacheReadTokens)} cached</span>
      )}
      {usage?.costUsd ? <span>{cost(usage.costUsd)}</span> : null}
    </span>
  );
}

function WorkspaceChange({
  change,
}: {
  change: NonNullable<ReturnType<typeof payloadOf<'workspace.changed'>>>;
}) {
  const files = change.files ?? [];
  const shown = files.slice(0, 12);

  return (
    <div className="bg-surface border-border rounded-[--radius-card] border px-3 py-2 text-sm">
      <div className="text-muted flex items-center gap-2">
        <FileDiff className="size-3.5" />
        <span>
          {files.length} file{files.length === 1 ? '' : 's'} changed
        </span>
        <span className="text-ok">+{change.additions}</span>
        <span className="text-danger">−{change.deletions}</span>
      </div>
      <ul className="mt-1.5 space-y-0.5 font-mono text-xs">
        {shown.map((file) => (
          <li
            key={file.path}
            className={cn(
              'break-all',
              file.state === 'ADDED' && 'text-ok',
              file.state === 'DELETED' && 'text-danger',
              file.state === 'MODIFIED' && 'text-muted',
              file.state === 'RENAMED' && 'text-warn',
            )}
          >
            {MARK[file.state]} {file.path}
          </li>
        ))}
        {files.length > shown.length && (
          <li className="text-muted">… and {files.length - shown.length} more</li>
        )}
      </ul>
    </div>
  );
}

const MARK: Record<string, string> = {
  ADDED: '+',
  MODIFIED: '~',
  DELETED: '−',
  RENAMED: '→',
};

/** The mark and the line for an event the timeline does not lay out specially. */
function describe(event: Event): {
  Icon: LucideIcon;
  tone?: string;
  text: React.ReactNode;
} {
  switch (event.type) {
    case 'session.created':
      return { Icon: Flag, text: 'Session created.' };
    // The provider result repeats the last agent message, so only the fact that
    // the turn ended is worth showing.
    case 'job.completed':
      return { Icon: CheckCircle2, tone: 'text-ok', text: 'Done.' };
    case 'job.failed':
      return {
        Icon: AlertTriangle,
        tone: 'text-danger',
        text: (
          <span className="text-danger">
            Failed: {payloadOf(event, 'job.failed')?.error ?? 'unknown error'}
          </span>
        ),
      };
    case 'job.cancelled':
      return { Icon: Ban, text: 'Cancelled.' };
    case 'validation.resolved':
      return payloadOf(event, 'validation.resolved')?.approved
        ? { Icon: ShieldCheck, tone: 'text-ok', text: 'Permission granted.' }
        : { Icon: ShieldX, tone: 'text-danger', text: 'Permission denied.' };
    case 'user_input.resolved':
      return {
        Icon: CornerDownLeft,
        text: `Answered: ${payloadOf(event, 'user_input.resolved')?.value ?? ''}`,
      };
    default:
      return { Icon: Circle, text: <Badge>{event.type}</Badge> };
  }
}
