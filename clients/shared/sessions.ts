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

/** Projects in alphabetical order, by the same collation as titles. */
export function projectsByName<T extends { id: string; name: string }>(projects: readonly T[]): T[] {
  return [...projects].sort((a, b) => titles.compare(a.name, b.name) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

// What a person comes back to a Session for: something the agent said, or a
// Job that failed. Core counts the same events when it lists a Session unread.
const UNREAD_TYPES = new Set(['agent.message', 'job.failed']);

/**
 * Where what the person has not read begins: the first event past what they
 * read that they come back for. It is read when a Session is opened, before
 * it is marked read, so the place can be shown after the mark has moved.
 */
export function firstUnread(
  readSequence: number,
  events: readonly { sequence: number; type: string }[],
): number | undefined {
  let first: number | undefined;
  for (const event of events) {
    if (event.sequence > readSequence && UNREAD_TYPES.has(event.type) && (first === undefined || event.sequence < first)) first = event.sequence;
  }
  return first;
}

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

/** What a list of Sessions needs to sort them by what is owed to them. */
export interface InboxSession extends TitledSession {
  updatedAt: string;
  activeJobStatus?: string;
  unread?: boolean;
  snoozed?: boolean;
  snoozedUntil?: string;
  snoozeUntilChange?: boolean;
}

export type SessionSectionKey = 'needs' | 'running' | 'unseen' | 'recent' | 'snoozed';

export const SESSION_SECTIONS: Record<SessionSectionKey, string> = {
  needs: 'Needs you',
  running: 'Running',
  unseen: 'Done, not seen',
  recent: 'Recent',
  snoozed: 'Snoozed',
};

const WAITING_STATUSES = new Set(['WAITING_VALIDATION', 'WAITING_INPUT']);

/** Titles compared without case or accents: "deploiement" finds "Déploiement". */
const folded = (text: string) => text.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();

/** Whether a title holds what was typed, every word of it, in any order. */
export function matchesSearch(title: string | undefined, query: string): boolean {
  const words = folded(query).split(/\s+/).filter(Boolean);
  const haystack = folded(title || 'Untitled session');
  return words.every((word) => haystack.includes(word));
}


/**
 * Whether a Session is out of the way right now. Core says so when it lists
 * it; a snooze whose time came since is over without waiting for the next
 * list.
 */
export function isSnoozed(session: InboxSession, now = Date.now()): boolean {
  if (!session.snoozed) return false;
  return !(session.snoozedUntil && Date.parse(session.snoozedUntil) <= now);
}

/** A snooze that ended and the Session not opened since: it comes back unread. */
export function wokeFromSnooze(session: InboxSession, now = Date.now()): boolean {
  return Boolean(session.snoozedUntil || session.snoozeUntilChange) && !isSnoozed(session, now);
}

/**
 * The Sessions sorted by what they are owed, first section first: those that
 * wait for a decision, those that work, those that finished — or came back
 * from a snooze — since they were last read, the rest by last activity, and
 * last the ones put out of the way.
 *
 * `waiting` names the Sessions with a request open, from the attention route:
 * a snooze never hides one. A Session the person is looking at counts as read.
 * Empty sections are kept, so a client chooses whether to show them.
 */
export function sessionSections<T extends InboxSession>(
  sessions: readonly T[],
  options: { waiting?: (id: string) => boolean; current?: string; query?: string; now?: number } = {},
): { key: SessionSectionKey; sessions: T[] }[] {
  const now = options.now ?? Date.now();
  const sections: Record<SessionSectionKey, T[]> = { needs: [], running: [], unseen: [], recent: [], snoozed: [] };
  for (const session of sessions) {
    if (options.query && !matchesSearch(session.title, options.query)) continue;
    if (options.waiting?.(session.id) || WAITING_STATUSES.has(session.activeJobStatus ?? '')) sections.needs.push(session);
    else if (isSnoozed(session, now)) sections.snoozed.push(session);
    else if (session.activeJobStatus) sections.running.push(session);
    else if ((session.unread || wokeFromSnooze(session, now)) && session.id !== options.current) sections.unseen.push(session);
    else sections.recent.push(session);
  }
  const newest = (a: T, b: T) => b.updatedAt.localeCompare(a.updatedAt) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  // What has waited longest is answered first.
  sections.needs.sort((a, b) => -newest(a, b));
  sections.running.sort(newest);
  sections.unseen.sort(newest);
  sections.recent.sort(newest);
  // The first to come back first; those waiting for a change after them.
  sections.snoozed.sort((a, b) => (a.snoozedUntil ?? '￿').localeCompare(b.snoozedUntil ?? '￿') || newest(a, b));
  return (Object.keys(sections) as SessionSectionKey[]).map((key) => ({ key, sessions: sections[key] }));
}

/** One way to snooze a Session, as both clients offer it. */
export interface SnoozeChoice {
  id: string;
  label: string;
  /** When it comes back; absent for "when it changes". */
  until?: Date;
}

/**
 * The usual ways to put a Session away, from a given moment: an hour, this
 * evening while there is one left, tomorrow morning, next Monday morning, or
 * until the agent says something new.
 */
export function snoozeChoices(now = new Date()): SnoozeChoice[] {
  const at = (days: number, hour: number) => {
    const date = new Date(now);
    date.setDate(date.getDate() + days);
    date.setHours(hour, 0, 0, 0);
    return date;
  };
  const choices: SnoozeChoice[] = [{ id: 'hour', label: 'In 1 hour', until: new Date(now.getTime() + 3600_000) }];
  if (now.getHours() < 17) choices.push({ id: 'evening', label: 'This evening', until: at(0, 18) });
  choices.push({ id: 'tomorrow', label: 'Tomorrow morning', until: at(1, 9) });
  // Next Monday, a week away when today is Monday.
  choices.push({ id: 'week', label: 'Next week', until: at(((8 - now.getDay()) % 7) || 7, 9) });
  choices.push({ id: 'change', label: 'When it changes' });
  return choices;
}

/** When a snoozed Session comes back, said shortly: "18:00", "Mon 09:00", "on change". */
export function snoozeBack(session: InboxSession, now = new Date()): string {
  if (!session.snoozedUntil) return 'on change';
  const until = new Date(session.snoozedUntil);
  const time = until.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  const sameDay = until.toDateString() === now.toDateString();
  if (sameDay) return time;
  const days = (until.getTime() - now.getTime()) / 86_400_000;
  return days < 6 ? `${until.toLocaleDateString(undefined, { weekday: 'short' })} ${time}` : until.toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
}
