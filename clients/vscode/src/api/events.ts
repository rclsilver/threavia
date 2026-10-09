import type { Activity } from './stream';
import { payloadOf, type Event } from './types';

/**
 * What an event changes, said as the things a view holds.
 *
 * The same reactions as the web client's stream (web/ui/src/api/stream.ts),
 * written as data so they can be tested without an editor: the stream turns
 * each event into effects, and the views subscribe to the effects they show.
 */
export type Effect =
  /** What waits for the user is stale: re-read /me/attention. */
  | { kind: 'attention' }
  /** Session lists, pinned ones included, are stale. */
  | { kind: 'sessions' }
  /** The snapshot of one Session is stale (title, runs, attention). */
  | { kind: 'snapshot'; sessionId: string }
  /** The git state of one Session may have moved. */
  | { kind: 'repository'; sessionId: string }
  /** A request was answered, here or anywhere else. */
  | { kind: 'resolved'; sessionId?: string; validationId?: string; userInputId?: string }
  /** Work ended, recently enough to be news. */
  | { kind: 'jobEnded'; sessionId: string; failed: boolean; text: string };

/** How old an ended Job can be and still be told: a replay is not news. */
export const JOB_ENDED_FRESH_FOR = 2 * 60_000;

export function effectsOf(event: Event, now: number = Date.now()): Effect[] {
  const sessionId = event.sessionId ?? '';
  switch (event.type) {
    // Pending attention is current state, not a count of unread events, so it
    // is re-read rather than built from the event: what makes an item worth a
    // notification is decided by Core, and this payload does not carry it.
    case 'validation.requested':
    case 'user_input.requested':
      return [{ kind: 'attention' }, { kind: 'sessions' }, ...snapshot(sessionId)];

    // A resolution names exactly what it resolved, so a notification or a row
    // showing it goes at once rather than after a round trip.
    case 'validation.resolved':
    case 'user_input.resolved':
      return [
        { kind: 'attention' },
        { kind: 'sessions' },
        ...snapshot(sessionId),
        {
          kind: 'resolved',
          sessionId: event.sessionId,
          validationId: payloadOf(event, 'validation.resolved')?.validationId,
          userInputId: payloadOf(event, 'user_input.resolved')?.requestId,
        },
      ];

    case 'session.created':
    case 'session.renamed':
    case 'session.archived':
    case 'session.restored':
    case 'session.deleted':
    case 'session.pinned':
      return [{ kind: 'sessions' }, ...snapshot(sessionId)];

    // A new Run means the Session moved to another backend, which also moves
    // where its repository is read.
    case 'run.created':
      return [...snapshot(sessionId), ...repository(sessionId)];

    // The sidebar says which Session is working from the list, so the list
    // follows each Job as it moves.
    case 'job.created':
    case 'job.started':
      return [{ kind: 'sessions' }];

    case 'job.cancelled':
      // An interrupted turn drops what it was waiting on without an event of
      // its own, and work that stopped may have left files behind.
      return [{ kind: 'sessions' }, { kind: 'attention' }, ...repository(sessionId)];

    case 'job.completed':
    case 'job.failed': {
      const effects: Effect[] = [{ kind: 'sessions' }, { kind: 'attention' }, ...repository(sessionId)];
      // Told as it happens, not as it is replayed: a reconnection resends what
      // was missed, and work that ended an hour ago is not news.
      if (sessionId && now - Date.parse(event.timestamp) < JOB_ENDED_FRESH_FOR) {
        const completed = payloadOf(event, 'job.completed');
        const failed = payloadOf(event, 'job.failed');
        effects.push({
          kind: 'jobEnded',
          sessionId,
          failed: Boolean(failed),
          text: completed?.summary ?? failed?.error ?? '',
        });
      }
      return effects;
    }

    default:
      return [];
  }
}

function snapshot(sessionId: string): Effect[] {
  return sessionId ? [{ kind: 'snapshot', sessionId }] : [];
}

function repository(sessionId: string): Effect[] {
  return sessionId ? [{ kind: 'repository', sessionId }] : [];
}

type Topics = {
  /** Every persisted event, for a view that keeps its own copy of a timeline. */
  event: Event;
  effect: Effect;
  activity: Activity;
  connection: boolean;
};

/**
 * Where the stream's news is published, and where every view listens.
 *
 * A plain emitter rather than the editor's EventEmitter, so the logic around
 * it runs in the tests too.
 */
export class EventBus {
  // Keyed by topic; the methods below are what tie each set to its value type.
  private readonly listeners = new Map<keyof Topics, Set<(value: never) => void>>();

  on<K extends keyof Topics>(topic: K, listener: (value: Topics[K]) => void): { dispose: () => void } {
    let set = this.listeners.get(topic);
    if (!set) {
      set = new Set();
      this.listeners.set(topic, set);
    }
    const listeners = set;
    listeners.add(listener);
    return { dispose: () => void listeners.delete(listener) };
  }

  emit<K extends keyof Topics>(topic: K, value: Topics[K]) {
    const set = this.listeners.get(topic) as Set<(value: Topics[K]) => void> | undefined;
    for (const listener of set ?? []) {
      // One view failing must not keep the event from the others.
      try {
        listener(value);
      } catch (error) {
        console.error(`threavia: a ${topic} listener failed`, error);
      }
    }
  }

  /** Publishes an event and everything it changes. */
  publish(event: Event, now?: number) {
    this.emit('event', event);
    for (const effect of effectsOf(event, now)) this.emit('effect', effect);
  }
}
