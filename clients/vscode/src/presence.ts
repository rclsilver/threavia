import * as vscode from 'vscode';

import type { CoreClient } from './api/client';

/**
 * Whether the person is looking at this editor right now.
 *
 * Core uses it to decide whether a phone should ring: while the person is
 * active on one of their clients, that screen already shows what happened. The
 * editor says focused when one of its windows has the focus, and active when
 * it was used in the last moments; both have to hold.
 */
export function isActive(): boolean {
  const state = vscode.window.state;
  return state.focused && (state.active ?? true);
}

/**
 * Follows the window state and tells Core when it changes. The stream declares
 * presence when it opens, so the first report after that is only a change.
 */
export class Presence implements vscode.Disposable {
  private reported: boolean | null = null;
  private readonly subscription: vscode.Disposable;
  private readonly client: CoreClient;
  private enabled = false;

  constructor(client: CoreClient) {
    this.client = client;
    this.subscription = vscode.window.onDidChangeWindowState(() => this.report());
  }

  /** Told once a stream opened with this presence in its header. */
  sentWithStream(active: boolean) {
    this.reported = active;
    this.enabled = true;
  }

  /** Stops reporting: there is no stream, so Core has no client to update. */
  stop() {
    this.enabled = false;
    this.reported = null;
  }

  private report() {
    if (!this.enabled) return;
    const active = isActive();
    if (active === this.reported) return;
    this.reported = active;
    // Best effort: a missed report costs one notification too many or too
    // few, and the next change sends the truth again.
    this.client.presence(active).catch(() => {
      this.reported = null;
    });
  }

  dispose() {
    this.subscription.dispose();
  }
}
