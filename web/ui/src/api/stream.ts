import type { QueryClient } from '@tanstack/react-query';

import { dropResolvedAttention } from './attention-cache';
import { CHANNEL } from './client';
import { keys } from './keys';
import { payloadOf, type Event, type Job, type Snapshot } from './types';

/**
 * A liveness signal. It carries no sequence and is never stored: it says the
 * agent is still working between two things worth remembering.
 *
 * Nothing orders it against the timeline, so one can arrive after the Job it
 * describes has ended. A view shows it only while a Job is actually running,
 * which makes a late signal harmless rather than a lie.
 */
export interface Activity {
  sessionId: string;
  kind: string;
  at: number;
}

/**
 * The global event stream, and the only writer the cache has besides a fetch.
 *
 * One stream per user carries every Session, so the client holds a single
 * cursor rather than one per Session (spec section 5). An event arriving here
 * patches the cached snapshot directly and invalidates whatever it contradicts,
 * which is what makes a resolved validation disappear from every open view at
 * once instead of when something happens to refetch.
 *
 * The cursor is what makes a dropped connection cost nothing: Core replays from
 * it, and the database is the source of truth while the in-process broker is
 * only the fast path.
 */
export class EventStream {
  private source: EventSource | null = null;
  private cursor = 0;
  private readonly queries: QueryClient;
  private onActivity: ((signal: Activity) => void) | null = null;

  constructor(queries: QueryClient) {
    this.queries = queries;
  }

  /** The sequence reached so far, which a reconnection resumes from. */
  get position() {
    return this.cursor;
  }

  /**
   * Raises the cursor to what a snapshot reported. A snapshot is a point the
   * stream has already passed, so the stream never rewinds for it.
   */
  seen(sequence: number) {
    this.cursor = Math.max(this.cursor, sequence);
  }

  open(onStateChange: (connected: boolean) => void, onActivity?: (signal: Activity) => void) {
    this.close();
    this.onActivity = onActivity ?? null;

    const source = new EventSource(`/api/v1/events?after=${this.cursor}&channel=${CHANNEL}`);
    this.source = source;

    source.onopen = () => onStateChange(true);
    source.onerror = () => {
      // EventSource reconnects on its own, and Core resumes from Last-Event-ID.
      // Reporting the gap is all there is to do here.
      onStateChange(false);
    };

    // Core names its frames, so there is no default `message` to listen on:
    // `event` carries the persisted timeline, `ephemeral` the liveness signals
    // that are streamed and never stored. A client listening on `message`
    // receives nothing at all, over a connection that looks perfectly healthy.
    source.addEventListener('event', (frame: MessageEvent<string>) => {
      const event = JSON.parse(frame.data) as Event;
      this.seen(event.sequence);
      this.apply(event);
    });

    source.addEventListener('ephemeral', (frame: MessageEvent<string>) => {
      const signal = JSON.parse(frame.data) as Event;
      if (signal.sessionId) {
        this.onActivity?.({ sessionId: signal.sessionId, kind: signal.type, at: Date.now() });
      }
    });
  }

  close() {
    this.source?.close();
    this.source = null;
  }

  /** Routes one event onto the cache. */
  private apply(event: Event) {
    if (event.sessionId) {
      this.appendToSession(event.sessionId, event);
    }

    switch (event.type) {
      // Pending attention is current state, not a count of unread events, and
      // a Session carries its own copy in the snapshot it was opened with. Both
      // have to follow, or a request answered elsewhere stays on screen here.
      case 'validation.requested':
      case 'user_input.requested':
        void this.queries.invalidateQueries({ queryKey: keys.attention() });
        // Re-read rather than built from the event: what makes an item worth a
        // notification is decided by Core, and this payload does not carry it.
        void this.queries.invalidateQueries({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;

      case 'validation.resolved':
      case 'user_input.resolved':
        void this.queries.invalidateQueries({ queryKey: keys.attention() });
        // A resolution names exactly what it resolved, so the card goes at
        // once rather than after a round trip.
        this.dropResolved(event);
        break;

      case 'session.created':
      case 'session.renamed':
      case 'session.archived':
      case 'session.restored':
        void this.queries.invalidateQueries({ queryKey: ['sessions'] });
        // The snapshot carries the title too, so a Session open on another
        // screen follows the rename rather than keeping the old one until a
        // reload.
        void this.queries.invalidateQueries({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;

      // A new Run means the Session moved to another backend, which the cached
      // snapshot cannot derive from the event alone.
      case 'run.created':
        void this.queries.invalidateQueries({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;

      case 'backend.registered':
      case 'backend.revoked':
        void this.queries.invalidateQueries({ queryKey: keys.backends() });
        break;

      // An agent writes project memory through the Core Tools, so these arrive
      // without any client having asked for them.
      case 'task.created':
      case 'task.updated':
        void this.queries.invalidateQueries({ queryKey: ['tasks'] });
        break;

      case 'decision.created':
      case 'decision.superseded':
        void this.queries.invalidateQueries({ queryKey: ['decisions'] });
        break;

      case 'working_directory.changed':
        void this.queries.invalidateQueries({ queryKey: ['directories'] });
        break;
    }
  }

  /**
   * Removes a resolved request from the Session that is showing it.
   *
   * The resolution names exactly what it resolved, which is what makes this
   * precise: the first valid answer wins, and every other client drops the same
   * card on the same event rather than on its own schedule.
   */
  private dropResolved(event: Event) {
    dropResolvedAttention(this.queries, event.sessionId, {
      validationId: payloadOf(event, 'validation.resolved')?.validationId,
      userInputId: payloadOf(event, 'user_input.resolved')?.requestId,
    });
  }

  /**
   * Appends an event to a cached snapshot, leaving an uncached Session alone:
   * it will arrive complete when it is opened.
   */
  private appendToSession(sessionId: string, event: Event) {
    this.queries.setQueryData<Snapshot>(keys.snapshot(sessionId), (snapshot) => {
      if (!snapshot) return snapshot;

      // A reconnection replays from the cursor, so the same event can arrive
      // twice. The sequence is the identity Core assigned it.
      if (snapshot.events.some((seen) => seen.sequence === event.sequence)) {
        return snapshot;
      }

      return {
        ...snapshot,
        events: [...snapshot.events, event],
        jobs: applyToJobs(snapshot.jobs, event),
        cursor: Math.max(snapshot.cursor, event.sequence),
      };
    });
  }
}

/**
 * Keeps the Job list of a snapshot current from the timeline.
 *
 * The status is derived from the events rather than refetched: the timeline
 * already says what happened, and a round trip to learn it again would only be
 * slower and occasionally disagree.
 */
function applyToJobs(jobs: Job[], event: Event): Job[] {
  const status = jobStatusFor(event.type);
  if (!status || !event.jobId) return jobs;

  const known = jobs.some((job) => job.id === event.jobId);
  if (!known) {
    // A Job created by another client, or by this one before the snapshot was
    // taken. The identifiers are all the list needs to show it as live.
    return [
      ...jobs,
      {
        id: event.jobId,
        runId: event.runId ?? '',
        status,
        createdAt: event.timestamp,
        updatedAt: event.timestamp,
      },
    ];
  }
  return jobs.map((job) =>
    job.id === event.jobId ? { ...job, status, updatedAt: event.timestamp } : job,
  );
}

function jobStatusFor(type: string): Job['status'] | null {
  switch (type) {
    case 'job.created':
      return 'QUEUED';
    case 'job.started':
    case 'validation.resolved':
    case 'user_input.resolved':
      return 'RUNNING';
    case 'validation.requested':
      return 'WAITING_VALIDATION';
    case 'user_input.requested':
      return 'WAITING_INPUT';
    case 'job.completed':
      return 'COMPLETED';
    case 'job.failed':
      return 'FAILED';
    case 'job.cancelled':
      return 'CANCELLED';
    default:
      // CANCELLING has no event of its own: Core sets it when the cancel is
      // accepted, and the mutation refetches the snapshot to show it.
      return null;
  }
}
