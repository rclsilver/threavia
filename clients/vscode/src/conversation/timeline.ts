import { payloadOf, type Event } from '../api/types';

/**
 * What the conversation shows, decided without an editor or a page.
 *
 * The same rules as the web client's timeline (web/ui/src/components/
 * timeline.tsx, rowsOf and foldFinished), ported rather than imported: the
 * extension does not build against web/ui, and a reader moving between the
 * browser and the editor must see the same rows in the same order. The
 * webview draws these; the tests check them.
 */

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

/** What a fold holds, counted, so it can be judged without opening it. */
export interface Steps {
  tools: number;
  failed: number;
  notes: number;
}

/**
 * One line of the timeline: a plain event, a tool call, a change of day, or the
 * folded steps of a finished Job.
 */
export type Row =
  | { kind: 'event'; key: number; event: Event; startedAt?: string }
  | { kind: 'tool'; key: number; call: ToolCall; jobId?: string }
  | { kind: 'day'; key: number; label: string }
  | { kind: 'steps'; key: number; at: string; steps: Steps; open: boolean };

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

/** How a Job ends, in the log. */
const ENDINGS = new Set(['job.completed', 'job.failed', 'job.cancelled']);

/** The Job a row belongs to, if any. */
export function jobOf(row: Row): string | undefined {
  if (row.kind === 'tool') return row.jobId;
  if (row.kind === 'event') return row.event.jobId || undefined;
  return undefined;
}

/**
 * The day an event belongs to, named the way a person would.
 *
 * A Session is long-lived: without this, work from last week and work from
 * this morning are one undivided scroll.
 */
export function dayOf(timestamp: string, now: Date = new Date()): { key: string; label: string } {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return { key: '', label: '' };

  const key = date.toDateString();
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);

  if (key === now.toDateString()) return { key, label: 'Today' };
  if (key === yesterday.toDateString()) return { key, label: 'Yesterday' };
  return {
    key,
    label: date.toLocaleDateString(undefined, {
      day: 'numeric',
      month: 'long',
      year: date.getFullYear() === now.getFullYear() ? undefined : 'numeric',
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
export function rowsOf(events: Event[], now: Date = new Date()): Row[] {
  const rows: Row[] = [];
  const byCall = new Map<string, Extract<Row, { kind: 'tool' }>>();
  // When each Job started, so its ending can say how long it took. The
  // timeline already carries both timestamps.
  const startedAt = new Map<string, string>();
  let day = '';

  // A separator goes in front of the first row of each day, including the
  // first of all: a reader opening a Session should see when it happened
  // without reading a timestamp off every line.
  const openDay = (event: Event) => {
    const { key, label } = dayOf(event.timestamp, now);
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
      if (row) row.call = { ...row.call, output: completed.output?.output ?? '', done: true };
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
 * the person's messages, the files the Job changed, what it published, the
 * agent's last word, and how the Job ended. Two steps or more fold — one alone
 * is already a single line.
 */
export function foldFinished(rows: Row[], opened: ReadonlySet<number>): Row[] {
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
    // steps around it: the steps fold into one line, and the results follow
    // it in their order, so publishing a chart does not leave one stray step
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
    // keeps its identity when rows around it change.
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

/** The label of a fold: "12 steps · 2 messages", or "Hide …" once open. */
export function stepsLabel(row: Extract<Row, { kind: 'steps' }>): string {
  const { tools, notes } = row.steps;
  const parts = [
    tools > 0 && `${tools} ${tools === 1 ? 'step' : 'steps'}`,
    notes > 0 && `${notes} ${notes === 1 ? 'message' : 'messages'}`,
  ].filter(Boolean);
  return `${row.open ? 'Hide ' : ''}${parts.join(' · ') || 'Steps'}`;
}

/**
 * The prompt a long Job answers, to hold at the top of the view once it has
 * scrolled out of sight: the last message sent above the first row on screen.
 *
 * `rows` are the rows in order, each with where it sits in the scroller; the
 * answer is an index, or -1 when no message is above, or when the one above
 * still shows — a prompt on screen needs no copy of itself.
 */
export function stickyPrompt(
  rows: readonly { user: boolean; top: number; bottom: number }[],
  scrollTop: number,
): number {
  let first = -1;
  for (let index = 0; index < rows.length; index++) {
    if (rows[index].bottom > scrollTop) {
      first = index;
      break;
    }
  }
  // Scrolled past the end of everything: the last row is the one above.
  if (first === -1) first = rows.length - 1;
  for (let index = first; index >= 0; index--) {
    if (rows[index].user) return rows[index].bottom <= scrollTop + 4 ? index : -1;
  }
  return -1;
}

/** The field that says what a call actually did: a command, a path, a URL. */
const TELLING = ['command', 'file_path', 'path', 'notebook_path', 'pattern', 'query', 'url', 'prompt'];

/** The one argument worth showing on a collapsed tool call. */
export function summariseInput(input: Record<string, unknown>): string {
  for (const key of TELLING) {
    const value = input[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return '';
}

/** The file a tool call worked on, when it names one. */
export function fileOfTool(input: Record<string, unknown>): string | undefined {
  for (const key of ['file_path', 'notebook_path', 'path']) {
    const value = input[key];
    if (typeof value === 'string' && value.trim() && !value.includes('\n')) return value.trim();
  }
  return undefined;
}

/**
 * Renders a tool input readably.
 *
 * A single string field is shown as itself: a shell command belongs on its
 * own lines, not wrapped in JSON quoting a reader has to undo in their head.
 */
export function formatInput(input: Record<string, unknown>): string {
  const entries = Object.entries(input);
  if (entries.length === 1 && typeof entries[0][1] === 'string') return entries[0][1];
  return entries
    .map(([key, value]) =>
      typeof value === 'string' && value.includes('\n')
        ? `${key}:\n${value}`
        : `${key}: ${typeof value === 'string' ? value : JSON.stringify(value)}`,
    )
    .join('\n');
}

/** Strips the mcp__threavia__ prefix, which is noise in a Threavia timeline. */
export function toolName(name: string): string {
  return name.replace(/^mcp__threavia__/, '');
}

/** The line of an event the timeline does not lay out specially. */
export function describe(event: Event): { icon: string; tone?: 'ok' | 'danger' | 'warn'; text: string; code?: string } {
  switch (event.type) {
    case 'session.created':
      return { icon: 'flag', text: 'Session created.' };
    // The provider result repeats the last agent message, so only the fact
    // that the turn ended is worth showing.
    case 'job.completed':
      return { icon: 'pass', tone: 'ok', text: 'Done.' };
    case 'job.failed':
      return {
        icon: 'warning',
        tone: 'danger',
        text: `Failed: ${payloadOf(event, 'job.failed')?.error ?? 'unknown error'}`,
      };
    case 'schedule.skipped':
      return {
        icon: 'calendar',
        tone: 'warn',
        text: `Scheduled message not sent: ${payloadOf(event, 'schedule.skipped')?.reason ?? 'skipped'}.`,
      };
    case 'job.cancelled':
      return { icon: 'circle-slash', text: 'Cancelled.' };
    case 'validation.resolved': {
      // What was decided, not only that something was: the log is read back
      // later, and "Permission granted." alone cannot be trusted then.
      const resolved = payloadOf(event, 'validation.resolved');
      const verb = resolved?.approved ? 'Allowed' : 'Denied';
      const icon = resolved?.approved ? 'workspace-trusted' : 'workspace-untrusted';
      const tone = resolved?.approved ? 'ok' : 'danger';
      if (!resolved?.title) return { icon, tone, text: `${verb}.` };
      // "Bash: go test" — the tool in words, the command as a command.
      const [tool, ...rest] = resolved.title.split(': ');
      return rest.length
        ? { icon, tone, text: `${verb}: ${tool} `, code: rest.join(': ') }
        : { icon, tone, text: `${verb}: `, code: tool };
    }
    case 'user_input.resolved':
      return { icon: 'reply', text: `Answered: ${payloadOf(event, 'user_input.resolved')?.value ?? ''}` };
    default:
      return { icon: 'circle-outline', text: event.type };
  }
}
