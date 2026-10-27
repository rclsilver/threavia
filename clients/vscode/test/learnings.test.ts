import { describe, expect, it, vi } from 'vitest';
import { isLearningRequest, learningPrompt, learningSource, requestLearningCapture } from '../../shared/learnings';

const job = (id: string, status: string, createdAt = '2026-10-27T10:00:00Z') => ({ id, status, createdAt });
const result = { session: { id: 'session', status: 'ACTIVE' }, jobs: [job('source', 'COMPLETED')] };

describe('deliberate learning capture', () => {
  it('folds only its complete generated request, without hiding ordinary or appended text', () => {
    const prompt = learningPrompt('source');
    expect(isLearningRequest(prompt)).toBe(true);
    expect(isLearningRequest(prompt + '\nDo something else')).toBe(false);
    expect(isLearningRequest(prompt.split('\n').slice(0, 2).join('\n'))).toBe(false);
    expect(isLearningRequest('Capture learnings from this resolution')).toBe(false);
  });
  it('uses the latest successful result without reordering the snapshot', () => {
    const jobs = [job('new', 'COMPLETED', '2026-10-27T11:00:00Z'), job('old', 'COMPLETED')];
    expect(learningSource({ ...result, jobs })?.id).toBe('new');
    expect(jobs[0].id).toBe('new');
    expect(learningSource({ ...result, jobs: [] })).toBeUndefined();
    expect(learningSource({ ...result, session: { ...result.session, status: 'ARCHIVED' } })).toBeUndefined();
  });

  it.each(['RUNNING', 'QUEUED', 'WAITING_BACKEND', 'WAITING_USER_INPUT', 'WAITING_VALIDATION', 'CANCELLING'])(
    'does not treat a result as settled while another job is %s', status => {
      expect(learningSource({ ...result, jobs: [job('other', status), job('source', 'COMPLETED', '2026-10-27T11:00:00Z')] })).toBeUndefined();
    });

  it.each(['FAILED', 'CANCELLED'])('does not offer a %s result as a resolution', status => {
    expect(learningSource({ ...result, jobs: [...result.jobs, job('latest', status, '2026-10-27T11:00:00Z')] })).toBeUndefined();
  });

  it('checks a fresh snapshot after confirmation and preserves one cross-client retry key', async () => {
    const send = vi.fn().mockResolvedValue({ id: 'capture' });
    await expect(requestLearningCapture('source', () => Promise.resolve(result), send)).resolves.toEqual({ id: 'capture' });
    await requestLearningCapture('source', () => Promise.resolve(result), send);
    expect(send.mock.calls[0]).toEqual(send.mock.calls[1]);
    expect(send).toHaveBeenCalledWith(learningPrompt('source'), 'capture-learnings:source');
    send.mockClear();
    await expect(requestLearningCapture('source', () => Promise.resolve({ ...result, jobs: [...result.jobs, job('new', 'RUNNING')] }), send)).rejects.toThrow('session changed');
    expect(send).not.toHaveBeenCalled();
    await expect(requestLearningCapture('source', () => Promise.reject(new Error('Offline')), send)).rejects.toThrow('Offline');
    expect(send).not.toHaveBeenCalled();
  });
});
