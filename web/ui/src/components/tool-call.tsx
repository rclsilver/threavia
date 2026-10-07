import { AlertTriangle, ChevronDown, ChevronRight, Loader2, Terminal } from 'lucide-react';

import { clock, cn } from '@/lib/utils';

/** One tool call, with whatever is known about how it ended. */
export interface ToolCall {
  id: string;
  name: string;
  input: Record<string, unknown>;
  output?: string;
  error?: string;
  /** When it started, so the log reads as a record and not only a list. */
  at: string;
  /** False while the result has not arrived, which is what makes it look live. */
  done: boolean;
}

/**
 * The field that says what a call actually did.
 *
 * A collapsed row shows one of these rather than the tool name alone: "Bash"
 * tells a reader nothing, "Bash: go test ./..." tells them everything they
 * usually need.
 */
const TELLING = ['command', 'file_path', 'path', 'pattern', 'query', 'url', 'prompt'];

function summarise(input: Record<string, unknown>): string {
  for (const key of TELLING) {
    const value = input[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return '';
}

/** Strips the mcp__<server>__ prefix, which is noise in a Threavia timeline. */
function label(name: string): string {
  return name.replace(/^mcp__threavia__/, '');
}

/**
 * A tool call in the timeline, foldable.
 *
 * Collapsed it is one line, because a session is mostly scrolled past. Expanded
 * it shows what was sent and what came back, which is the difference between
 * seeing that an agent ran something and seeing what it ran.
 */
export function ToolCallEntry({
  call,
  expanded,
  onToggle,
}: {
  call: ToolCall;
  expanded: boolean;
  onToggle: () => void;
}) {
  const summary = summarise(call.input);
  const failed = Boolean(call.error);

  return (
    <div
      className={cn(
        'border-border overflow-hidden rounded-[--radius-card] border',
        expanded ? 'bg-surface' : 'border-transparent',
      )}
    >
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="hover:bg-surface-2 flex w-full items-center gap-2 px-2 py-1 text-left text-xs"
      >
        {expanded ? (
          <ChevronDown className="text-muted size-3.5 shrink-0" />
        ) : (
          <ChevronRight className="text-muted size-3.5 shrink-0" />
        )}

        {!call.done ? (
          <Loader2 className="text-accent size-3.5 shrink-0 animate-spin" />
        ) : failed ? (
          <AlertTriangle className="text-danger size-3.5 shrink-0" />
        ) : (
          <Terminal className="text-muted size-3.5 shrink-0" />
        )}

        <time
          dateTime={call.at}
          className="text-muted shrink-0 font-mono text-[0.6875rem] opacity-60"
        >
          {clock(call.at)}
        </time>

        <span className={cn('font-mono shrink-0', failed ? 'text-danger' : 'text-text')}>
          {label(call.name)}
        </span>
        {summary && (
          <span className="text-muted truncate font-mono">{summary.replace(/\s+/g, ' ')}</span>
        )}
      </button>

      {expanded && (
        <div className="space-y-2 px-3 pt-1 pb-3">
          <Block title="Input">{format(call.input)}</Block>
          {call.error !== undefined && (
            <Block title="Error" tone="danger">
              {call.error}
            </Block>
          )}
          {call.output !== undefined && <Block title="Output">{call.output}</Block>}
          {!call.done && <p className="text-muted text-xs">Still running…</p>}
        </div>
      )}
    </div>
  );
}

function Block({
  title,
  tone,
  children,
}: {
  title: string;
  tone?: 'danger';
  children: string;
}) {
  const text = children.trim();
  if (!text) {
    return (
      <div>
        <Label>{title}</Label>
        <p className="text-muted text-xs">empty</p>
      </div>
    );
  }

  return (
    <div>
      <Label>{title}</Label>
      <pre
        className={cn(
          'bg-surface-2 border-border max-h-80 overflow-auto rounded-md border p-2 font-mono text-xs whitespace-pre-wrap',
          tone === 'danger' && 'text-danger',
        )}
      >
        {text}
      </pre>
    </div>
  );
}

function Label({ children }: { children: string }) {
  return (
    <span className="text-muted mb-1 block text-[0.6875rem] tracking-wide uppercase">
      {children}
    </span>
  );
}

/**
 * Renders a tool input readably.
 *
 * A single string field is shown as itself: a shell command belongs on its own
 * lines, not wrapped in JSON quoting that a reader has to undo in their head.
 */
function format(input: Record<string, unknown>): string {
  const entries = Object.entries(input);
  if (entries.length === 1 && typeof entries[0][1] === 'string') {
    return entries[0][1];
  }
  if (entries.length === 0) return '';

  return entries
    .map(([key, value]) =>
      typeof value === 'string' && value.includes('\n')
        ? `${key}:\n${value}`
        : `${key}: ${typeof value === 'string' ? value : JSON.stringify(value)}`,
    )
    .join('\n');
}
