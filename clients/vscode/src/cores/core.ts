import * as vscode from 'vscode';

import { CoreClient, type Identity } from '../api/client';
import { EventBus } from '../api/events';
import type { BackendInstance } from '../api/types';
import { Answers } from '../attention/answer';
import { AttentionStore } from '../attention/store';
import { CoreAuth, type Callbacks } from '../auth/core';
import { Connection } from '../connection';
import { Artifacts } from '../conversation/artifacts';
import type { VirtualDocuments } from '../diff/documents';
import { Presence } from '../presence';
import { withCore, type CoreState } from '../tree/model';
import { hostOf, type CoreSetting } from './settings';

/** What every Core shares with the others. */
export interface CoreDeps {
  context: vscode.ExtensionContext;
  identity: Identity;
  callbacks: Callbacks;
  /** Whether there is more than one Core, read when a message is worded. */
  several: () => boolean;
  /** Where an Artifact's text opens, a document shared by every Core. */
  artifactDocuments: VirtualDocuments;
}

/**
 * One Core and everything the extension holds for it: its client, sign-in,
 * stream and cursor, what waits there, its presence. Each Core has its own
 * event bus, so an event of one never reaches a view of another; what follows
 * every Core listens through the registry (see registry.ts).
 */
export class Core implements vscode.Disposable {
  readonly bus = new EventBus();
  readonly auth: CoreAuth;
  readonly client: CoreClient;
  readonly attention: AttentionStore;
  readonly answers: Answers;
  readonly presence: Presence;
  readonly connection: Connection;
  readonly artifacts: Artifacts;
  private backendList: { at: number; list: Promise<BackendInstance[]> } | undefined;

  constructor(
    public setting: CoreSetting,
    private readonly deps: CoreDeps,
  ) {
    const { context, identity } = deps;
    // The client and the auth layer need each other: requests carry the
    // credential, and a basic sign-in is checked with a request. The auth layer
    // reaches the client lazily, only once a sign-in runs.
    this.auth = new CoreAuth(
      context.secrets,
      { id: setting.id, name: () => this.name, several: deps.several },
      () => this.client,
      deps.callbacks,
    );
    this.client = new CoreClient({
      baseUrl: () => this.setting.url,
      identity,
      authorization: () => this.auth.authorization(),
      onUnauthorized: () => this.auth.recover(),
    });
    this.attention = new AttentionStore(this.client, this.bus);
    this.answers = new Answers(this.client, this.attention);
    this.presence = new Presence(this.client);
    this.connection = new Connection(
      setting.id,
      context.globalState,
      this.client,
      this.auth,
      this.bus,
      this.attention,
      this.presence,
    );
    this.artifacts = new Artifacts(this.client, deps.artifactDocuments, setting.id);
  }

  get id(): string {
    return this.setting.id;
  }

  get name(): string {
    return this.setting.name;
  }

  get url(): string {
    return this.setting.url;
  }

  get host(): string {
    return hostOf(this.setting.url);
  }

  /** The Project this Core's setting names, or nothing. */
  get projectSetting(): string {
    return this.setting.project ?? '';
  }

  get state(): CoreState {
    return this.connection.status;
  }

  get ready(): boolean {
    return this.connection.status === 'ready';
  }

  /** Says which Core a message is about, when there is another it could be. */
  label(text: string): string {
    return withCore(text, this.name, this.deps.several());
  }

  /**
   * The backends, shared by every panel of this Core and kept a minute: a
   * backend's name and features change rarely, and ten panels restored at
   * start ask once.
   */
  backends(): Promise<BackendInstance[]> {
    if (!this.backendList || Date.now() - this.backendList.at > 60_000) {
      const list = this.client.backends();
      list.catch(() => (this.backendList = undefined));
      this.backendList = { at: Date.now(), list };
    }
    return this.backendList.list;
  }

  dispose() {
    this.connection.dispose();
    this.presence.dispose();
    this.attention.dispose();
    this.auth.dispose();
  }
}
