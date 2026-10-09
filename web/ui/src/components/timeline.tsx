import { useVirtualizer } from '@tanstack/react-virtual';
import {
  AlertTriangle,
  ArrowDown,
  Ban,
  CalendarClock,
  CalendarX,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Circle,
  CornerDownLeft,
  FileDiff,
  Flag,
  Layers,
  ShieldCheck,
  ShieldX,
  Square,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Markdown } from '@/components/markdown';
import { ArtifactCard } from '@/components/artifact-preview';
import { Diff } from '@/components/file-change';
import { ToolCallEntry, type ToolCall } from '@/components/tool-call';
import { useFileDiff } from '@/api/queries';
import { parseUnified } from '@/lib/file-change';
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
  'artifact.created',
  'schedule.skipped',
  'validation.resolved',
  'user_input.resolved',
  'job.completed',
  'job.failed',
  'job.cancelled',
]);

/**
 * One line of the timeline: a plain event, a tool call, a change of day, or the
 * folded steps of a finished Job.
 */
type Row =
  | { kind: 'event'; key: number; event: Event; startedAt?: string }
  | { kind: 'tool'; key: number; call: ToolCall; jobId?: string }
  | { kind: 'day'; key: number; label: string }
  | { kind: 'steps'; key: number; at: string; steps: Steps; open: boolean };

/** What a fold holds, counted, so it can be judged without opening it. */
interface Steps {
  tools: number;
  failed: number;
  notes: number;
}

/** How a Job ends, in the log. */
const ENDINGS = new Set(['job.completed', 'job.failed', 'job.cancelled']);

/** The Job a row belongs to, if any. */
function jobOf(row: Row): string | undefined {
  if (row.kind === 'tool') return row.jobId;
  if (row.kind === 'event') return row.event.jobId || undefined;
  return undefined;
}

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
  const byCall = new Map<string, Extract<Row, { kind: 'tool' }>>();
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
        jobId: event.jobId || undefined,
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
          jobId: event.jobId || undefined,
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
 * Folds the work of every finished Job behind one line.
 *
 * While a Job runs, each step is shown as it happens: that is how a person
 * follows what the agent is doing. Once it has ended, what is left to read is
 * what was asked and what came back; the thirty commands in between are a
 * record, kept one click away rather than scrolled through. What stays out:
 * the person's messages, the files the Job changed, the agent's last word,
 * and how the Job ended. Two
 * steps or more fold — one alone is already a single line.
 */
function foldFinished(rows: Row[], opened: ReadonlySet<number>): Row[] {
  const finished = new Set<string>();
  const answer = new Map<string, number>();
  for (const row of rows) {
    if (row.kind !== 'event' || !row.event.jobId) continue;
    if (ENDINGS.has(row.event.type)) finished.add(row.event.jobId);
    if (row.event.type === 'agent.message') answer.set(row.event.jobId, row.key);
  }
  if (finished.size === 0) return rows;

  const folds = (row: Row) => {
    const job = jobOf(row);
    if (!job || !finished.has(job) || row.kind === 'day' || row.kind === 'steps') return false;
    if (row.kind === 'tool') return true;
    const type = row.event.type;
    // What the job changed in the files, and what it published, are part of
    // its result, not of how it got there, so they stay out with the answer.
    return !(
      type === 'user.message' ||
      type === 'workspace.changed' ||
      type === 'artifact.created' ||
      ENDINGS.has(type) ||
      row.key === answer.get(job)
    );
  };

  const out: Row[] = [];
  for (let index = 0; index < rows.length; ) {
    const row = rows[index];
    if (!folds(row)) {
      out.push(row);
      index++;
      continue;
    }
    const job = jobOf(row);
    // A result — a file changed, a file published — does not break the run of
    // steps around it: the steps fold into one line, and the results follow it
    // in their order, so publishing a chart does not leave one stray step
    // between two pictures.
    const result = (candidate: Row) =>
      candidate.kind === 'event' &&
      jobOf(candidate) === job &&
      (candidate.event.type === 'workspace.changed' || candidate.event.type === 'artifact.created');
    const group: Row[] = [];
    const results: Row[] = [];
    let end = index;
    while (end < rows.length && ((folds(rows[end]) && jobOf(rows[end]) === job) || result(rows[end]))) {
      (result(rows[end]) ? results : group).push(rows[end]);
      end++;
    }
    // A result closing the run belongs after whatever follows it, not before.
    while (results.length > 0 && rows[end - 1] === results.at(-1) && !group.includes(rows[end - 1])) {
      end--;
      results.pop();
    }
    index = end;
    if (group.length < 2) {
      out.push(...rows.slice(index - group.length - results.length, index));
      continue;
    }
    const steps: Steps = { tools: 0, failed: 0, notes: 0 };
    for (const step of group) {
      if (step.kind === 'tool') {
        steps.tools++;
        if (step.call.error !== undefined) steps.failed++;
      } else if (step.kind === 'event' && step.event.type === 'agent.message') {
        steps.notes++;
      }
    }
    // Keyed just after its first step, so it sorts where the steps began and
    // keeps its measured size when rows around it change.
    const key = group[0].key + 0.5;
    const first = group[0];
    const at = first.kind === 'tool' ? first.call.at : first.kind === 'event' ? first.event.timestamp : '';
    const open = opened.has(key);
    out.push({ kind: 'steps', key, at, steps, open });
    if (open) out.push(...group);
    out.push(...results);
  }
  return out;
}

/** Whether more of the timeline lies below what is on screen. */
function below(element: HTMLElement): boolean {
  return element.scrollHeight - element.scrollTop - element.clientHeight > 8;
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
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [opened, setOpened] = useState<ReadonlySet<number>>(new Set());
  const all = useMemo(() => rowsOf(events), [events]);
  const rows = useMemo(() => foldFinished(all, opened), [all, opened]);

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 48,
    overscan: 12,
    getItemKey: (index) => rows[index].key,
  });

  // Follow the conversation as it arrives, which is what a chat does.
  //
  // Only the reader stops it. A scroll event alone cannot say who scrolled:
  // the timeline scrolls itself to the bottom, then the rows that came into
  // view are measured, and when they are taller than estimated — a burst of
  // tool calls — the bottom moves away again. Read as "the reader scrolled
  // up", that stopped the following for good, a little short of the end. So
  // following stops only on what a person does — a wheel, a finger, a key,
  // the scrollbar — and resumes when they come back to the bottom.
  //
  // It stops on the gesture, not on the scroll it causes: the virtualiser
  // re-renders on that scroll before any listener here hears of it, and a
  // render that still believed in following put the reader back at the
  // bottom — every turn of the wheel bounced.
  const stick = useRef(true);
  const lastIntent = useRef(0);
  const [away, setAway] = useState(false);
  // Reaching the top asks for what came before. Read through a ref so the
  // listener is attached once and still sees the current state.
  const reachTop = useRef<() => void>(() => {});
  reachTop.current = () => {
    if (earlier?.more && !earlier.loading) earlier.load();
  };
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element) return;
    const intent = (up: boolean) => {
      lastIntent.current = Date.now();
      if (up) {
        stick.current = false;
      }
    };
    const onWheel = (event: WheelEvent) => intent(event.deltaY < 0);
    let touchY = 0;
    const onTouchStart = (event: TouchEvent) => {
      touchY = event.touches[0]?.clientY ?? 0;
    };
    // A finger moving down the screen pulls the page up, to older lines.
    const onTouchMove = (event: TouchEvent) => {
      const y = event.touches[0]?.clientY ?? touchY;
      intent(y > touchY);
      touchY = y;
    };
    // The scrollbar is the element itself being pressed, not a row in it.
    const onPress = (event: PointerEvent) => {
      if (event.target === element) intent(true);
    };
    const onKey = (event: KeyboardEvent) => {
      if (['ArrowUp', 'PageUp', 'Home'].includes(event.key)) intent(true);
      else if (['ArrowDown', 'PageDown', 'End', ' '].includes(event.key)) intent(false);
    };
    const onScroll = () => {
      const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
      if (distance <= 8) {
        stick.current = true;
      } else if (Date.now() - lastIntent.current < 600) {
        stick.current = false;
      }
      setAway(!stick.current && below(element));
      if (element.scrollTop < 400) reachTop.current();
    };
    element.addEventListener('wheel', onWheel, { passive: true });
    element.addEventListener('touchstart', onTouchStart, { passive: true });
    element.addEventListener('touchmove', onTouchMove, { passive: true });
    element.addEventListener('pointerdown', onPress);
    element.addEventListener('keydown', onKey);
    element.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      element.removeEventListener('wheel', onWheel);
      element.removeEventListener('touchstart', onTouchStart);
      element.removeEventListener('touchmove', onTouchMove);
      element.removeEventListener('pointerdown', onPress);
      element.removeEventListener('keydown', onKey);
      element.removeEventListener('scroll', onScroll);
    };
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
      if (stick.current) element.scrollTop = element.scrollHeight;
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
    stick.current = true;
    // Placed once per message; the reader is free to scroll away after.
  }, [anchor]);

  // Following means the true bottom of the scroller, its padding included,
  // after every render: a row is measured after it is drawn, and one that
  // grows — a burst of logs, a stop control appearing, a tool call unfolded —
  // re-renders with a new total, which lands here again until it settles.
  // Pointing at the last row instead stopped short by the padding, and used
  // estimated offsets. While the spacer is there, the answer still fits under
  // the message just sent, and following would pull the message back down.
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element || !stick.current || spacer > 0) return;
    if (element.scrollHeight - element.scrollTop - element.clientHeight > 1) {
      element.scrollTop = element.scrollHeight;
    }
  });

  // The way back down is offered only when there is a way down: a reader who
  // turned the wheel or opened a step on a conversation that fits on screen
  // has nothing below to return to. And with nothing to scroll there is
  // nothing to have scrolled away from, so the next line is followed again.
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element) return;
    if (element.scrollHeight <= element.clientHeight + 8) stick.current = true;
    setAway(!stick.current && below(element));
  });

  // A way back to the latest line, for a reader who scrolled up to read and
  // now wants to see what the agent is doing.
  const toLatest = () => {
    const element = parentRef.current;
    if (!element) return;
    stick.current = true;
    setAway(false);
    element.scrollTop = element.scrollHeight;
  };

  // Opening something is reading it: following the bottom would pull what was
  // just opened out of sight as it grows.
  const holdStill = () => {
    stick.current = false;
  };
  const toggle = (id: string) => {
    holdStill();
    setExpanded((current) => {
      const next = new Set(current);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  };
  const toggleSteps = (key: number) => {
    holdStill();
    setOpened((current) => {
      const next = new Set(current);
      if (!next.delete(key)) next.add(key);
      return next;
    });
  };

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
                {row.kind === 'day' ? (
                  <DaySeparator label={row.label} />
                ) : (
                  // The log's spine: every row's time in one column, so the
                  // session reads down a timeline the way an operations log
                  // does, whatever kind of row it is. Agent prose keeps the
                  // column empty and runs beside it.
                  <div className="grid grid-cols-[2.75rem_minmax(0,1fr)] gap-x-2 sm:grid-cols-[3.25rem_minmax(0,1fr)]">
                    <Gutter row={row} />
                    {row.kind === 'steps' ? (
                      <StepsEntry row={row} onToggle={() => toggleSteps(row.key)} />
                    ) : row.kind === 'tool' ? (
                      <ToolCallEntry
                        call={row.call}
                        expanded={expanded.has(row.call.id)}
                        onToggle={() => toggle(row.call.id)}
                      />
                    ) : (
                      <Entry
                        event={row.event}
                        startedAt={row.startedAt}
                        pending={pending}
                        onStop={onStop}
                      />
                    )}
                  </div>
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
      // A tinted row headed by who spoke, rather than a coloured stripe down
      // its side: the log keeps one left margin, and the mark is a word.
      <div className="bg-surface-2 border-border space-y-1 rounded-(--radius-card) border px-3 py-2">
        <p className="text-muted flex flex-wrap items-center gap-x-2 text-xs">
          <span className="text-text font-medium">
            {user.scheduleId ? (
              // Nobody typed it: a schedule did, and the reader should not
              // wonder when they wrote this.
              <span className="flex items-center gap-1">
                <CalendarClock className="size-3" /> Schedule
              </span>
            ) : (
              'You'
            )}
          </span>
          {/* Sent while the agent worked: it changed the course of a Job
              rather than starting one, and the reader of the log should know. */}
          {user.delivery && (
            <span className={user.delivery === 'NOW' ? 'text-warn-text' : undefined}>
              {user.delivery === 'NOW' ? 'interrupted the job' : 'added to the running job'}
            </span>
          )}
        </p>
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
    return <WorkspaceChange change={changed} sessionId={event.sessionId} sequence={event.sequence} />;
  }

  // Something the agent made for the person to look at, shown where it said
  // so.
  const published = payloadOf(event, 'artifact.created');
  if (published) {
    return (
      <ArtifactCard
        artifactId={published.artifactId}
        filename={published.filename}
        mimeType={published.mimeType}
        size={published.size}
        title={published.title}
      />
    );
  }

  // Everything else is the log running between the messages: a mark, the time
  // it happened, and one line. The mark is what makes a long Session scannable
  // without reading it, and the time is what makes it a record.
  const { Icon, tone, text } = describe(event);
  return (
    <div className="text-muted flex items-start gap-x-2 text-[0.8125rem] leading-5">
      <Icon className={cn('mt-0.5 size-3.5 shrink-0', tone)} />
      <span className="min-w-0 break-words">
        {text}
        <JobCost event={event} startedAt={startedAt} />
      </span>
    </div>
  );
}

/** The time of a row, in the log's gutter, aligned with its first line. */
function Gutter({ row }: { row: Exclude<Row, { kind: 'day' }> }) {
  if (row.kind === 'event' && row.event.type === 'agent.message') return <span />;
  const at = row.kind === 'tool' ? row.call.at : row.kind === 'steps' ? row.at : row.event.timestamp;
  const user = row.kind === 'event' && row.event.type === 'user.message';
  return (
    <time
      dateTime={at}
      className={cn(
        'text-muted text-right font-mono text-[0.6875rem] leading-5',
        row.kind === 'tool' || row.kind === 'steps' ? 'pt-1' : user ? 'pt-2' : 'pt-px',
      )}
    >
      {clock(at)}
    </time>
  );
}

/**
 * The folded work of a finished Job: one line saying how much there was and
 * whether any of it went wrong, which opens onto the steps themselves.
 */
function StepsEntry({
  row,
  onToggle,
}: {
  row: Extract<Row, { kind: 'steps' }>;
  onToggle: () => void;
}) {
  const { tools, failed, notes } = row.steps;
  const parts = [
    tools > 0 && `${tools} ${tools === 1 ? 'step' : 'steps'}`,
    notes > 0 && `${notes} ${notes === 1 ? 'message' : 'messages'}`,
  ].filter(Boolean);
  return (
    <button
      data-tour="steps"
      type="button"
      onClick={onToggle}
      aria-expanded={row.open}
      className="hover:bg-surface-2 text-muted hover:text-text flex w-full items-center gap-2 rounded-(--radius-card) px-2 py-1 text-left text-xs transition-colors"
    >
      {row.open ? (
        <ChevronDown className="size-3.5 shrink-0" />
      ) : (
        <ChevronRight className="size-3.5 shrink-0" />
      )}
      <Layers className="size-3.5 shrink-0" />
      <span className="figures">
        {row.open ? 'Hide ' : ''}
        {parts.join(' · ') || 'Steps'}
      </span>
      {failed > 0 && (
        <span className="text-danger figures flex items-center gap-1">
          <AlertTriangle className="size-3" />
          {failed} failed
        </span>
      )}
    </button>
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
        className="min-h-11 gap-1.5 px-2 text-xs sm:h-6 sm:min-h-0 sm:px-1.5 [&_svg]:size-3"
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
    <span className="text-muted figures ml-2.5 inline-flex flex-wrap items-center gap-x-2.5 font-mono text-xs">
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
  sessionId,
  sequence,
}: {
  change: NonNullable<ReturnType<typeof payloadOf<'workspace.changed'>>>;
  sessionId?: string;
  sequence: number;
}) {
  const files = change.files ?? [];
  const [all, setAll] = useState(false);
  const shown = all ? files : files.slice(0, 12);
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  // The backend can only be asked when it recorded the two trees.
  const diffable = Boolean(sessionId && change.baseTree && change.headTree);
  const toggle = (path: string) =>
    setOpen((current) => {
      const next = new Set(current);
      if (!next.delete(path)) next.add(path);
      return next;
    });

  return (
    <div data-tour="workspace" className="bg-surface border-border rounded-(--radius-card) border px-3 py-2 text-sm">
      <div className="text-muted flex items-center gap-2">
        <FileDiff className="size-3.5" />
        <span>
          {files.length} file{files.length === 1 ? '' : 's'} changed
        </span>
        <span className="text-ok">+{change.additions}</span>
        <span className="text-danger">−{change.deletions}</span>
      </div>
      <ul className="mt-1.5 space-y-0.5 font-mono text-xs">
        {shown.map((file) => {
          const label = (
            <span
              className={cn(
                'break-all',
                file.state === 'ADDED' && 'text-ok',
                file.state === 'DELETED' && 'text-danger',
                file.state === 'MODIFIED' && 'text-muted',
                file.state === 'RENAMED' && 'text-warn-text',
              )}
            >
              {MARK[file.state]} {file.path}
            </span>
          );
          return (
            <li key={file.path}>
              {diffable ? (
                <button
                  type="button"
                  onClick={() => toggle(file.path)}
                  aria-expanded={open.has(file.path)}
                  className="hover:bg-surface-2 flex w-full items-start gap-1 rounded text-left"
                >
                  {open.has(file.path) ? (
                    <ChevronDown className="text-muted mt-0.5 size-3 shrink-0" />
                  ) : (
                    <ChevronRight className="text-muted mt-0.5 size-3 shrink-0" />
                  )}
                  {label}
                </button>
              ) : (
                label
              )}
              {diffable && open.has(file.path) && sessionId && (
                <FileDiffPanel sessionId={sessionId} sequence={sequence} path={file.path} />
              )}
            </li>
          );
        })}
        {files.length > shown.length && (
          <li>
            <button type="button" className="text-muted hover:text-text" onClick={() => setAll(true)}>
              … and {files.length - shown.length} more
            </button>
          </li>
        )}
      </ul>
    </div>
  );
}

/**
 * The diff of one file of a change, fetched from the backend when unfolded.
 *
 * Said plainly when it cannot be shown: the backend is away, or git has
 * collected the state it was taken from.
 */
function FileDiffPanel({ sessionId, sequence, path }: { sessionId: string; sequence: number; path: string }) {
  const diff = useFileDiff(sessionId, sequence, path, true);

  if (diff.isPending) {
    return <p className="text-muted py-1 pl-4">Asking the backend for the diff…</p>;
  }
  if (diff.error) {
    return <p className="text-warn-text py-1 pl-4">{diff.error.message}</p>;
  }
  if (diff.data.binary) {
    return <p className="text-muted py-1 pl-4">A binary file: there are no lines to show.</p>;
  }
  const lines = parseUnified(diff.data.diff);
  if (lines.length === 0) {
    return <p className="text-muted py-1 pl-4">No difference in content.</p>;
  }
  return (
    <div className="border-border my-1 ml-4 max-h-96 overflow-auto rounded-md border font-sans">
      <Diff lines={lines} />
      {diff.data.truncated && (
        <p className="text-muted border-border border-t px-2 py-1">Cut short: the diff is too long to show whole.</p>
      )}
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
    case 'schedule.skipped':
      return {
        Icon: CalendarX,
        tone: 'text-warn-text',
        text: `Scheduled message not sent: ${payloadOf(event, 'schedule.skipped')?.reason ?? 'skipped'}.`,
      };
    case 'job.cancelled':
      return { Icon: Ban, text: 'Cancelled.' };
    case 'validation.resolved': {
      // What was decided, not only that something was: the log is read back
      // later, and "Permission granted." alone cannot be trusted then.
      const resolved = payloadOf(event, 'validation.resolved');
      // "Bash: go test" — the tool in words, the command as a command.
      const [tool, ...rest] = (resolved?.title ?? '').split(': ');
      const what = resolved?.title ? (
        <>
          : {rest.length ? `${tool} ` : ''}
          <code className="text-text font-mono text-xs">{rest.length ? rest.join(': ') : tool}</code>
        </>
      ) : (
        '.'
      );
      return resolved?.approved
        ? { Icon: ShieldCheck, tone: 'text-ok', text: <>Allowed{what}</> }
        : { Icon: ShieldX, tone: 'text-danger', text: <>Denied{what}</> };
    }
    case 'user_input.resolved':
      return {
        Icon: CornerDownLeft,
        text: `Answered: ${payloadOf(event, 'user_input.resolved')?.value ?? ''}`,
      };
    default:
      return { Icon: Circle, text: <Badge>{event.type}</Badge> };
  }
}
