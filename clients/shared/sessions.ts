interface TitledSession {
  id: string;
  title?: string;
}

// The clients use the same collation regardless of the browser/editor locale.
// Case and accents do not split the alphabet; numbered titles read naturally.
const titles = new Intl.Collator('en', { sensitivity: 'base', numeric: true });

/** Sort the displayed titles without mutating an API response or query cache. */
export function sessionsByTitle<T extends TitledSession>(sessions: readonly T[]): T[] {
  return [...sessions].sort((a, b) =>
    titles.compare(a.title || 'Untitled session', b.title || 'Untitled session') ||
    (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
}

// What a person comes back to a Session for: something the agent said, or a
// Job that failed. Core counts the same events when it lists a Session unread.
const UNREAD_TYPES = new Set(['agent.message', 'job.failed']);

/**
 * How far to mark a Session read once it is on screen: its last event, when
 * something the person comes back for sits past what they already read.
 * Undefined when there is nothing to clear, so a client that shows a Session
 * does not call Core for every tool the agent runs.
 */
export function readUpTo(
  readSequence: number,
  events: readonly { sequence: number; type: string }[],
): number | undefined {
  let last = 0;
  let unread = false;
  for (const event of events) {
    last = Math.max(last, event.sequence);
    if (event.sequence > readSequence && UNREAD_TYPES.has(event.type)) unread = true;
  }
  return unread ? last : undefined;
}
