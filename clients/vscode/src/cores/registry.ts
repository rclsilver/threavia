import * as vscode from 'vscode';

import type { Identity } from '../api/client';
import type { Activity } from '../api/stream';
import type { Effect } from '../api/events';
import type { Event } from '../api/types';
import { AttentionLedgers, type SavedLedgers } from '../attention/ledger';
import { Accounts, PROVIDER_ID } from '../auth/accounts';
import { coreSettings, legacySettings, saveCores } from '../config';
import type { VirtualDocuments } from '../diff/documents';
import { coreIcon, overallState } from '../tree/model';
import { Core } from './core';
import { legacyKeys, migrateLegacy, type CoreSetting } from './settings';

const LEDGERS_KEY = 'threavia.announced.byCore';
/** Where an earlier version kept its one ledger. */
const LEGACY_LEDGER_KEY = 'threavia.announced';

type Topics = { event: Event; effect: Effect; activity: Activity; connection: boolean };
type Listener<K extends keyof Topics> = (core: Core, value: Topics[K]) => void;

/**
 * Every configured Core, connected at once.
 *
 * The `threavia.cores` setting is the truth: the registry follows it, whoever
 * edits it — a command here, another window, the settings file — creating a
 * Core's runtime when it appears, reconnecting one whose address changed and
 * forgetting one that was removed, sign-in and cursor with it.
 *
 * What follows every Core (the sidebar, the status bar, the views) listens
 * here, and is told which Core each event came from.
 */
export class Cores implements vscode.Disposable {
  private readonly runtimes = new Map<string, Core>();
  private order: string[] = [];
  private starting: Promise<void> | undefined;
  /** A Core moving to another address, until it reconnected. */
  private readonly moves = new Map<string, Promise<void>>();
  private readonly listeners = new Map<keyof Topics, Set<Listener<never>>>();
  private readonly attentionListeners = new Set<(core: Core) => void>();
  private readonly changed = new vscode.EventEmitter<Core | undefined>();
  /** A Core was added, removed, renamed, or changed state; undefined when the list changed. */
  readonly onDidChange = this.changed.event;
  private readonly removed = new vscode.EventEmitter<string>();
  /** A Core was removed: what was open on it closes. */
  readonly onDidRemove = this.removed.event;
  readonly accounts: Accounts;
  readonly ledgers: AttentionLedgers;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly identity: Identity,
    private readonly artifactDocuments: VirtualDocuments,
  ) {
    this.accounts = new Accounts(
      () => this.list(),
      () => this.pick('Sign in to Threavia', 'Which Core?'),
    );
    this.ledgers = new AttentionLedgers(context.globalState.get<SavedLedgers>(LEDGERS_KEY, {}));
    this.disposables.push(
      this.accounts,
      vscode.authentication.registerAuthenticationProvider(PROVIDER_ID, 'Threavia', this.accounts, {
        supportsMultipleAccounts: true,
      }),
      vscode.window.registerUriHandler(this.accounts),
      vscode.workspace.onDidChangeConfiguration((event) => {
        if (event.affectsConfiguration('threavia.cores')) this.sync();
      }),
    );
  }

  /** Moves an earlier version's one Core into the list, then connects to every Core. */
  start(): Promise<void> {
    this.starting ??= (async () => {
      try {
        await this.migrate();
      } catch (error) {
        // The old settings stay where they were; the list is read as it is.
        console.error('threavia: the settings of an earlier version were not moved', error);
      }
      this.sync();
      await this.publishContext();
    })();
    return this.starting;
  }

  /**
   * Resolves once the Cores were read: the editor may restore a conversation
   * before that, and it has to know which Core it is on.
   */
  get started(): Promise<void> {
    return this.start();
  }

  private async migrate() {
    const migrated = migrateLegacy(legacySettings());
    if (!migrated) return;
    const { secrets, globalState } = this.context;
    // The sign-in and the cursor were keyed by the URL; they move to the id,
    // so nobody has to sign in again after the update.
    const [credential, cursor] = legacyKeys(migrated.url, migrated.id);
    const secret = await secrets.get(credential.from);
    if (secret) {
      await secrets.store(credential.to, secret);
      await secrets.delete(credential.from);
    }
    const position = globalState.get<number>(cursor.from);
    if (position !== undefined) {
      await globalState.update(cursor.to, position);
      await globalState.update(cursor.from, undefined);
    }
    const announced = globalState.get<string[]>(LEGACY_LEDGER_KEY);
    if (announced) {
      this.ledgers.adopt(migrated.id, announced);
      await this.saveLedgers();
      await globalState.update(LEGACY_LEDGER_KEY, undefined);
    }
    await saveCores([migrated]);
  }

  /** Follows the setting: Cores appear, change, or go. */
  private sync() {
    const { cores, changed } = coreSettings();
    // An id generated for a hand-written entry has to be the same next time,
    // so it is written first and the change it makes brings the list back
    // here; were it used now, the next read would generate another.
    if (changed) {
      saveCores(cores).then(undefined, () => this.apply(cores));
      return;
    }
    this.apply(cores);
  }

  private apply(cores: CoreSetting[]) {
    const wanted = new Set(cores.map((core) => core.id));
    for (const [id, core] of this.runtimes) {
      if (!wanted.has(id)) void this.retire(core);
    }
    let listChanged = cores.length !== this.order.length || cores.some((core, index) => core.id !== this.order[index]);
    this.order = cores.map((core) => core.id);

    for (const setting of cores) {
      const existing = this.runtimes.get(setting.id);
      if (!existing) {
        this.create(setting);
        listChanged = true;
        continue;
      }
      const before = existing.setting;
      existing.setting = setting;
      if (before.url !== setting.url) {
        this.moves.set(
          existing.id,
          this.moved(existing).finally(() => this.moves.delete(existing.id)),
        );
      } else if (before.name !== setting.name || before.project !== setting.project) {
        // The Core's name is part of its account's label when there are several.
        const label = existing.auth.accountLabel;
        if (label) this.accounts.changed(existing, label, label);
        this.changed.fire(existing);
      }
    }
    if (listChanged) {
      this.changed.fire(undefined);
      void this.publishContext();
    }
  }

  private create(setting: CoreSetting): Core {
    const core = new Core(setting, {
      context: this.context,
      identity: this.identity,
      callbacks: this.accounts,
      several: () => this.several,
      artifactDocuments: this.artifactDocuments,
    });
    this.runtimes.set(setting.id, core);
    core.auth.onAccount = (before, after) => this.accounts.changed(core, before, after);
    const forward = <K extends keyof Topics>(topic: K) =>
      core.bus.on(topic, (value) => {
        for (const listener of (this.listeners.get(topic) ?? []) as Set<Listener<K>>) {
          // One view failing must not keep the event from the others.
          try {
            listener(core, value);
          } catch (error) {
            console.error(`threavia: a ${topic} listener failed`, error);
          }
        }
      });
    forward('event');
    forward('effect');
    forward('activity');
    forward('connection');
    core.attention.onChange(() => {
      for (const listener of this.attentionListeners) listener(core);
    });
    core.connection.onDidChange(() => {
      this.changed.fire(core);
      void this.publishContext();
    });
    void core.connection.start();
    return core;
  }

  /**
   * The Core moved to another address. What was kept for the old one — its
   * sign-in, its cursor, what was announced — means nothing to the new one.
   */
  private async moved(core: Core) {
    await core.auth.signOut();
    await core.connection.forgetCursor();
    this.ledgers.forget(core.id);
    await this.saveLedgers();
    await core.connection.start();
  }

  /** A Core was removed: its sign-in and everything kept for it are forgotten. */
  private async retire(core: Core) {
    this.runtimes.delete(core.id);
    await core.auth.signOut();
    await core.connection.forgetCursor();
    core.dispose();
    this.ledgers.forget(core.id);
    await this.saveLedgers();
    this.removed.fire(core.id);
  }

  saveLedgers(): Thenable<void> {
    return this.context.globalState.update(LEDGERS_KEY, this.ledgers.saved);
  }

  // ------------------------------------------------------------- reading

  /** The Cores, in the order the setting lists them. */
  list(): Core[] {
    return this.order.flatMap((id) => {
      const core = this.runtimes.get(id);
      return core ? [core] : [];
    });
  }

  get(id: string | undefined): Core | undefined {
    return id ? this.runtimes.get(id) : undefined;
  }

  get ids(): string[] {
    return [...this.order];
  }

  get several(): boolean {
    return this.order.length > 1;
  }

  /** The Cores that can be asked now. */
  ready(): Core[] {
    return this.list().filter((core) => core.ready);
  }

  /** Listens to a topic of every Core's bus, told which Core it came from. */
  on<K extends keyof Topics>(topic: K, listener: Listener<K>): vscode.Disposable {
    let set = this.listeners.get(topic);
    if (!set) {
      set = new Set();
      this.listeners.set(topic, set);
    }
    const listeners = set;
    listeners.add(listener as Listener<never>);
    return { dispose: () => void listeners.delete(listener as Listener<never>) };
  }

  /** Told when what waits changed on any Core. */
  onAttention(listener: (core: Core) => void): vscode.Disposable {
    this.attentionListeners.add(listener);
    return { dispose: () => void this.attentionListeners.delete(listener) };
  }

  /**
   * Which Core, asked only when there is a choice: none when there is no
   * Core, the only one without asking.
   */
  async pick(title: string, placeHolder: string, cores: Core[] = this.list()): Promise<Core | undefined> {
    if (cores.length <= 1) return cores[0];
    const picked = await vscode.window.showQuickPick(
      cores.map((core) => ({
        label: core.name,
        description: core.host,
        detail: core.ready ? core.auth.accountLabel : coreIcon(core.state).label,
        core,
      })),
      { title, placeHolder, ignoreFocusOut: true, matchOnDescription: true },
    );
    return picked?.core;
  }

  // ------------------------------------------------------------- writing

  /** Adds a Core and connects to it; resolves once it answered, or did not. */
  async add(setting: CoreSetting): Promise<Core | undefined> {
    await saveCores([...coreSettings().cores, setting]);
    // The listener syncs too; doing it here is what lets the caller wait for
    // the Core's first answer.
    this.sync();
    const core = this.runtimes.get(setting.id);
    await core?.connection.settled;
    return core;
  }

  async update(id: string, change: Partial<Omit<CoreSetting, 'id'>>): Promise<void> {
    const cores = coreSettings().cores.map((core) => (core.id === id ? { ...core, ...change } : core));
    await saveCores(cores);
    this.sync();
    // A new address is a new connection: the caller waits for its first answer.
    await this.moves.get(id);
    await this.runtimes.get(id)?.connection.settled;
  }

  async remove(id: string): Promise<void> {
    await saveCores(coreSettings().cores.filter((core) => core.id !== id));
    this.sync();
  }

  /**
   * The context keys the welcome views and menus read: where the extension
   * stands overall, whether any Core is signed in, whether there are several.
   */
  private async publishContext() {
    const cores = this.list();
    await Promise.all([
      vscode.commands.executeCommand('setContext', 'threavia.state', overallState(cores.map((core) => core.state))),
      vscode.commands.executeCommand('setContext', 'threavia.signedIn', cores.some((core) => core.auth.signedIn)),
      vscode.commands.executeCommand('setContext', 'threavia.severalCores', cores.length > 1),
    ]);
  }

  dispose() {
    for (const core of this.runtimes.values()) core.dispose();
    this.runtimes.clear();
    this.changed.dispose();
    this.removed.dispose();
    for (const disposable of this.disposables) disposable.dispose();
  }
}
