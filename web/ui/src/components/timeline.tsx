import { useVirtualizer } from '@tanstack/react-virtual';
import { AlertTriangle, CheckCircle2, FileDiff, Terminal, XCircle } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef } from 'react';

import { Badge } from '@/components/ui/badge';
import { Markdown } from '@/components/markdown';
import { payloadOf, type Event } from '@/api/types';
import { cn } from '@/lib/utils';

/** The events worth a line. The rest is machinery a user never asked to see. */
const RENDERED = new Set([
  'session.created',
  'user.message',
  'agent.message',
  'tool.started',
  'tool.failed',
  'workspace.changed',
  'validation.resolved',
  'user_input.resolved',
  'job.completed',
  'job.failed',
  'job.cancelled',
]);

/**
 * The Session timeline.
 *
 * Virtualised because a Session is append-only and unbounded: a long one holds
 * thousands of events, and rendering them all is what turns a conversation into
 * a frozen tab. Only what fits on screen is in the DOM.
 */
export function Timeline({ events }: { events: Event[] }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const shown = events.filter((event) => RENDERED.has(event.type));

  const virtualizer = useVirtualizer({
    count: shown.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 80,
    overscan: 12,
    getItemKey: (index) => shown[index].sequence,
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
    if (atBottom.current && shown.length > 0) {
      virtualizer.scrollToIndex(shown.length - 1, { align: 'end' });
    }
  }, [shown.length, virtualizer]);

  if (shown.length === 0) {
    return <div className="text-muted flex-1 p-4 text-sm">Nothing yet.</div>;
  }

  return (
    <div ref={parentRef} className="min-h-0 flex-1 overflow-y-auto px-1">
      <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((item) => (
          <div
            key={item.key}
            ref={virtualizer.measureElement}
            data-index={item.index}
            className="absolute top-0 left-0 w-full py-1.5"
            style={{ transform: `translateY(${item.start}px)` }}
          >
            <Entry event={shown[item.index]} />
          </div>
        ))}
      </div>
    </div>
  );
}

function Entry({ event }: { event: Event }) {
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

  const started = payloadOf(event, 'tool.started');
  if (started) {
    return (
      <Line icon={<Terminal className="size-3.5" />} mono>
        {started.name}
      </Line>
    );
  }

  const failed = payloadOf(event, 'tool.failed');
  if (failed) {
    return (
      <Line icon={<XCircle className="text-danger size-3.5" />} mono tone="danger">
        {failed.name}
        {failed.error ? `: ${failed.error}` : ''}
      </Line>
    );
  }

  const changed = payloadOf(event, 'workspace.changed');
  if (changed) {
    return <WorkspaceChange change={changed} />;
  }

  return <Line>{describe(event)}</Line>;
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

function Line({
  icon,
  mono,
  tone,
  children,
}: {
  icon?: React.ReactNode;
  mono?: boolean;
  tone?: 'danger';
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        'text-muted flex items-center gap-2 px-1 text-[0.8125rem]',
        mono && 'font-mono text-xs',
        tone === 'danger' && 'text-danger',
      )}
    >
      {icon}
      <span className="break-all">{children}</span>
    </div>
  );
}

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
