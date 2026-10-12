import type { CoreClient } from '../api/client';
import type { EventBus } from '../api/events';
import type { Repository } from '../api/types';

/** A checkout outside Threavia produces no Session event. */
export const REPOSITORY_REFRESH_EVERY = 5_000;

export type RepositoryRead = { repo: Repository; error?: never } | { repo?: never; error: unknown };
type Listener = (value: RepositoryRead) => void;

/**
 * Reads repositories that are being shown, from the backend that holds them.
 * All views on a Core share each read. Polling is local git inspection; only
 * an explicit refresh from origin fetches the remote.
 */
export class RepositoryMonitor {
  private readonly watchers = new Map<string, Set<Listener>>();
  private readonly last = new Map<string, RepositoryRead>();
  private readonly loading = new Map<string, Promise<Repository>>();
  private focused = true;
  private disposed = false;
  private timer: ReturnType<typeof setInterval> | undefined;
  private readonly subscriptions: { dispose(): void }[];

  constructor(private readonly client: Pick<CoreClient, 'repository'>, bus: EventBus) {
    this.subscriptions = [
      bus.on('effect', (effect) => {
        if (effect.kind === 'repository') this.update(effect.sessionId);
      }),
      bus.on('connection', (connected) => {
        if (connected) this.updateAll();
      }),
    ];
  }

  watch(sessionId: string, listener: Listener): { dispose(): void } {
    let listeners = this.watchers.get(sessionId);
    if (!listeners) {
      listeners = new Set();
      this.watchers.set(sessionId, listeners);
    }
    listeners.add(listener);
    const previous = this.last.get(sessionId);
    if (previous) listener(previous);
    if (listeners.size === 1) this.update(sessionId);
    this.schedule();
    return { dispose: () => {
      listeners.delete(listener);
      if (listeners.size === 0) {
        this.watchers.delete(sessionId);
        this.last.delete(sessionId);
      }
      this.schedule();
    } };
  }

  setFocused(focused: boolean) {
    if (focused === this.focused) return;
    this.focused = focused;
    if (focused) this.updateAll();
    this.schedule();
  }

  refresh(sessionId: string, fetch = false): Promise<Repository> {
    if (this.disposed) return Promise.reject(new Error('Repository monitoring has stopped.'));
    const pending = this.loading.get(sessionId);
    if (pending) {
      // A fetch must run after the pending local read, so an older answer
      // cannot overwrite what was just fetched from origin.
      return fetch ? pending.catch(() => undefined).then(() => this.refresh(sessionId, true)) : pending;
    }
    const load = this.client.repository(sessionId, fetch)
      .then((repo) => { this.publish(sessionId, { repo }); return repo; })
      .catch((error: unknown) => { this.publish(sessionId, { error }); throw error; })
      .finally(() => this.loading.delete(sessionId));
    this.loading.set(sessionId, load);
    return load;
  }

  private publish(sessionId: string, value: RepositoryRead) {
    const listeners = this.watchers.get(sessionId);
    if (!listeners) return;
    const previous = this.last.get(sessionId);
    this.last.set(sessionId, value);
    // A read timestamp alone does not change the branch, counts or fetch
    // result. Avoid rebuilding headers and tooltips for an unchanged status.
    if (previous?.repo && value.repo &&
      JSON.stringify({ ...previous.repo, checkedAt: undefined }) === JSON.stringify({ ...value.repo, checkedAt: undefined })) return;
    for (const listener of listeners) listener(value);
  }

  private update(sessionId: string) {
    if (this.focused && this.watchers.has(sessionId)) void this.refresh(sessionId).catch(() => undefined);
  }

  private updateAll() {
    for (const sessionId of this.watchers.keys()) this.update(sessionId);
  }

  private schedule() {
    if (this.disposed || !this.focused || this.watchers.size === 0) {
      clearInterval(this.timer);
      this.timer = undefined;
    } else {
      this.timer ??= setInterval(() => this.updateAll(), REPOSITORY_REFRESH_EVERY);
    }
  }

  dispose() {
    this.disposed = true;
    clearInterval(this.timer);
    this.watchers.clear();
    this.last.clear();
    for (const subscription of this.subscriptions) subscription.dispose();
  }
}
