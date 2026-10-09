import type { CoreClient } from '../api/client';
import type { EventBus } from '../api/events';
import type { UserInputRequest, ValidationRequest } from '../api/types';
import type { Pending } from '../tree/model';

/**
 * What waits for the user, across every Project: the one copy the sidebar, the
 * status bar and the notifications read.
 *
 * Current state, never a count of unread events (spec section 6). It is re-read
 * from /me/attention whenever the stream says it may have changed, and a
 * resolution the stream names is dropped at once, so a request answered on the
 * phone disappears here without waiting for the round trip.
 */
export class AttentionStore {
  private pending: Pending = { validations: [], userInputs: [] };
  private loaded = false;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private inflight: Promise<void> | null = null;
  private again = false;
  private readonly listeners = new Set<(pending: Pending) => void>();
  private readonly client: CoreClient;

  constructor(client: CoreClient, bus: EventBus) {
    this.client = client;
    bus.on('effect', (effect) => {
      if (effect.kind === 'attention') this.schedule();
      if (effect.kind === 'resolved') this.drop(effect.validationId, effect.userInputId);
    });
  }

  get current(): Pending {
    return this.pending;
  }

  /** False until the first answer, so nothing reads an empty list as "nothing waits". */
  get ready(): boolean {
    return this.loaded;
  }

  onChange(listener: (pending: Pending) => void): { dispose: () => void } {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  }

  findValidation(id: string): ValidationRequest | undefined {
    return this.pending.validations.find((request) => request.id === id);
  }

  findUserInput(id: string): UserInputRequest | undefined {
    return this.pending.userInputs.find((request) => request.id === id);
  }

  /**
   * Re-reads soon rather than now: a replay after a reconnection can carry
   * dozens of requests and resolutions, and one read answers them all.
   */
  schedule(delay = 150) {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.refresh(), delay);
  }

  /** Re-reads now. Concurrent calls share one request, plus one more after it. */
  async refresh(): Promise<void> {
    if (this.inflight) {
      this.again = true;
      return this.inflight;
    }
    this.inflight = (async () => {
      try {
        this.set(await this.client.attention());
      } catch {
        // Kept as it was: a failed read says nothing about what waits, and
        // the stream will ask again on the next change.
      } finally {
        this.inflight = null;
      }
      if (this.again) {
        this.again = false;
        await this.refresh();
      }
    })();
    return this.inflight;
  }

  /** Forgets everything, for a sign-out or another Core. */
  reset() {
    clearTimeout(this.timer);
    this.loaded = false;
    this.set({ validations: [], userInputs: [] }, false);
  }

  /** Takes a request out at once, when this client or the stream resolved it. */
  drop(validationId?: string, userInputId?: string) {
    if (!validationId && !userInputId) return;
    const next = {
      validations: this.pending.validations.filter((request) => request.id !== validationId),
      userInputs: this.pending.userInputs.filter((request) => request.id !== userInputId),
    };
    if (
      next.validations.length !== this.pending.validations.length ||
      next.userInputs.length !== this.pending.userInputs.length
    ) {
      this.set(next, this.loaded);
    }
  }

  private set(pending: Pending, loaded = true) {
    this.pending = pending;
    this.loaded = loaded;
    for (const listener of this.listeners) listener(pending);
  }

  dispose() {
    clearTimeout(this.timer);
    this.listeners.clear();
  }
}
