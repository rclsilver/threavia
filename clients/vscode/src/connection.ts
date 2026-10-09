import * as vscode from 'vscode';

import type { CoreClient } from './api/client';
import type { EventBus } from './api/events';
import { EventStream } from './api/stream';
import type { AttentionStore } from './attention/store';
import type { ThreaviaAuth } from './auth/provider';
import { coreUrl } from './config';
import { isActive, type Presence } from './presence';
/** A view that shows Core's data only while Core can be asked. */
export interface Switchable {
  setEnabled(enabled: boolean): void;
}

export type State = 'unconfigured' | 'unreachable' | 'signedOut' | 'ready';

/** How often a Core that did not answer is asked again. */
const RETRY_AFTER = 30_000;

/**
 * Where the extension stands with Core, and what follows from it.
 *
 * Unconfigured, unreachable and signed out each have their own welcome in the
 * sidebar (the `threavia.state` context key); ready is the only state with a
 * stream, a sidebar full of Sessions and notifications.
 */
export class Connection implements vscode.Disposable {
  private current: State = 'unconfigured';
  private stream: EventStream | null = null;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private saveTimer: ReturnType<typeof setTimeout> | undefined;
  private wasReady = false;
  private generation = 0;
  private readonly subscription: vscode.Disposable;

  constructor(
    private readonly state: vscode.Memento,
    private readonly client: CoreClient,
    private readonly auth: ThreaviaAuth,
    private readonly bus: EventBus,
    private readonly attention: AttentionStore,
    private readonly views: Switchable[],
    private readonly presence: Presence,
  ) {
    // Only a change of whether requests can be sent matters here: a renewed
    // token changes nothing for the stream already open.
    this.subscription = auth.onDidChange(() => {
      if (this.auth.ready !== this.wasReady) this.apply();
      else void this.publish(this.current);
    });
  }

  get status(): State {
    return this.current;
  }

  /** (Re)connects to the Core the settings name, from scratch. */
  async start(): Promise<void> {
    const generation = ++this.generation;
    clearTimeout(this.retry);
    this.closeStream();
    this.wasReady = false;
    this.attention.reset();
    for (const view of this.views) view.setEnabled(false);

    if (!coreUrl()) {
      this.auth.forget();
      return this.publish('unconfigured');
    }
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

  /** Follows whether there is a credential: views and stream on, or off. */
  private apply() {
    this.wasReady = this.auth.ready;
    if (!this.auth.ready) {
      this.closeStream();
      this.attention.reset();
      for (const view of this.views) view.setEnabled(false);
      void this.publish('signedOut');
      return;
    }
    void this.publish('ready');
    for (const view of this.views) view.setEnabled(true);
    void this.attention.refresh();
    this.openStream();
  }

  /** Opens the stream from the saved cursor, or from where a refused one stopped. */
  private openStream(from?: number) {
    this.closeStream();
    const key = `threavia.cursor:${coreUrl()}`;
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

  private async publish(state: State) {
    this.current = state;
    await vscode.commands.executeCommand('setContext', 'threavia.state', state);
    await vscode.commands.executeCommand('setContext', 'threavia.signedIn', this.auth.signedIn);
  }

  dispose() {
    clearTimeout(this.retry);
    clearTimeout(this.saveTimer);
    this.closeStream();
    this.subscription.dispose();
  }
}
