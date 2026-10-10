import * as vscode from 'vscode';

import { ApiError, EARLIER_PAGE } from '../api/client';
import type {
  BackendInstance,
  Delivery,
  Event,
  Job,
  Repository,
  Run,
  Session,
  UserInputRequest,
  ValidationRequest,
} from '../api/types';
import type { Core } from '../cores/core';
import type { SessionRef } from '../cores/refs';
import type { Cores } from '../cores/registry';
import type { VirtualDocuments } from '../diff/documents';
import { openFileDiff } from '../diff/open';
import { markSessionOpen } from '../sessions';
import { repositoryDescription, repositoryLines, titleOf, waitingBySession } from '../tree/model';
import type { PathResolver } from '../workspace/resolve';
import { humanise } from './format';
import {
  onSend,
  restoredTarget,
  sending,
  startBody,
  startFailed,
  started,
  targetCore,
  targetKey,
  type DraftStart,
  type Target,
} from './draft';
import { conversationPage, nonce } from './html';
import type { HostMessage, PersistedState, SessionView, WebviewMessage } from './protocol';
import {
  activeJob,
  applyToJobs,
  currentRun,
  deliveriesFor,
  hasEarlier,
  mergeEvents,
  pendingJobs,
  sessionStatus,
} from './state';

export const VIEW_TYPE = 'threavia.conversation';

/** The context key the panel's title-bar actions read. */
const PINNED_KEY = 'threavia.conversationPinned';

/**
 * The context key that hides those actions while the conversation in front
 * is a draft: there is nothing to pin or to open in the browser yet.
 */
const DRAFT_KEY = 'threavia.conversationDraft';

/** A draft's title, the web client's words. */
const NEW_SESSION = 'New session';

/** How long a liveness signal says something: past it, the agent is just working. */
const ACTIVITY_FOR = 30_000;

/** What every panel shares, whichever Core it shows. */
export interface Services {
  extensionUri: vscode.Uri;
  cores: Cores;
  documents: VirtualDocuments;
  paths: PathResolver;
}

/**
 * The conversation panels: one per Session of each Core, found again rather
 * than opened twice, and restored after a reload by the editor through the
 * serializer.
 */
export class ConversationPanels implements vscode.WebviewPanelSerializer<PersistedState>, vscode.Disposable {
  private readonly panels = new Map<string, ConversationPanel>();
  private active: ConversationPanel | undefined;
  private readonly disposables: vscode.Disposable[];
  /**
   * Told the Project of the conversation in front when it comes forward, or
   * once its Session is read: the Tasks and Memory views follow it.
   */
  onActiveProject: (coreId: string, projectId: string) => void = () => undefined;

  constructor(private readonly services: Services) {
    this.disposables = [
      vscode.window.registerWebviewPanelSerializer(VIEW_TYPE, this),
      // A Core removed takes its conversations with it: nothing in them can
      // be read or answered any more.
      services.cores.onDidRemove((coreId) => {
        for (const panel of [...this.panels.values()]) if (panel.core.id === coreId) panel.panel.dispose();
      }),
    ];
  }

  /** Opens a Session's conversation, or brings its panel forward. */
  open(ref: SessionRef): void {
    const core = this.services.cores.get(ref.coreId);
    if (!core) {
      void vscode.window.showWarningMessage('That session is on a Core that is no longer configured.');
      return;
    }
    const target: Target = { kind: 'session', coreId: ref.coreId, sessionId: ref.sessionId };
    const existing = this.panels.get(targetKey(target));
    if (existing) {
      existing.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, 'Threavia', vscode.ViewColumn.Active, options(this.services));
    this.adopt(panel, core, target);
  }

  /**
   * Opens a new Session as a draft, with what was chosen to start it. A
   * Project has one draft, as it has one first message in the web client: a
   * second "New Session" there brings it forward with the new choice, and
   * what was written in it stays.
   */
  openDraft(start: DraftStart): void {
    const core = this.services.cores.get(start.coreId);
    if (!core) return;
    const target: Target = { kind: 'draft', start };
    const existing = this.panels.get(targetKey(target));
    if (existing) {
      existing.redraft(start);
      existing.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, NEW_SESSION, vscode.ViewColumn.Active, options(this.services));
    this.adopt(panel, core, target);
  }

  async deserializeWebviewPanel(panel: vscode.WebviewPanel, state: PersistedState | undefined): Promise<void> {
    // A panel with nothing to show — a state from an older version, a Core
    // removed since — is closed rather than left blank. The Cores are read
    // first: the editor may restore panels before the extension read them.
    await this.services.cores.started;
    const target = restoredTarget(state, this.services.cores.ids[0]);
    const core = target && this.services.cores.get(targetCore(target));
    if (!target || !core || this.panels.has(targetKey(target))) {
      panel.dispose();
      return;
    }
    panel.webview.options = options(this.services);
    this.adopt(panel, core, target);
  }

  /** The Session of the conversation in front, for the commands of its title bar. */
  get activeSession(): SessionRef | undefined {
    const active = this.active;
    return active?.panel.active && active.sessionId ? { coreId: active.core.id, sessionId: active.sessionId } : undefined;
  }

  /** Reads a Session's panel again, after an action changed it from here. */
  refresh(ref: SessionRef) {
    void this.panels.get(targetKey({ kind: 'session', ...ref }))?.reload();
  }

  private adopt(panel: vscode.WebviewPanel, core: Core, target: Target) {
    const conversation = new ConversationPanel(panel, core, target, this.services, () => {
      if (this.active === conversation) void this.setActive(undefined);
    });
    this.panels.set(conversation.key, conversation);
    // The key changes once a draft is sent, so it is read when the panel
    // closes rather than when it opened.
    panel.onDidDispose(() => {
      if (this.panels.get(conversation.key) === conversation) this.panels.delete(conversation.key);
    });
    panel.onDidChangeViewState(() => {
      if (panel.active) void this.setActive(conversation);
      else if (this.active === conversation) void this.setActive(undefined);
    });
    conversation.onPinned = () => {
      if (this.active === conversation) void this.setActive(conversation);
    };
    conversation.onLoaded = () => {
      if (this.active === conversation && conversation.projectId) {
        this.onActiveProject(conversation.core.id, conversation.projectId);
      }
    };
    // The draft became a Session: found by its id from now on, and the title
    // bar offers what a Session's offers.
    conversation.onStarted = (draft) => {
      if (this.panels.get(draft) === conversation) this.panels.delete(draft);
      this.panels.set(conversation.key, conversation);
      if (this.active === conversation) void this.setActive(conversation);
    };
    if (panel.active) void this.setActive(conversation);
  }

  private async setActive(conversation: ConversationPanel | undefined) {
    const changed = conversation !== this.active;
    this.active = conversation;
    if (changed && conversation?.projectId) this.onActiveProject(conversation.core.id, conversation.projectId);
    await Promise.all([
      vscode.commands.executeCommand('setContext', PINNED_KEY, Boolean(conversation?.pinned)),
      vscode.commands.executeCommand('setContext', DRAFT_KEY, Boolean(conversation && !conversation.sessionId)),
    ]);
  }

  dispose() {
    for (const disposable of this.disposables) disposable.dispose();
    for (const panel of this.panels.values()) panel.panel.dispose();
  }
}

function options(services: Services): vscode.WebviewPanelOptions & vscode.WebviewOptions {
  return {
    enableScripts: true,
    // The page keeps its own state (draft, folds) through the editor's
    // webview state, so it can be dropped while hidden and drawn again.
    retainContextWhenHidden: false,
    enableFindWidget: true,
    localResourceRoots: [vscode.Uri.joinPath(services.extensionUri, 'dist')],
  };
}


/**
 * One Session's conversation, or a new Session's draft until its first
 * message creates it.
 *
 * It keeps the Session the way the web client's cache does: the snapshot it
 * opened with, every persisted event of that Session from the stream, Jobs
 * moved along by those events, and the snapshot read again when the stream
 * says something it holds — title, runs, attention — changed.
 */
class ConversationPanel {
  private target: Target;
  /** The start of the Session under way, which a message sent meanwhile waits for. */
  private starting: Promise<void> | undefined;
  private session: Session | undefined;
  private runs: Run[] = [];
  private jobs: Job[] = [];
  private events: Event[] = [];
  private snapshotAttention: { validations: ValidationRequest[]; userInputs: UserInputRequest[] } = {
    validations: [],
    userInputs: [],
  };
  private backend: BackendInstance | undefined;
  private repository: Repository | undefined;
  private activity: { kind: string; at: number } | undefined;
  private exhausted = false;
  private loadingEarlier = false;
  private ready = false;
  private loaded = false;
  private reloadTimer: ReturnType<typeof setTimeout> | undefined;
  private activityTimer: ReturnType<typeof setTimeout> | undefined;
  private readonly disposables: vscode.Disposable[] = [];
  onPinned: () => void = () => undefined;
  /** Called each time the Session is read, which is when its Project is first known. */
  onLoaded: () => void = () => undefined;
  /** Called with the draft's key once its first message created the Session. */
  onStarted: (draft: string) => void = () => undefined;

  constructor(
    readonly panel: vscode.WebviewPanel,
    readonly core: Core,
    target: Target,
    private readonly services: Services,
    onClosed: () => void,
  ) {
    this.target = target;
    const { webview } = panel;
    panel.iconPath = vscode.Uri.joinPath(services.extensionUri, 'media', 'threavia.svg');
    if (!this.sessionId) panel.title = NEW_SESSION;
    const dist = vscode.Uri.joinPath(services.extensionUri, 'dist');
    webview.html = conversationPage({
      cspSource: webview.cspSource,
      scriptUri: webview.asWebviewUri(vscode.Uri.joinPath(dist, 'webview.js')).toString(),
      styleUri: webview.asWebviewUri(vscode.Uri.joinPath(dist, 'webview-style.css')).toString(),
      coreId: core.id,
      sessionId: this.sessionId ?? '',
      nonce: nonce(),
    });

    // Every listener reads the Session id when it fires: a draft has none,
    // and gets one without the panel being opened again.
    const { bus, attention } = core;
    this.disposables.push(
      webview.onDidReceiveMessage((message: WebviewMessage) => void this.receive(message)),
      panel.onDidChangeViewState(() => {
        if (this.sessionId) markSessionOpen(this.ref(this.sessionId), panel.visible);
      }),
      bus.on('event', (event) => this.onEvent(event)),
      bus.on('effect', (effect) => {
        const id = this.sessionId;
        if (effect.kind === 'snapshot' && id && effect.sessionId === id) this.scheduleReload();
        if (effect.kind === 'repository' && id && effect.sessionId === id) void this.loadRepository();
      }),
      bus.on('activity', (activity) => {
        if (!this.sessionId || activity.sessionId !== this.sessionId) return;
        this.activity = { kind: activity.kind, at: activity.at };
        clearTimeout(this.activityTimer);
        this.activityTimer = setTimeout(() => this.postView(), ACTIVITY_FOR);
        this.postView();
      }),
      // A panel restored before Core answered, or one that lost it, tries
      // again once the stream is back.
      bus.on('connection', (connected) => {
        if (connected) this.scheduleReload();
      }),
      attention.onChange(() => this.postView()),
    );
    panel.onDidDispose(() => {
      if (this.sessionId) markSessionOpen(this.ref(this.sessionId), false);
      clearTimeout(this.reloadTimer);
      clearTimeout(this.activityTimer);
      for (const disposable of this.disposables) disposable.dispose();
      onClosed();
    });
    if (this.sessionId) markSessionOpen(this.ref(this.sessionId), panel.visible);
    void this.reload();
  }

  /** The Session shown, or nothing while the panel is a draft. */
  get sessionId(): string | undefined {
    return this.target.kind === 'session' ? this.target.sessionId : undefined;
  }

  private ref(sessionId: string): SessionRef {
    return { coreId: this.core.id, sessionId };
  }

  /** The Project it works in: chosen for a draft, read with the Session otherwise. */
  get projectId(): string | undefined {
    return this.target.kind === 'session' ? this.session?.projectId : this.target.start.projectId;
  }

  /** How the panels find this one again. */
  get key(): string {
    return targetKey(this.target);
  }

  get pinned(): boolean {
    return Boolean(this.session?.pinnedAt);
  }

  /** Another "New Session" in the same Project: the draft takes the new choice. */
  redraft(start: DraftStart) {
    if (this.target.kind !== 'draft') return;
    this.target = { kind: 'draft', start };
    if (this.ready) this.post({ type: 'draft', start });
  }

  private post(message: HostMessage) {
    void this.panel.webview.postMessage(message);
  }

  // ------------------------------------------------------------- loading

  private scheduleReload() {
    clearTimeout(this.reloadTimer);
    this.reloadTimer = setTimeout(() => void this.reload(), 250);
  }

  async reload(): Promise<void> {
    const { client } = this.core;
    const sessionId = this.sessionId;
    // A draft has nothing in Core to read.
    if (!sessionId) return;
    try {
      const snapshot = await client.snapshot(sessionId);
      const wasPinned = this.pinned;
      this.session = snapshot.session;
      this.runs = snapshot.runs;
      // The snapshot's Jobs are the truth at its point; events after it,
      // already applied, are applied again on top of them.
      let jobs = snapshot.jobs;
      for (const event of this.events) if (event.sequence > snapshot.cursor) jobs = applyToJobs(jobs, event);
      this.jobs = jobs;
      this.snapshotAttention = {
        validations: snapshot.attention.validations ?? [],
        userInputs: snapshot.attention.userInputs ?? [],
      };
      this.events = mergeEvents(this.events, snapshot.events);
      this.panel.title = titleOf(snapshot.session);
      const firstRead = !this.loaded;
      this.loaded = true;
      if (this.pinned !== wasPinned) this.onPinned();
      if (firstRead) this.onLoaded();
      if (this.ready) this.post({ type: 'events', events: this.events, reset: true });
      this.postView();
      void this.loadBackend();
      if (!this.repository) void this.loadRepository();
    } catch (error) {
      if (!this.loaded && this.ready) {
        this.post({ type: 'failed', message: `This session could not be read: ${reason(error)}` });
      }
    }
  }

  private async loadBackend() {
    const run = currentRun(this.runs);
    if (!run) return;
    try {
      const backend = (await this.core.backends()).find((candidate) => candidate.id === run.backendInstanceId);
      if (backend?.id !== this.backend?.id || backend?.features?.length !== this.backend?.features?.length) {
        this.backend = backend;
        this.postView();
      }
    } catch {
      // The header goes without the backend's name; the conversation works.
    }
  }

  private async loadRepository() {
    if (!this.sessionId) return;
    try {
      this.repository = await this.core.client.repository(this.sessionId);
    } catch {
      this.repository = undefined;
    }
    this.postView();
  }

  private onEvent(event: Event) {
    if (!this.sessionId || event.sessionId !== this.sessionId || !this.loaded) return;
    const before = this.events;
    this.events = mergeEvents(this.events, [event]);
    if (this.events === before) return;
    this.jobs = applyToJobs(this.jobs, event);
    this.post({ type: 'events', events: [event] });
    this.postView();
  }

  // ---------------------------------------------------------------- view

  private requests() {
    const { attention } = this.core;
    // The shared store is the one current copy, which drops a request the
    // moment it is answered anywhere; until it has answered once, the
    // snapshot's own copy stands in.
    if (!attention.ready) return this.snapshotAttention;
    return {
      validations: attention.current.validations.filter((request) => request.scope.sessionId === this.sessionId),
      userInputs: attention.current.userInputs.filter((request) => request.scope.sessionId === this.sessionId),
    };
  }

  private view(): SessionView | undefined {
    if (!this.session) return undefined;
    const requests = this.requests();
    const active = activeJob(this.jobs);
    const waiting = waitingBySession(requests).get(this.session.id);
    const fresh = this.activity && Date.now() - this.activity.at < ACTIVITY_FOR ? this.activity.kind : undefined;
    const repo = this.repository;
    const text = repo && repositoryDescription(repo);
    return {
      title: titleOf(this.session),
      status: sessionStatus(active, waiting),
      backend: this.backend?.name,
      repository: repo && text ? { text, tooltip: repositoryLines(repo).join('\n') } : undefined,
      activity: active?.status === 'RUNNING' && fresh ? humanise(fresh) : undefined,
      pending: pendingJobs(this.jobs),
      active: active && { id: active.id, status: active.status },
      deliveries: deliveriesFor(active, this.backend),
      validations: requests.validations,
      questions: requests.userInputs,
      more: hasEarlier(this.events, this.exhausted),
    };
  }

  private postView() {
    const view = this.view();
    if (view && this.ready) this.post({ type: 'session', view });
  }

  // ------------------------------------------------------------ messages

  /** Where a file or a diff opens: beside the conversation, not over it. */
  private get beside(): vscode.ViewColumn {
    return this.panel.viewColumn === vscode.ViewColumn.One ? vscode.ViewColumn.Two : vscode.ViewColumn.One;
  }

  private async receive(message: WebviewMessage) {
    const { client, answers, artifacts } = this.core;
    const { paths, documents } = this.services;
    switch (message.type) {
      case 'ready':
        this.ready = true;
        if (this.target.kind !== 'session') {
          this.post({ type: 'draft', start: this.target.start });
          return;
        }
        // A page drawn again from a draft's state — hidden while its Session
        // was created — learns which Session it shows now.
        this.post({ type: 'started', sessionId: this.target.sessionId });
        if (this.loaded) {
          this.post({ type: 'events', events: this.events, reset: true });
          this.postView();
        }
        return;

      case 'send':
        return this.send(message.text, message.delivery);

      case 'stop':
        try {
          await client.cancelJob(message.jobId);
          // CANCELLING has no event of its own: the snapshot says it.
          await this.reload();
        } catch (error) {
          void vscode.window.showErrorMessage(`Not stopped: ${reason(error)}`);
        }
        return;

      case 'decide': {
        const request = this.requests().validations.find((candidate) => candidate.id === message.id);
        const ok = request ? await answers.decide(request, message.approved) : false;
        this.post({ type: 'answered', id: message.id, ok });
        if (ok) this.dropRequest(message.id);
        return;
      }

      case 'answer': {
        const request = this.requests().userInputs.find((candidate) => candidate.id === message.id);
        const ok = request ? await answers.reply(request, message.value) : false;
        this.post({ type: 'answered', id: message.id, ok });
        if (ok) this.dropRequest(message.id);
        return;
      }

      case 'loadEarlier':
        return this.loadEarlier();

      case 'resolvePaths': {
        const resolved: Record<string, boolean> = {};
        await Promise.all(
          message.paths.slice(0, 200).map(async (path) => {
            resolved[path] = Boolean(await paths.resolve(path));
          }),
        );
        this.post({ type: 'paths', resolved });
        return;
      }

      case 'openPath':
        await paths.open(message.path, this.beside);
        return;

      case 'openLink':
        if (/^(https?|mailto):/i.test(message.href)) await vscode.env.openExternal(vscode.Uri.parse(message.href));
        return;

      case 'openDiff':
        if (!this.sessionId) return;
        await openFileDiff(client, documents, paths, {
          coreId: this.core.id,
          sessionId: this.sessionId,
          sequence: message.sequence,
          path: message.path,
          column: this.beside,
        });
        return;

      case 'openArtifact':
        await artifacts.open(message, this.beside);
        return;

      case 'saveArtifact':
        await artifacts.save(message);
        return;
    }
  }

  private async send(text: string, delivery: Delivery) {
    const action = onSend(this.target);
    if (action === 'start') return this.start(text);
    if (action === 'wait') await this.starting;
    const sessionId = this.sessionId;
    if (!sessionId) {
      // The Session it waited for was not created: the draft's own error says why.
      this.post({ type: 'sendFailed', text, error: 'The session was not created.' });
      return;
    }
    try {
      await this.core.client.postMessage(sessionId, text, delivery);
      this.post({ type: 'sent' });
      this.scheduleReload();
    } catch (error) {
      this.post({ type: 'sendFailed', text, error: reason(error) });
    }
  }

  /**
   * Sends a draft's first message, which creates the Session, its Run and
   * its Job at once; then this panel is that Session's, without being closed
   * and opened again.
   */
  private start(text: string): Promise<void> {
    if (this.target.kind !== 'draft') return Promise.resolve();
    const body = startBody(this.target.start, text);
    this.target = sending(this.target);
    this.starting = (async () => {
      try {
        const created = await this.core.client.startSession(body);
        this.become(created.session.id);
        this.post({ type: 'sent' });
      } catch (error) {
        this.target = startFailed(this.target);
        this.post({ type: 'sendFailed', text, error: reason(error) });
      } finally {
        this.starting = undefined;
      }
    })();
    return this.starting;
  }

  private become(sessionId: string) {
    const draft = this.key;
    this.target = started(this.target, sessionId);
    this.post({ type: 'started', sessionId });
    markSessionOpen(this.ref(sessionId), this.panel.visible);
    this.onStarted(draft);
    void this.reload();
  }

  /** Takes an answered request off the panel at once, the stream catching up after. */
  private dropRequest(id: string) {
    this.snapshotAttention = {
      validations: this.snapshotAttention.validations.filter((request) => request.id !== id),
      userInputs: this.snapshotAttention.userInputs.filter((request) => request.id !== id),
    };
    this.postView();
  }

  private async loadEarlier() {
    const oldest = this.events[0];
    const sessionId = this.sessionId;
    if (!sessionId || !oldest || this.loadingEarlier || !hasEarlier(this.events, this.exhausted)) {
      this.post({ type: 'loadingEarlier', loading: false });
      return;
    }
    this.loadingEarlier = true;
    this.post({ type: 'loadingEarlier', loading: true });
    try {
      const page = await this.core.client.earlierEvents(sessionId, oldest.sequence);
      if (page.length < EARLIER_PAGE) this.exhausted = true;
      this.events = mergeEvents(this.events, page);
      this.post({ type: 'events', events: page });
    } catch (error) {
      void vscode.window.showWarningMessage(`Earlier messages could not be read: ${reason(error)}`);
    } finally {
      this.loadingEarlier = false;
      this.post({ type: 'loadingEarlier', loading: false });
      this.postView();
    }
  }
}

function reason(error: unknown): string {
  if (error instanceof ApiError || error instanceof Error) return error.message;
  return String(error);
}

