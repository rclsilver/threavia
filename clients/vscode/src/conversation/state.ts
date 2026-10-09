import type { BackendInstance, Event, Job, JobStatus, Run } from '../api/types';
import { humanise } from './format';

/**
 * A Session's live state, kept from its snapshot and the stream, without an
 * editor: which Jobs are going, what a message can do to them, and what the
 * header says.
 */

export const FINISHED: ReadonlySet<string> = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);

/**
 * Adds events to a timeline: each one once, by its global sequence, in
 * sequence order. A snapshot read again and the stream overlap, and a page of
 * history lands before what is already there; all of them go through here.
 */
export function mergeEvents(current: readonly Event[], incoming: readonly Event[]): Event[] {
  if (incoming.length === 0) return current as Event[];
  const known = new Set(current.map((event) => event.sequence));
  const fresh = incoming.filter((event) => !known.has(event.sequence));
  if (fresh.length === 0) return current as Event[];
  const last = current.at(-1)?.sequence ?? -Infinity;
  // The usual case, an event from the stream after the last one, needs no sort.
  if (fresh.every((event) => event.sequence > last)) {
    return [...current, ...fresh.sort((a, b) => a.sequence - b.sequence)];
  }
  return [...current, ...fresh].sort((a, b) => a.sequence - b.sequence);
}

/**
 * Keeps the Job list current from the timeline, as the web client does: the
 * status is derived from the events rather than read again, the timeline
 * already being what Core persisted.
 */
export function applyToJobs(jobs: readonly Job[], event: Event): Job[] {
  const status = jobStatusFor(event.type);
  if (!status || !event.jobId) return jobs as Job[];
  if (!jobs.some((job) => job.id === event.jobId)) {
    // A Job created by another client, or by this one before the snapshot was
    // taken. The identifiers are all the list needs to show it as live.
    return [
      ...jobs,
      { id: event.jobId, runId: event.runId ?? '', status, createdAt: event.timestamp, updatedAt: event.timestamp },
    ];
  }
  return jobs.map((job) => (job.id === event.jobId ? { ...job, status, updatedAt: event.timestamp } : job));
}

function jobStatusFor(type: string): JobStatus | null {
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
      // accepted, and the panel reads the snapshot again to show it.
      return null;
  }
}

/**
 * The oldest Job not finished: the one holding the Session, which everything
 * else is queued behind, and the one Stop and Escape stop.
 */
export function activeJob(jobs: readonly Job[]): Job | undefined {
  return [...jobs]
    .filter((job) => !FINISHED.has(job.status))
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt))[0];
}

/** Every Job still going somewhere, so a message can offer a stop on its own. */
export function pendingJobs(jobs: readonly Job[]): Record<string, JobStatus> {
  const pending: Record<string, JobStatus> = {};
  for (const job of jobs) if (!FINISHED.has(job.status)) pending[job.id] = job.status;
  return pending;
}

/** The Run the Session works in now: the last one created. */
export function currentRun(runs: readonly Run[]): Run | undefined {
  return [...runs].sort((a, b) => a.createdAt.localeCompare(b.createdAt)).at(-1);
}

/**
 * How a message can reach the running work besides being queued. Only while
 * something runs to receive it, and only the ways the backend holding the
 * Session announced.
 */
export function deliveriesFor(active: Job | undefined, backend: BackendInstance | undefined): ('NEXT' | 'NOW')[] {
  if (!active || ['QUEUED', 'CANCELLING', 'WAITING_BACKEND'].includes(active.status)) return [];
  const features: string[] = backend?.features ?? [];
  return (['NEXT', 'NOW'] as const).filter((delivery) => features.includes(`JOB_INPUT_${delivery}`));
}

export type StateTone = 'idle' | 'running' | 'waiting';

/**
 * The state the header shows. What waits for the person outranks the Job's
 * own status, since it is the one thing they have to act on.
 */
export function sessionStatus(
  active: Job | undefined,
  waiting: 'validation' | 'input' | undefined,
): { label: string; tone: StateTone } {
  if (waiting === 'validation') return { label: 'Waiting for your approval', tone: 'waiting' };
  if (waiting === 'input') return { label: 'Waiting for your answer', tone: 'waiting' };
  if (!active) return { label: 'Idle', tone: 'idle' };
  const label = humanise(active.status);
  return {
    label: label.charAt(0).toUpperCase() + label.slice(1),
    tone: active.status === 'RUNNING' ? 'running' : 'waiting',
  };
}

/**
 * Whether history goes further back than what is loaded: the Session's own
 * first event is the start of it.
 */
export function hasEarlier(events: readonly Event[], exhausted: boolean): boolean {
  const first = events[0];
  return !exhausted && Boolean(first) && first.type !== 'session.created';
}
