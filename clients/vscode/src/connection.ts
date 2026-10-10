import * as vscode from 'vscode';

import type { CoreClient } from './api/client';
import type { EventBus } from './api/events';
import { EventStream } from './api/stream';
import type { AttentionStore } from './attention/store';
import type { CoreAuth } from './auth/core';
import { cursorKey } from './cores/settings';
import { isActive, type Presence } from './presence';
import type { CoreState } from './tree/model';

/** How often a Core that did not answer is asked again. */
const RETRY_AFTER = 30_000;

/**
 * Where the extension stands with one Core, and what follows from it.
 *
 * Connecting, unreachable and signed out each say so in the sidebar; ready is
 * the only state with a stream, Sessions to list and notifications. Every
 * Core has its own, so one that does not answer leaves the others alone.
 */
export class Connection implements vscode.Disposable {
  private current: CoreState = 'connecting';
  private stream: EventStream | null = null;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private saveTimer: ReturnType<typeof setTimeout> | undefined;
  private wasReady = false;
  private generation = 0;
  private starting: Promise<void> = Promise.resolve();
  private readonly subscription: vscode.Disposable;
  private readonly changed = new vscode.EventEmitter<CoreState>();
  /** Fired on every change of state, and when the sign-in changed within one. */
  readonly onDidChange = this.changed.event;

  constructor(
    private readonly coreId: string,
    private readonly state: vscode.Memento,
    private readonly client: CoreClient,
    private readonly auth: CoreAuth,
    private readonly bus: EventBus,
    private readonly attention: AttentionStore,
    private readonly presence: Presence,
  ) {
    // Only a change of whether requests can be sent matters here: a renewed
    // token changes nothing for the stream already open.
    this.subscription = auth.onDidChange(() => {
      if (this.auth.ready !== this.wasReady) this.apply();
      else this.publish(this.current);
    });
  }

  get status(): CoreState {
    return this.current;
  }

  /** The start under way, for a command that needs to know how it ended. */
  get settled(): Promise<void> {
    return this.starting;
  }

  /** (Re)connects from scratch. */
  start(): Promise<void> {
    this.starting = this.doStart();
    return this.starting;
  }

  private async doStart(): Promise<void> {
    const generation = ++this.generation;
    clearTimeout(this.retry);
    this.closeStream();
    this.wasReady = false;
    this.attention.reset();
    this.publish('connecting');

    try {
      const config = await this.client.authConfig();
      if (generation !== this.generation) return;
      await this.auth.load(config);
    } catch {
      if (generation !== this.generation) return;
      this.auth.forget();
      this.retry = setTimeout(() => void this.start(), RETRY_AFTER);
      return this.publish('unreachable');
    }
    if (generation === this.generation) this.apply();
  }

  /**
   * Forgets where the stream was: the Core moved to another address, whose
   * sequence has nothing to do with the old one's.
   */
  async forgetCursor() {
    clearTimeout(this.saveTimer);
    await this.state.update(cursorKey(this.coreId), undefined);
  }

  /** Follows whether there is a credential: stream on, or off. */
  private apply() {
    this.wasReady = this.auth.ready;
    if (!this.auth.ready) {
      this.closeStream();
      this.attention.reset();
      this.publish('signedOut');
      return;
    }
    this.publish('ready');
    void this.attention.refresh();
    this.openStream();
  }

  /** Opens the stream from the saved cursor, or from where a refused one stopped. */
  private openStream(from?: number) {
    this.closeStream();
    const key = cursorKey(this.coreId);
    const stream = new EventStream({
      client: this.client,
      isActive,
      presenceSent: (active) => this.presence.sentWithStream(active),
      initialCursor: from ?? this.state.get<number>(key, 0),
    });
    this.stream = stream;
    stream.open({
      onEvent: (event) => this.bus.publish(event),
      onActivity: (activity) => this.bus.emit('activity', activity),
      onConnection: (connected) => this.bus.emit('connection', connected),
      // Saved at most every few seconds: a replay moves the cursor hundreds of
      // times, and only where it ends matters.
      onCursor: (cursor) => {
        clearTimeout(this.saveTimer);
        this.saveTimer = setTimeout(() => void this.state.update(key, cursor), 2000);
      },
      // Core refused the credential. One renewal is worth trying; otherwise
      // the auth layer drops it and the sidebar asks to sign in again.
      onUnauthorized: () => {
        void this.auth.recover().then((renewed) => {
          if (renewed && this.stream === stream) this.openStream(stream.position);
        });
      },
    });
  }

  private closeStream() {
    this.stream?.close();
    this.stream = null;
    this.presence.stop();
  }

  private publish(state: CoreState) {
    this.current = state;
    this.changed.fire(state);
  }

  dispose() {
    // Stops reconnecting for good: a removed Core must not come back by itself.
    this.generation++;
    clearTimeout(this.retry);
    clearTimeout(this.saveTimer);
    this.closeStream();
    this.subscription.dispose();
    this.changed.dispose();
  }
}
