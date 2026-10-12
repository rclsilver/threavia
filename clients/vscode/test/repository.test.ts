import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { CoreClient } from '../src/api/client';
import { EventBus } from '../src/api/events';
import type { Repository } from '../src/api/types';
import { RepositoryMonitor, REPOSITORY_REFRESH_EVERY } from '../src/conversation/repository';

const repo = (branch: string): Repository => ({
  directory: '/work/project', tracked: true, branch, ahead: 0, behind: 0, upstreamGone: false,
  staged: 0, unstaged: 0, untracked: 0, conflicted: 0, checkedAt: '2026-10-12T12:00:00Z',
});
const monitors: RepositoryMonitor[] = [];
function setup() {
  const read = vi.fn<CoreClient['repository']>().mockResolvedValue(repo('main'));
  const bus = new EventBus();
  const monitor = new RepositoryMonitor({ repository: read }, bus);
  monitors.push(monitor);
  return { monitor, read, bus };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const monitor of monitors.splice(0)) monitor.dispose();
  vi.useRealTimers();
});

describe('repository refresh', () => {
  it('shows a terminal checkout in all views with one local read every five seconds', async () => {
    const { monitor, read } = setup();
    const header = vi.fn();
    const sidebar = vi.fn();
    monitor.watch('s1', header);
    monitor.watch('s1', sidebar);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(1);
    expect(header).toHaveBeenLastCalledWith({ repo: repo('main') });
    read.mockResolvedValue(repo('feature'));
    await vi.advanceTimersByTimeAsync(REPOSITORY_REFRESH_EVERY);
    expect(read).toHaveBeenCalledTimes(2);
    expect(header).toHaveBeenLastCalledWith({ repo: repo('feature') });
    expect(sidebar).toHaveBeenLastCalledWith({ repo: repo('feature') });
    expect(read.mock.calls).toEqual([['s1', false], ['s1', false]]);
    read.mockResolvedValue({ ...repo('feature'), checkedAt: '2026-10-12T12:00:10Z' });
    await vi.advanceTimersByTimeAsync(REPOSITORY_REFRESH_EVERY);
    expect(read).toHaveBeenCalledTimes(3);
    expect(header).toHaveBeenCalledTimes(2);
    expect(sidebar).toHaveBeenCalledTimes(2);
  });

  it('pauses when the window loses focus and refreshes immediately on return', async () => {
    const { monitor, read } = setup();
    monitor.setFocused(false);
    const view = vi.fn();
    monitor.watch('s1', view);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(read).not.toHaveBeenCalled();
    monitor.setFocused(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledOnce();
    monitor.setFocused(false);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(read).toHaveBeenCalledOnce();
    read.mockResolvedValue(repo('other'));
    monitor.setFocused(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(view).toHaveBeenLastCalledWith({ repo: repo('other') });
  });

  it('stops reading hidden sessions and stops the timer after the last view closes', async () => {
    const { monitor, read } = setup();
    const first = monitor.watch('s1', vi.fn());
    const second = monitor.watch('s1', vi.fn());
    const other = monitor.watch('s2', vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    read.mockClear();
    first.dispose();
    other.dispose();
    await vi.advanceTimersByTimeAsync(REPOSITORY_REFRESH_EVERY);
    expect(read.mock.calls).toEqual([['s1', false]]);
    second.dispose();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(read).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not stack requests when the backend takes longer than the polling interval', async () => {
    const { monitor, read } = setup();
    let finish!: (value: Repository) => void;
    read.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    const view = vi.fn();
    monitor.watch('s1', view);
    await vi.advanceTimersByTimeAsync(15_000);
    expect(read).toHaveBeenCalledOnce();
    finish(repo('feature'));
    await vi.advanceTimersByTimeAsync(0);
    expect(view).toHaveBeenLastCalledWith({ repo: repo('feature') });
    await vi.advanceTimersByTimeAsync(REPOSITORY_REFRESH_EVERY);
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('queues an explicit origin fetch after a pending read and updates the header too', async () => {
    const { monitor, read } = setup();
    let finish!: (value: Repository) => void;
    read.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    const header = vi.fn();
    monitor.watch('s1', header);
    const fetched = { ...repo('feature'), behind: 3 };
    read.mockResolvedValue(fetched);
    const fetch = monitor.refresh('s1', true);
    expect(read).toHaveBeenCalledOnce();
    finish(repo('main'));
    await expect(fetch).resolves.toEqual(fetched);
    expect(read.mock.calls).toEqual([['s1', false], ['s1', true]]);
    expect(header).toHaveBeenLastCalledWith({ repo: fetched });
  });

  it('retries failed reads and refreshes watched sessions on events and reconnection', async () => {
    const { monitor, read, bus } = setup();
    const failure = new Error('offline');
    read.mockRejectedValueOnce(failure);
    const view = vi.fn();
    monitor.watch('s1', view);
    await vi.advanceTimersByTimeAsync(0);
    expect(view).toHaveBeenLastCalledWith({ error: failure });
    await vi.advanceTimersByTimeAsync(REPOSITORY_REFRESH_EVERY);
    expect(view).toHaveBeenLastCalledWith({ repo: repo('main') });
    bus.emit('effect', { kind: 'repository', sessionId: 'other' });
    bus.emit('effect', { kind: 'repository', sessionId: 's1' });
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(3);
    bus.emit('connection', true);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(4);
    monitor.dispose();
    bus.emit('connection', true);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(read).toHaveBeenCalledTimes(4);
  });

  it('keeps identical session ids on different Cores independent', async () => {
    const first = setup();
    const second = setup();
    first.read.mockResolvedValue(repo('first'));
    second.read.mockResolvedValue(repo('second'));
    const firstView = vi.fn();
    const secondView = vi.fn();
    first.monitor.watch('s1', firstView);
    second.monitor.watch('s1', secondView);
    await vi.advanceTimersByTimeAsync(0);
    expect(firstView).toHaveBeenLastCalledWith({ repo: repo('first') });
    expect(secondView).toHaveBeenLastCalledWith({ repo: repo('second') });
  });
});
