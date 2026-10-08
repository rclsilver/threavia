import type { QueryClient } from '@tanstack/react-query';

import { dropResolvedAttention } from './attention-cache';
import { isActive, presenceSentWithStream } from '@/lib/presence';

import { authorize, CHANNEL } from './client';
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
  private abort: AbortController | null = null;
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

  /**
   * Opens the stream and keeps it open.
   *
   * Read with fetch rather than EventSource, which cannot send a header: a
   * deployment behind a provider wants the same bearer token here as on every
   * other call, and a token in the query string would land in access logs.
   *
   * What EventSource gave for free was the reconnection, and resuming from
   * Last-Event-ID. The reconnection is the loop below; the resume was never
   * EventSource's to give here, because the client already carries its own
   * cursor and Core replays from `after`.
   */
  open(onStateChange: (connected: boolean) => void, onActivity?: (signal: Activity) => void) {
    this.close();
    this.onActivity = onActivity ?? null;

    const abort = new AbortController();
    this.abort = abort;
    void this.run(abort, onStateChange);
  }

  close() {
    this.abort?.abort();
    this.abort = null;
  }

  private async run(abort: AbortController, onStateChange: (connected: boolean) => void) {
    // Backs off so a Core that is down is not hammered, and recovers quickly
    // when it is a blip.
    let backoff = 1000;

    while (!abort.signal.aborted) {
      try {
        // The stream says whether the person is looking, so Core never holds
        // a fresh connection as present by mistake until the first change.
        const active = isActive();
        const headers = authorize(new Headers({ Accept: 'text/event-stream' }));
        headers.set('X-Threavia-Active', String(active));
        const response = await fetch(`/api/v1/events?after=${this.cursor}&channel=${CHANNEL}`, {
          headers,
          signal: abort.signal,
        });
        presenceSentWithStream(active);
        if (!response.ok || !response.body) {
          throw new Error(`the stream did not open (${response.status})`);
        }

        onStateChange(true);
        backoff = 1000;
        await this.consume(response.body, abort.signal);
      } catch {
        // An abort is the caller closing the stream, not a failure.
        if (abort.signal.aborted) return;
      }

      onStateChange(false);
      if (abort.signal.aborted) return;
      await sleep(backoff, abort.signal);
      backoff = Math.min(backoff * 2, 30_000);
    }
  }

  /** Reads frames until the connection ends. */
  private async consume(body: ReadableStream<Uint8Array>, signal: AbortSignal) {
    const reader = body.getReader();
    // Decoded with `stream: true` so a multi-byte character split across two
    // chunks is held until the rest of it arrives.
    const decoder = new TextDecoder();
    let buffer = '';

    while (!signal.aborted) {
      const { done, value } = await reader.read();
      if (done) return;

      buffer += decoder.decode(value, { stream: true });
      // A frame ends at a blank line. Anything after the last one is a frame
      // still arriving, so it stays in the buffer.
      let boundary = buffer.indexOf('\n\n');
      while (boundary !== -1) {
        this.frame(buffer.slice(0, boundary));
        buffer = buffer.slice(boundary + 2);
        boundary = buffer.indexOf('\n\n');
      }
    }
  }

  /**
   * Routes one frame by its name.
   *
   * Core names them, so there is no default `message`: `event` carries the
   * persisted timeline, `ephemeral` the liveness signals that are streamed and
   * never stored. A client reading only unnamed frames receives nothing at all,
   * over a connection that looks perfectly healthy.
   */
  private frame(raw: string) {
    let name = 'message';
    let data = '';

    for (const line of raw.split('\n')) {
      if (line.startsWith(':')) continue; // A comment: Core sends these as keep-alives.
      const colon = line.indexOf(':');
      const field = colon === -1 ? line : line.slice(0, colon);
      const value = colon === -1 ? '' : line.slice(colon + 1).replace(/^ /, '');

      if (field === 'event') name = value;
      // A data field may be repeated; the spec joins them with a newline.
      if (field === 'data') data = data ? `${data}\n${value}` : value;
    }
    if (!data) return;

    const payload = JSON.parse(data) as Event;
    if (name === 'event') {
      this.seen(payload.sequence);
      this.apply(payload);
      return;
    }
    if (name === 'ephemeral' && payload.sessionId) {
      this.onActivity?.({ sessionId: payload.sessionId, kind: payload.type, at: Date.now() });
    }
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
      case 'session.deleted':
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

      // The sidebar says which Session is working, from the list rather than
      // from every snapshot, so the list follows each Job as it moves.
      case 'job.created':
      case 'job.started':
      case 'job.completed':
      case 'job.failed':
      case 'job.cancelled':
        void this.queries.invalidateQueries({ queryKey: ['sessions'] });
        // A Job may be a Schedule's doing, which changes what it last did.
        if (event.type === 'job.created') {
          void this.queries.invalidateQueries({ queryKey: keys.schedules(event.sessionId ?? '') });
        }
        break;

      case 'schedule.skipped':
        void this.queries.invalidateQueries({ queryKey: keys.schedules(event.sessionId ?? '') });
        break;

      case 'backend.registered':
      case 'backend.revoked':
        void this.queries.invalidateQueries({ queryKey: keys.backends() });
        break;

      // An agent writes project memory through the Core Tools, so these arrive
      // without any client having asked for them.
      case 'task.created':
      case 'task.updated':
      case 'task.deleted':
        void this.queries.invalidateQueries({ queryKey: ['tasks'] });
        break;

      case 'decision.created':
      case 'decision.superseded':
      case 'decision.updated':
      case 'decision.deleted':
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

/** A delay that gives up when the stream is closed under it. */
function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => {
      clearTimeout(timer);
      resolve();
    }, { once: true });
  });
}
