import * as vscode from 'vscode';

import { ApiError, EARLIER_PAGE, type CoreClient } from '../api/client';
import type { EventBus } from '../api/events';
import type {
  BackendInstance,
  Event,
  Job,
  Repository,
  Run,
  Session,
  UserInputRequest,
  ValidationRequest,
} from '../api/types';
import type { Answers } from '../attention/answer';
import type { AttentionStore } from '../attention/store';
import type { VirtualDocuments } from '../diff/documents';
import { openFileDiff } from '../diff/open';
import { markSessionOpen } from '../sessions';
import { repositoryDescription, repositoryLines, titleOf, waitingBySession } from '../tree/model';
import type { PathResolver } from '../workspace/resolve';
import type { Artifacts } from './artifacts';
import { humanise } from './format';
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

/** How long a liveness signal says something: past it, the agent is just working. */
const ACTIVITY_FOR = 30_000;

/** What every panel shares. */
export interface Services {
  extensionUri: vscode.Uri;
  client: CoreClient;
  bus: EventBus;
  attention: AttentionStore;
  answers: Answers;
  documents: VirtualDocuments;
  paths: PathResolver;
  artifacts: Artifacts;
  backends: () => Promise<BackendInstance[]>;
}

/**
 * The conversation panels: one per Session, found again rather than opened
 * twice, and restored after a reload by the editor through the serializer.
 */
export class ConversationPanels implements vscode.WebviewPanelSerializer<PersistedState>, vscode.Disposable {
  private readonly panels = new Map<string, ConversationPanel>();
  private active: ConversationPanel | undefined;
  private readonly registration: vscode.Disposable;

  constructor(private readonly services: Services) {
    this.registration = vscode.window.registerWebviewPanelSerializer(VIEW_TYPE, this);
  }

  /** Opens a Session's conversation, or brings its panel forward. */
  open(sessionId: string): void {
    const existing = this.panels.get(sessionId);
    if (existing) {
      existing.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, 'Threavia', vscode.ViewColumn.Active, options(this.services));
    this.adopt(panel, sessionId);
  }

  async deserializeWebviewPanel(panel: vscode.WebviewPanel, state: PersistedState | undefined): Promise<void> {
    // A panel with no Session to show — a state from an older version — is
    // closed rather than left blank.
    if (!state?.sessionId || this.panels.has(state.sessionId)) {
      panel.dispose();
      return;
    }
    panel.webview.options = options(this.services);
    this.adopt(panel, state.sessionId);
    return Promise.resolve();
  }

  /** The Session of the conversation in front, for the commands of its title bar. */
  get activeSessionId(): string | undefined {
    return this.active?.panel.active ? this.active.sessionId : undefined;
  }

  /** Reads the active panel's Session again, after an action changed it from here. */
  refresh(sessionId: string) {
    void this.panels.get(sessionId)?.reload();
  }

  private adopt(panel: vscode.WebviewPanel, sessionId: string) {
    const conversation = new ConversationPanel(panel, sessionId, this.services, () => {
      if (this.active === conversation) void this.setActive(undefined);
    });
    this.panels.set(sessionId, conversation);
    panel.onDidDispose(() => this.panels.delete(sessionId));
    panel.onDidChangeViewState(() => {
      if (panel.active) void this.setActive(conversation);
      else if (this.active === conversation) void this.setActive(undefined);
    });
    conversation.onPinned = () => {
      if (this.active === conversation) void this.setActive(conversation);
    };
    if (panel.active) void this.setActive(conversation);
  }

  private async setActive(conversation: ConversationPanel | undefined) {
    this.active = conversation;
    await vscode.commands.executeCommand('setContext', PINNED_KEY, Boolean(conversation?.pinned));
  }

  dispose() {
    this.registration.dispose();
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
 * One Session's conversation.
 *
 * It keeps the Session the way the web client's cache does: the snapshot it
 * opened with, every persisted event of that Session from the stream, Jobs
 * moved along by those events, and the snapshot read again when the stream
 * says something it holds — title, runs, attention — changed.
 */
class ConversationPanel {
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

  constructor(
    readonly panel: vscode.WebviewPanel,
    readonly sessionId: string,
    private readonly services: Services,
    onClosed: () => void,
  ) {
    const { webview } = panel;
    panel.iconPath = vscode.Uri.joinPath(services.extensionUri, 'media', 'threavia.svg');
    const dist = vscode.Uri.joinPath(services.extensionUri, 'dist');
    webview.html = conversationPage({
      cspSource: webview.cspSource,
      scriptUri: webview.asWebviewUri(vscode.Uri.joinPath(dist, 'webview.js')).toString(),
      styleUri: webview.asWebviewUri(vscode.Uri.joinPath(dist, 'webview-style.css')).toString(),
      sessionId,
      nonce: nonce(),
    });

    const { bus, attention } = services;
    this.disposables.push(
      webview.onDidReceiveMessage((message: WebviewMessage) => void this.receive(message)),
      panel.onDidChangeViewState(() => markSessionOpen(sessionId, panel.visible)),
      bus.on('event', (event) => this.onEvent(event)),
      bus.on('effect', (effect) => {
        if (effect.kind === 'snapshot' && effect.sessionId === sessionId) this.scheduleReload();
        if (effect.kind === 'repository' && effect.sessionId === sessionId) void this.loadRepository();
      }),
      bus.on('activity', (activity) => {
        if (activity.sessionId !== sessionId) return;
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
      markSessionOpen(sessionId, false);
      clearTimeout(this.reloadTimer);
      clearTimeout(this.activityTimer);
      for (const disposable of this.disposables) disposable.dispose();
      onClosed();
    });
    markSessionOpen(sessionId, panel.visible);
    void this.reload();
  }

  get pinned(): boolean {
    return Boolean(this.session?.pinnedAt);
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
    const { client } = this.services;
    try {
      const snapshot = await client.snapshot(this.sessionId);
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
      this.loaded = true;
      if (this.pinned !== wasPinned) this.onPinned();
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
      const backend = (await this.services.backends()).find((candidate) => candidate.id === run.backendInstanceId);
      if (backend?.id !== this.backend?.id || backend?.features?.length !== this.backend?.features?.length) {
        this.backend = backend;
        this.postView();
      }
    } catch {
      // The header goes without the backend's name; the conversation works.
    }
  }

  private async loadRepository() {
    try {
      this.repository = await this.services.client.repository(this.sessionId);
    } catch {
      this.repository = undefined;
    }
    this.postView();
  }

  private onEvent(event: Event) {
    if (event.sessionId !== this.sessionId || !this.loaded) return;
    const before = this.events;
    this.events = mergeEvents(this.events, [event]);
    if (this.events === before) return;
    this.jobs = applyToJobs(this.jobs, event);
    this.post({ type: 'events', events: [event] });
    this.postView();
  }

  // ---------------------------------------------------------------- view

  private requests() {
    const { attention } = this.services;
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
    const waiting = waitingBySession(requests).get(this.sessionId);
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
    const { client, answers, paths, artifacts, documents } = this.services;
    switch (message.type) {
      case 'ready':
        this.ready = true;
        if (this.loaded) {
          this.post({ type: 'events', events: this.events, reset: true });
          this.postView();
        }
        return;

      case 'send':
        try {
          await client.postMessage(this.sessionId, message.text, message.delivery);
          this.post({ type: 'sent' });
          this.scheduleReload();
        } catch (error) {
          this.post({ type: 'sendFailed', text: message.text, error: reason(error) });
        }
        return;

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
        await openFileDiff(client, documents, paths, {
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
    if (!oldest || this.loadingEarlier || !hasEarlier(this.events, this.exhausted)) {
      this.post({ type: 'loadingEarlier', loading: false });
      return;
    }
    this.loadingEarlier = true;
    this.post({ type: 'loadingEarlier', loading: true });
    try {
      const page = await this.services.client.earlierEvents(this.sessionId, oldest.sequence);
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

