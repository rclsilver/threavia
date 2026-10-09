import { describe, expect, it } from 'vitest';

import type { BackendInstance, Event, Job } from '../src/api/types';
import {
  activeJob,
  applyToJobs,
  deliveriesFor,
  hasEarlier,
  mergeEvents,
  pendingJobs,
  sessionStatus,
} from '../src/conversation/state';

const event = (sequence: number, type = 'agent.message', jobId?: string): Event =>
  ({ id: `e${sequence}`, sequence, timestamp: `2026-10-09T10:00:${String(sequence).padStart(2, '0')}Z`, type, jobId, payload: {} }) as Event;

const job = (id: string, status: Job['status'], createdAt = '2026-10-09T10:00:00Z'): Job => ({
  id,
  runId: 'r',
  status,
  createdAt,
  updatedAt: createdAt,
});

describe('mergeEvents', () => {
  it('keeps each event once, in sequence order', () => {
    const merged = mergeEvents([event(3), event(5)], [event(5), event(4), event(1), event(6)]);
    expect(merged.map((e) => e.sequence)).toEqual([1, 3, 4, 5, 6]);
  });

  it('returns the same list when nothing is new', () => {
    const current = [event(1)];
    expect(mergeEvents(current, [event(1)])).toBe(current);
  });
});

describe('applyToJobs', () => {
  it('moves a Job along the timeline and learns Jobs it did not know', () => {
    let jobs = [job('a', 'QUEUED')];
    jobs = applyToJobs(jobs, event(1, 'job.started', 'a'));
    jobs = applyToJobs(jobs, event(2, 'job.created', 'b'));
    expect(jobs.map((j) => [j.id, j.status])).toEqual([
      ['a', 'RUNNING'],
      ['b', 'QUEUED'],
    ]);
    jobs = applyToJobs(jobs, event(3, 'job.completed', 'a'));
    expect(pendingJobs(jobs)).toEqual({ b: 'QUEUED' });
  });

  it('ignores what says nothing of a status', () => {
    const jobs = [job('a', 'RUNNING')];
    expect(applyToJobs(jobs, event(1, 'agent.message', 'a'))).toBe(jobs);
  });
});

describe('activeJob and deliveries', () => {
  const backend = (features: string[]) => ({ features }) as unknown as BackendInstance;

  it('is the oldest Job not finished', () => {
    const jobs = [job('b', 'QUEUED', '2026-10-09T11:00:00Z'), job('a', 'RUNNING', '2026-10-09T10:00:00Z'), job('c', 'COMPLETED')];
    expect(activeJob(jobs)?.id).toBe('a');
    expect(activeJob([job('c', 'COMPLETED')])).toBeUndefined();
  });

  it('offers only what the backend announced, and only while something runs', () => {
    expect(deliveriesFor(job('a', 'RUNNING'), backend(['JOB_INPUT_NOW']))).toEqual(['NOW']);
    expect(deliveriesFor(job('a', 'RUNNING'), backend(['JOB_INPUT_NOW', 'JOB_INPUT_NEXT']))).toEqual(['NEXT', 'NOW']);
    expect(deliveriesFor(job('a', 'QUEUED'), backend(['JOB_INPUT_NOW']))).toEqual([]);
    expect(deliveriesFor(job('a', 'CANCELLING'), backend(['JOB_INPUT_NOW']))).toEqual([]);
    expect(deliveriesFor(undefined, backend(['JOB_INPUT_NOW']))).toEqual([]);
    expect(deliveriesFor(job('a', 'RUNNING'), undefined)).toEqual([]);
  });
});

describe('sessionStatus', () => {
  it('puts what waits for the person first', () => {
    expect(sessionStatus(job('a', 'RUNNING'), 'validation')).toEqual({ label: 'Waiting for your approval', tone: 'waiting' });
    expect(sessionStatus(job('a', 'RUNNING'), undefined)).toEqual({ label: 'Running', tone: 'running' });
    expect(sessionStatus(job('a', 'WAITING_BACKEND'), undefined)).toEqual({ label: 'Waiting backend', tone: 'waiting' });
    expect(sessionStatus(undefined, undefined)).toEqual({ label: 'Idle', tone: 'idle' });
  });
});

describe('hasEarlier', () => {
  it('stops at the Session’s own first event, or once a short page came back', () => {
    expect(hasEarlier([event(10)], false)).toBe(true);
    expect(hasEarlier([event(1, 'session.created'), event(2)], false)).toBe(false);
    expect(hasEarlier([event(10)], true)).toBe(false);
    expect(hasEarlier([], false)).toBe(false);
  });
});
