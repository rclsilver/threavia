import type { InvalidateQueryFilters, QueryClient } from '@tanstack/react-query';

import { dropInterruptedAttention, dropResolvedAttention } from './attention-cache';
import { announceJobEnded } from '@/lib/job-ended';
import { isActive, presenceSentWithStream } from '@/lib/presence';

import { authorizedFetch, CHANNEL } from './client';
import { keys } from './keys';
import { payloadOf, type Event, type Job, type Snapshot } from './types';

// Core sends a keepalive every 20 seconds, even when no Job is running.
const SILENCE_LIMIT = 45_000;
/** Quiet for this long, a wake-up replaces the connection instead of trusting it. */
const STALE_AFTER = 20_000;
/** Invalidations are sent once events have been quiet this long, or after the maximum wait. */
const INVALIDATE_QUIET = 150;
const INVALIDATE_MAX_WAIT = 1_000;

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
  private lastHeard = 0;
  private live = false;
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
    onStateChange(false);
    void this.run(abort, onStateChange);
  }

  close() {
    this.abort?.abort();
    this.abort = null;
    this.live = false;
  }

  /**
   * Whether a wake-up has any reason to replace the connection: it is not
   * open, or nothing (not even a keep-alive) has been heard for a while. A
   * healthy stream is left alone, since every reopening costs a full refetch.
   */
  get stale() {
    return !this.abort || !this.live || Date.now() - this.lastHeard > STALE_AFTER;
  }

  private async run(abort: AbortController, onStateChange: (connected: boolean) => void) {
    // Backs off so a Core that is down is not hammered, and recovers quickly
    // when it is a blip.
    let backoff = 1000;

    while (!abort.signal.aborted) {
      // A network can stop delivering without closing the TCP connection.
      // Bound both opening and reading, then replay from the last event read.
      const connection = new AbortController();
      const stop = () => connection.abort();
      abort.signal.addEventListener('abort', stop, { once: true });
      let timer = setTimeout(stop, SILENCE_LIMIT);
      const heard = () => {
        this.lastHeard = Date.now();
        clearTimeout(timer);
        timer = setTimeout(stop, SILENCE_LIMIT);
      };
      try {
        // The stream says whether the person is looking, so Core never holds
        // a fresh connection as present by mistake until the first change.
        const active = isActive();
        const headers = new Headers({ Accept: 'text/event-stream' });
        headers.set('X-Threavia-Active', String(active));
        const response = await authorizedFetch(`/api/v1/events?after=${this.cursor}&channel=${CHANNEL}`, {
          headers,
          signal: connection.signal,
        });
        if (connection.signal.aborted) {
          void response.body?.cancel().catch(() => {});
          throw new Error('the stream timed out');
        }
        presenceSentWithStream(active);
        if (!response.ok || !response.body) {
          throw new Error(`the stream did not open (${response.status})`);
        }

        heard();
        this.live = true;
        onStateChange(true);
        backoff = 1000;
        await this.consume(response.body, connection.signal, heard);
      } catch {
        // An abort is the caller closing the stream, not a failure.
        if (abort.signal.aborted) return;
      } finally {
        clearTimeout(timer);
        abort.signal.removeEventListener('abort', stop);
        connection.abort();
      }

      if (abort.signal.aborted) return;
      this.live = false;
      onStateChange(false);
      await sleep(backoff, abort.signal);
      backoff = Math.min(backoff * 2, 30_000);
    }
  }

  /** Reads frames until the connection ends. */
  private async consume(body: ReadableStream<Uint8Array>, signal: AbortSignal, heard: () => void) {
    const reader = body.getReader();
    // Decoded with `stream: true` so a multi-byte character split across two
    // chunks is held until the rest of it arrives.
    const decoder = new TextDecoder();
    let buffer = '';

    const cancel = () => { void reader.cancel().catch(() => {}); };
    signal.addEventListener('abort', cancel, { once: true });
    try {
      while (!signal.aborted) {
        const { done, value } = await reader.read();
        if (done || signal.aborted) return;

        buffer += decoder.decode(value, { stream: true });
        // A frame ends at a blank line. Anything after the last one is a frame
        // still arriving, so it stays in the buffer.
        let boundary = buffer.indexOf('\n\n');
        while (boundary !== -1) {
          heard(); // Comment keepalives count too; agent activity is optional.
          this.frame(buffer.slice(0, boundary));
          buffer = buffer.slice(boundary + 2);
          boundary = buffer.indexOf('\n\n');
        }
      }
    } finally {
      signal.removeEventListener('abort', cancel);
      await reader.cancel().catch(() => {});
      reader.releaseLock();
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
      this.deliver(payload);
      return;
    }
    if (name === 'ephemeral' && payload.sessionId) {
      this.onActivity?.({ sessionId: payload.sessionId, kind: payload.type, at: Date.now() });
    }
  }

  private readonly pendingInvalidations = new Map<string, InvalidateQueryFilters>();
  private flushTimer: ReturnType<typeof setTimeout> | null = null;
  private flushDeadline = 0;

  /**
   * Marks queries stale, once per burst.
   *
   * A reconnection replays history, and a client that has just loaded replays
   * all of it: one invalidation per event cancels and restarts the same
   * requests thousands of times, until the browser has no connection left.
   * Invalidations of one key are merged and sent when events stop arriving, or
   * at the latest after a second of them arriving.
   */
  private invalidate(filters: InvalidateQueryFilters) {
    this.pendingInvalidations.set(JSON.stringify(filters.queryKey ?? null), filters);
    const now = Date.now();
    if (this.flushTimer === null) this.flushDeadline = now + INVALIDATE_MAX_WAIT;
    else clearTimeout(this.flushTimer);
    this.flushTimer = setTimeout(() => this.flush(), Math.max(0, Math.min(INVALIDATE_QUIET, this.flushDeadline - now)));
  }

  private flush() {
    this.flushTimer = null;
    const pending = [...this.pendingInvalidations.values()];
    this.pendingInvalidations.clear();
    for (const filters of pending) void this.queries.invalidateQueries(filters);
  }

  /** Routes one event onto the cache. */
  deliver(event: Event) {
    // Only consuming global events advances the cursor. A Session snapshot
    // says nothing about missed events belonging to other Sessions.
    this.cursor = Math.max(this.cursor, event.sequence);
    this.apply(event);
  }

  private apply(event: Event) {
    if (event.sessionId) {
      this.appendToSession(event.sessionId, event);
    }

    switch (event.type) {
      case 'control_access.changed':
        void this.invalidate({ queryKey: ['control-accesses'] });
        void this.invalidate({ queryKey: ['audit'] });
        break;
      case 'monitor.changed':
        void this.invalidate({ queryKey: ['monitors'] });
        break;
      case 'mcp.changed':
        void this.invalidate({ queryKey: ['mcp'] });
        break;
      case 'review.changed':
        void this.invalidate({ queryKey: ['review', event.sessionId ?? ''] });
        void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;
      case 'user.message':
        if (payloadOf(event, 'user.message')?.delivery === 'NOW' && event.jobId) {
          dropInterruptedAttention(this.queries, event.sessionId, event.jobId, event.timestamp);
          void this.invalidate({ queryKey: keys.attention() });
          void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
          void this.invalidate({ queryKey: ['sessions'] });
        }
        break;
      // Pending attention is current state, not a count of unread events, and
      // a Session carries its own copy in the snapshot it was opened with. Both
      // have to follow, or a request answered elsewhere stays on screen here.
      case 'validation.requested':
      case 'user_input.requested':
        void this.invalidate({ queryKey: keys.attention() });
        // Re-read rather than built from the event: what makes an item worth a
        // notification is decided by Core, and this payload does not carry it.
        void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;

      case 'validation.resolved':
      case 'user_input.resolved':
        void this.invalidate({ queryKey: keys.attention() });
        // A resolution names exactly what it resolved, so the card goes at
        // once rather than after a round trip.
        this.dropResolved(event);
        break;

      case 'session.deleted': {
        const id = event.sessionId || payloadOf(event, 'session.deleted')?.sessionId || '';
        void this.invalidate({ queryKey: ['sessions'] });
        void this.invalidate({ queryKey: keys.snapshot(id) });
        void this.invalidate({ queryKey: keys.attention() });
        void this.invalidate({ queryKey: keys.audit() });
        break;
      }

      // Read on one client: it stops standing out on the others.
      case 'session.read':
        void this.invalidate({ queryKey: ['sessions'] });
        void this.invalidate({ queryKey: ['projects'] });
        break;

      case 'session.created':
      case 'session.renamed':
      case 'session.archived':
      case 'session.restored':
      case 'session.pinned':
      case 'session.model_changed':
        if (['session.created', 'session.archived', 'session.restored'].includes(event.type)) {
          void this.invalidate({ queryKey: keys.audit() });
        }
        void this.invalidate({ queryKey: ['sessions'] });
        // The snapshot carries the title too, so a Session open on another
        // screen follows the rename rather than keeping the old one until a
        // reload.
        void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;

      // A new Run means the Session moved to another backend, which the cached
      // snapshot cannot derive from the event alone.
      case 'run.created':
      case 'run.resume_unavailable':
      case 'run.resume_available':
      case 'run.resume_failed':
        void this.invalidate({ queryKey: keys.audit() });
        void this.invalidate({ queryKey: keys.repository(event.sessionId ?? '') });
        void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
        break;

      // The sidebar says which Session is working, from the list rather than
      // from every snapshot, so the list follows each Job as it moves.
      case 'job.created':
      case 'job.started':
      case 'job.waiting_backend':
      case 'job.completed':
      case 'job.failed':
      case 'job.cancelled':
        if (event.type === 'job.waiting_backend') {
          void this.invalidate({ queryKey: keys.backends() });
        }
        void this.invalidate({ queryKey: ['sessions'] });
        // What the agent said, or a failure, may now be unread in a Project
        // the sidebar is not showing.
        if (event.type === 'job.completed' || event.type === 'job.failed') {
          void this.invalidate({ queryKey: ['projects'] });
        }
        // Work that ended may have committed, pushed or left files behind.
        if (event.type !== 'job.created' && event.type !== 'job.started') {
          if (event.jobId) dropInterruptedAttention(this.queries, event.sessionId, event.jobId, event.timestamp);
          void this.invalidate({ queryKey: keys.attention() });
          void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
          void this.invalidate({ queryKey: keys.repository(event.sessionId ?? '') });
        }
        // A Job may be a Schedule's doing, which changes what it last did.
        if (event.type === 'job.created') {
          void this.invalidate({ queryKey: keys.schedules(event.sessionId ?? '') });
        }
        // Told as it happens, not as it is replayed: a reconnection resends
        // what was missed, and work that ended an hour ago is not news.
        if ((event.type === 'job.completed' || event.type === 'job.failed') && event.sessionId &&
          Date.now() - Date.parse(event.timestamp) < 2 * 60_000) {
          const completed = payloadOf(event, 'job.completed');
          const failed = payloadOf(event, 'job.failed');
          announceJobEnded({
            sessionId: event.sessionId,
            failed: Boolean(failed),
            text: completed?.summary ?? failed?.error ?? '',
          });
        }
        break;

      case 'schedule.created':
      case 'schedule.updated':
      case 'schedule.deleted':
      case 'schedule.skipped':
        void this.invalidate({ queryKey: keys.audit() });
        void this.invalidate({ queryKey: keys.schedules(event.sessionId ?? '') });
        break;

      case 'backend.status_changed':
        void this.invalidate({ queryKey: keys.audit() });
        void this.invalidate({ queryKey: keys.backends() });
        void this.invalidate({ queryKey: ['sessions'] });
        void this.invalidate({ queryKey: ['snapshot'] });
        void this.invalidate({ queryKey: keys.attention() });
        break;
      case 'backend.registered':
      case 'backend.revoked':
        void this.invalidate({ queryKey: keys.audit() });
        void this.invalidate({ queryKey: keys.backends() });
        break;
      case 'backend.quotas_updated':
      case 'backend.models_updated':
      case 'backend.default_changed':
        void this.invalidate({ queryKey: keys.backends() });
        break;

      case 'directory.changed':
        void this.invalidate({ queryKey: ['directories'] });
        void this.invalidate({ queryKey: ['bindings'] });
        break;
      case 'project.created':
      case 'project.archived':
      case 'project.restored':
      case 'project.updated':
      case 'project.deleted':
        void this.invalidate({ queryKey: keys.audit() });
        void this.invalidate({ queryKey: ['projects'] });
        void this.invalidate({ queryKey: ['project'] });
        if (event.type === 'project.deleted') {
          void this.invalidate({ queryKey: ['sessions'] });
          void this.invalidate({ queryKey: keys.attention() });
        }
        break;

      // An agent writes project memory through the Core Tools, so these arrive
      // without any client having asked for them.
      case 'task.created':
      case 'task.updated':
      case 'task.deleted':
        void this.invalidate({ queryKey: ['tasks'] });
        void this.invalidate({ queryKey: keys.audit() });
        break;

      case 'skill.installed':
      case 'skill.removed': {
        const payload = payloadOf(event, 'skill.installed') ?? payloadOf(event, 'skill.removed');
        void this.invalidate({ queryKey: event.projectId ? keys.skills(event.projectId) : ['skills'] });
        void this.invalidate({ queryKey: payload?.skillId ? ['skill-files', payload.skillId] : ['skill-files'] });
        void this.invalidate({ queryKey: keys.audit() });
        break;
      }

      // Made global, or turned on or off in a Project: it moves between lists.
      case 'skill.changed':
        void this.invalidate({ queryKey: ['skills'] });
        void this.invalidate({ queryKey: keys.audit() });
        break;

      // An agent published a file: the project's list has one more.
      case 'artifact.created':
      case 'artifact.deleted':
        void this.invalidate({ queryKey: ['artifacts'] });
        break;

      case 'decision.created':
      case 'decision.superseded':
      case 'decision.updated':
      case 'decision.deleted':
        void this.invalidate({ queryKey: ['decisions'] });
        void this.invalidate({ queryKey: keys.audit() });
        break;

      case 'working_directory.changed':
        void this.invalidate({ queryKey: ['directories'] });
        void this.invalidate({ queryKey: ['sessions'] });
        void this.invalidate({ queryKey: keys.snapshot(event.sessionId ?? '') });
        void this.invalidate({ queryKey: keys.repository(event.sessionId ?? '') });
        void this.invalidate({ queryKey: ['audit'] });
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

      // A snapshot already includes Job state through its cursor, even when
      // its recent timeline window no longer contains the replayed event.
      // Applying that older event would rewind a finished Job to RUNNING.
      if (event.sequence <= snapshot.cursor || snapshot.events.some((seen) => seen.sequence === event.sequence)) {
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
  const settings = payloadOf(event, 'job.started');
  const model = { ...(settings?.model && { model: settings.model }), ...(settings?.effort && { effort: settings.effort }) };
  const waiting = payloadOf(event, 'job.waiting_backend');
  const backendWait = waiting ? { reason: waiting.reason, message: waiting.message, retryAt: waiting.retryAt } : undefined;
  const recovery = waiting || ['job.started', 'job.completed', 'job.failed', 'job.cancelled'].includes(event.type) ? { backendWait } : {};
  const status = payloadOf(event, 'user.message')?.delivery === 'NOW' ? 'RUNNING' : jobStatusFor(event.type);
  if (!status || !event.jobId) return jobs;

  const known = jobs.some((job) => job.id === event.jobId);
  if (!known) {
    // A Job created by another client, or by this one before the snapshot was
    // taken. The identifiers are all the list needs to show it as live.
    return [
      ...jobs,
      {
        ...model,
        ...recovery,
        id: event.jobId,
        runId: event.runId ?? '',
        status,
        createdAt: event.timestamp,
        updatedAt: event.timestamp,
      },
    ];
  }
  return jobs.map((job) =>
    job.id === event.jobId ? { ...job, ...model, ...recovery, status, updatedAt: event.timestamp } : job,
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
    case 'job.waiting_backend':
      return 'WAITING_BACKEND';
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
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener('abort', done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal.addEventListener('abort', done, { once: true });
    if (signal.aborted) done();
  });
}
