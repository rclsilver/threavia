import { useVirtualizer } from '@tanstack/react-virtual';
import { AlertTriangle, CheckCircle2, FileDiff } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Markdown } from '@/components/markdown';
import { ToolCallEntry, type ToolCall } from '@/components/tool-call';
import { payloadOf, type Event } from '@/api/types';
import { cn, cost, duration, tokens } from '@/lib/utils';

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

/** One line of the timeline: a plain event, or a tool call and its result. */
type Row =
  | { kind: 'event'; key: number; event: Event; startedAt?: string }
  | { kind: 'tool'; key: number; call: ToolCall };

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

  for (const event of events) {
    if (event.type === 'job.started' && event.jobId) {
      startedAt.set(event.jobId, event.timestamp);
    }
    const started = payloadOf(event, 'tool.started');
    if (started) {
      const row = {
        kind: 'tool' as const,
        key: event.sequence,
        call: {
          id: started.toolCallId || `${event.sequence}`,
          name: started.name,
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
        rows.push({
          kind: 'tool',
          key: event.sequence,
          call: {
            id: failed.toolCallId || `${event.sequence}`,
            name: failed.name,
            input: {},
            error: failed.error ?? '',
            done: true,
          },
        });
      }
      continue;
    }

    if (RENDERED.has(event.type)) {
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
export function Timeline({ events }: { events: Event[] }) {
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
  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element) return;
    const onScroll = () => {
      atBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 80;
    };
    element.addEventListener('scroll', onScroll, { passive: true });
    return () => element.removeEventListener('scroll', onScroll);
  }, []);

  useEffect(() => {
    if (atBottom.current && rows.length > 0) {
      virtualizer.scrollToIndex(rows.length - 1, { align: 'end' });
    }
  }, [rows.length, virtualizer]);

  const toggle = (id: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  if (rows.length === 0) {
    return <div className="text-muted flex-1 p-4 text-sm">Nothing yet.</div>;
  }

  return (
    <div ref={parentRef} className="min-h-0 flex-1 overflow-y-auto px-1">
      <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((item) => {
          const row = rows[item.index];
          return (
            <div
              key={item.key}
              ref={virtualizer.measureElement}
              data-index={item.index}
              className="absolute top-0 left-0 w-full py-1"
              style={{ transform: `translateY(${item.start}px)` }}
            >
              {row.kind === 'tool' ? (
                <ToolCallEntry
                  call={row.call}
                  expanded={expanded.has(row.call.id)}
                  onToggle={() => toggle(row.call.id)}
                />
              ) : (
                <Entry event={row.event} startedAt={row.startedAt} />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Entry({ event, startedAt }: { event: Event; startedAt?: string }) {
  const user = payloadOf(event, 'user.message');
  if (user) {
    return (
      <div className="flex justify-end">
        <div className="bg-accent text-accent-text max-w-[80%] rounded-[--radius-card] px-3 py-2 text-sm whitespace-pre-wrap">
          {user.text}
        </div>
      </div>
    );
  }

  const agent = payloadOf(event, 'agent.message');
  if (agent) {
    return (
      <div className="bg-surface border-border max-w-[88%] rounded-[--radius-card] border px-3 py-2">
        <Markdown>{agent.text}</Markdown>
      </div>
    );
  }

  const changed = payloadOf(event, 'workspace.changed');
  if (changed) {
    return <WorkspaceChange change={changed} />;
  }

  return (
    <div className="text-muted flex flex-wrap items-center gap-x-3 gap-y-1 px-2 text-[0.8125rem]">
      <span className="break-all">{describe(event)}</span>
      <JobCost event={event} startedAt={startedAt} />
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

/** One line for an event the timeline shows but does not lay out specially. */
function describe(event: Event): React.ReactNode {
  switch (event.type) {
    case 'session.created':
      return 'Session created.';
    // The provider result repeats the last agent message, so only the fact that
    // the turn ended is worth showing.
    case 'job.completed':
      return (
        <span className="inline-flex items-center gap-1.5">
          <CheckCircle2 className="text-ok size-3.5" /> Done.
        </span>
      );
    case 'job.failed':
      return (
        <span className="text-danger inline-flex items-center gap-1.5">
          <AlertTriangle className="size-3.5" />
          Failed: {payloadOf(event, 'job.failed')?.error ?? 'unknown error'}
        </span>
      );
    case 'job.cancelled':
      return 'Cancelled.';
    case 'validation.resolved':
      return payloadOf(event, 'validation.resolved')?.approved
        ? 'Permission granted.'
        : 'Permission denied.';
    case 'user_input.resolved':
      return `Answered: ${payloadOf(event, 'user_input.resolved')?.value ?? ''}`;
    default:
      return <Badge>{event.type}</Badge>;
  }
}
