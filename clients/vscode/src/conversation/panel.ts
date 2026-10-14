import { mediaType } from '../../../shared/media';
import { artifactKind } from './format';
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
import { payloadOf } from '../api/types';
import type { Core } from '../cores/core';
import type { SessionRef } from '../cores/refs';
import type { Cores } from '../cores/registry';
import type { VirtualDocuments } from '../diff/documents';
import { openFileDiff } from '../diff/open';
import { markSessionOpen } from '../sessions';
import { closeSessionViews, registerSessionPanel } from '../session-views';
import { repositoryDescription, titleOf, waitingBySession } from '../tree/model';
import type { PathResolver } from '../workspace/resolve';
import { humanise } from './format';
import { backendTooltip } from './backend';
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
import { documentGroup, newConversationGroup, protectConversationGroup } from './groups';
import type { HostMessage, PersistedState, SessionView, WebviewMessage } from './protocol';
import {
  activeJob,
  applyToJobs,
  applyToAttention,
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
  private groupChanges: Promise<void> = Promise.resolve();
  private disposed = false;
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
  open(ref: SessionRef): Promise<void> {
    return this.changeGroup(() => this.openSession(ref));
  }

  private async openSession(ref: SessionRef): Promise<void> {
    if (this.disposed) return;
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
    const column = await newConversationGroup();
    if (this.disposed) return;
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, 'Threavia', column, options(this.services));
    this.adopt(panel, core, target);
    await protectConversationGroup(panel);
  }

  /**
   * Opens a new Session as a draft, with what was chosen to start it. A
   * Project has one draft, as it has one first message in the web client: a
   * second "New Session" there brings it forward with the new choice, and
   * what was written in it stays.
   */
  openDraft(start: DraftStart): Promise<void> {
    return this.changeGroup(() => this.createDraft(start));
  }

  private async createDraft(start: DraftStart): Promise<void> {
    if (this.disposed) return;
    const core = this.services.cores.get(start.coreId);
    if (!core) return;
    const target: Target = { kind: 'draft', start };
    const existing = this.panels.get(targetKey(target));
    if (existing) {
      existing.redraft(start);
      existing.panel.reveal();
      return;
    }
    const column = await newConversationGroup();
    if (this.disposed) return;
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, NEW_SESSION, column, options(this.services));
    this.adopt(panel, core, target);
    await protectConversationGroup(panel);
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
    const conversation = new ConversationPanel(panel, core, target, this.services, () => this.documentColumn(), () => {
      if (this.active === conversation) void this.setActive(undefined);
    });
    this.panels.set(conversation.key, conversation);
    // The key changes once a draft is sent, so it is read when the panel
    // closes rather than when it opened.
    panel.onDidDispose(() => {
      if (this.panels.get(conversation.key) === conversation) this.panels.delete(conversation.key);
    });
    panel.onDidChangeViewState(() => {
      if (panel.active) {
        void this.setActive(conversation);
        this.protectGroup(conversation);
      } else if (this.active === conversation) void this.setActive(undefined);
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
      if (conversation.sessionId && !registerSessionPanel({ coreId: core.id, sessionId: conversation.sessionId }, panel)) return;
      if (this.active === conversation) void this.setActive(conversation);
    };
    if (conversation.sessionId && !registerSessionPanel({ coreId: core.id, sessionId: conversation.sessionId }, panel)) return;
    if (panel.active) {
      void this.setActive(conversation);
      this.protectGroup(conversation);
    }
  }

  /** Serialize layout changes, including simultaneous requests for the same Session. */
  private changeGroup<T>(action: () => Promise<T>): Promise<T> {
    const result = this.groupChanges.then(action);
    this.groupChanges = result.then(() => undefined, () => undefined);
    return result;
  }

  private protectGroup(conversation: ConversationPanel) {
    void this.changeGroup(async () => {
      if (this.disposed || this.panels.get(conversation.key) !== conversation) return;
      await protectConversationGroup(conversation.panel);
    }).catch((error: unknown) => {
      void vscode.window.showWarningMessage(`The conversation group could not be locked: ${reason(error)}`);
    });
  }

  private documentColumn(): Promise<vscode.ViewColumn> {
    return this.changeGroup(() => documentGroup(new Set(
      [...this.panels.values()].flatMap(({ panel }) => panel.viewColumn === undefined ? [] : [panel.viewColumn]),
    )));
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
    this.disposed = true;
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
  private backendGeneration = 0;
  private repository: Repository | undefined;
  private fetchingRepository = false;
  private repositoryError: string | undefined;
  private repositoryWatch: vscode.Disposable | undefined;
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
    private readonly beside: () => Promise<vscode.ViewColumn>,
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
        this.watchRepository();
      }),
      bus.on('event', (event) => this.onEvent(event)),
      bus.on('effect', (effect) => {
        if (effect.kind === 'backends') void this.loadBackend();
        const id = this.sessionId;
        if (effect.kind === 'snapshot' && id && effect.sessionId === id) this.scheduleReload();
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
      this.repositoryWatch?.dispose();
      for (const disposable of this.disposables) disposable.dispose();
      onClosed();
    });
    if (this.sessionId) markSessionOpen(this.ref(this.sessionId), panel.visible);
    this.watchRepository();
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
      if (snapshot.session.status === 'ARCHIVED') {
        await closeSessionViews(this.ref(sessionId));
        return;
      }
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
      for (const event of this.events) if (event.sequence > snapshot.cursor) this.snapshotAttention = applyToAttention(this.snapshotAttention, event);
      this.events = mergeEvents(this.events, snapshot.events);
      this.panel.title = titleOf(snapshot.session);
      const firstRead = !this.loaded;
      this.loaded = true;
      if (this.pinned !== wasPinned) this.onPinned();
      if (firstRead) this.onLoaded();
      if (this.ready) this.post({ type: 'events', events: this.events, reset: true });
      this.postView();
      void this.loadBackend();
    } catch (error) {
      if (!this.loaded && this.ready) {
        this.post({ type: 'failed', message: `This session could not be read: ${reason(error)}` });
      }
    }
  }

  private async loadBackend() {
    const generation = ++this.backendGeneration;
    const run = currentRun(this.runs);
    if (!run) return;
    try {
      const backend = (await this.core.backends()).find((candidate) => candidate.id === run.backendInstanceId);
      if (generation !== this.backendGeneration) return;
      this.backend = backend;
      this.postView();
    } catch {
      // The header goes without the backend's name; the conversation works.
    }
  }

  private watchRepository() {
    if (!this.panel.visible || !this.sessionId) {
      this.repositoryWatch?.dispose();
      this.repositoryWatch = undefined;
      return;
    }
    this.repositoryWatch ??= this.core.repositories.watch(this.sessionId, (value) => {
      // Keep the last status while a manual fetch fails, so its panel can
      // show the error and offer a retry instead of disappearing.
      if (value.repo || (!this.fetchingRepository && !this.repositoryError)) this.repository = value.repo;
      this.postView();
    });
  }

  private onEvent(event: Event) {
    if (event.type === 'project.deleted') {
      const projectId = typeof event.payload?.projectId === 'string' ? event.payload.projectId : undefined;
      const shown = this.target.kind === 'session' ? this.session?.projectId : this.target.start.projectId;
      if (projectId && projectId === shown) this.panel.dispose();
      return;
    }
    if (!this.sessionId || event.sessionId !== this.sessionId || !this.loaded) return;
    const before = this.events;
    this.events = mergeEvents(this.events, [event]);
    if (this.events === before) return;
    this.jobs = applyToJobs(this.jobs, event);
    this.snapshotAttention = applyToAttention(this.snapshotAttention, event);
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
      backendType: this.backend?.backend,
      backendTooltip: this.backend && backendTooltip(this.backend),
      backendQuotas: this.backend?.quotas,
      modelSettings: { model: this.session.model || this.backend?.modelCatalog?.defaultModel || 'Backend default', effort: this.session.effort || (this.session.model ? this.backend?.modelCatalog?.models.find((model) => model.id === this.session?.model)?.defaultEffort : this.backend?.modelCatalog?.defaultEffort) || 'Backend default', supported: this.backend?.features?.includes('MODEL_SELECTION') ?? false },
      repository: repo && text ? { text, status: repo, fetching: this.fetchingRepository, error: this.repositoryError } : undefined,
      activity: active?.status === 'RUNNING' && fresh ? humanise(fresh) : undefined,
      pending: pendingJobs(this.jobs),
      active: active && { id: active.id, status: active.status, model: active.model, effort: active.effort },
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

  private async receive(message: WebviewMessage) {
    const { client, answers, artifacts } = this.core;
    const { paths, documents } = this.services;
    switch (message.type) {
      case 'configureModel': {
        if (!this.session || !this.backend?.features?.includes('MODEL_SELECTION')) return;
        const catalog = this.backend.modelCatalog;
        const selected = await vscode.window.showQuickPick([
          { label: 'Backend default', description: catalog?.defaultModel, model: '' },
          ...(catalog?.models ?? []).map((model) => ({ label: model.name, description: model.id, model: model.id })),
        ], { title: 'Session model', placeHolder: 'Saved on Core; applies when the next job starts' });
        if (!selected) return;
        const option = catalog?.models.find((model) => model.id === (selected.model || catalog.defaultModel));
        const efforts = option?.efforts ?? [];
        const reasoning = await vscode.window.showQuickPick([
          { label: 'Backend default', description: option?.defaultEffort, effort: '' },
          ...efforts.map((effort) => ({ label: effort, effort })),
        ], { title: 'Session reasoning effort', placeHolder: 'Running work keeps its current settings' });
        if (!reasoning) return;
        try {
          this.session = await client.setSessionModel(this.session.id, selected.model, reasoning.effort);
          this.postView();
        } catch (error) { void vscode.window.showErrorMessage(`Model settings not saved: ${error instanceof Error ? error.message : String(error)}`); }
        return;
      }
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
        return this.send(message.text, message.delivery, message.artifactIds);

      case 'copyResponse': {
        try {
          const event = this.events.find((event) => event.sequence === message.sequence);
          const response = event && payloadOf(event, 'agent.message');
          if (typeof response?.text !== 'string') throw new Error('Reload the conversation and try again.');
          await vscode.env.clipboard.writeText(response.text);
          this.post({ type: 'copiedResponse', sequence: message.sequence, ok: true });
        } catch (error) {
          this.post({ type: 'copiedResponse', sequence: message.sequence, ok: false, error: `Response not copied: ${reason(error)}` });
        }
        return;
      }

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

      case 'refreshRepository': {
        const sessionId = this.sessionId;
        if (!sessionId || this.fetchingRepository) return;
        this.fetchingRepository = true;
        this.repositoryError = undefined;
        this.postView();
        try {
          this.repository = await this.core.repositories.refresh(sessionId, true);
        } catch (error) {
          this.repositoryError = reason(error);
        } finally {
          this.fetchingRepository = false;
          this.postView();
        }
        return;
      }

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
        await artifacts.open(message, this.beside, this.sessionId);
        return;

      case 'attach': {
        try {
          if (!this.projectId) throw new Error('Wait for the session to load.');
          const content = Buffer.from(message.content, 'base64');
          if (content.length > 3 * 1024 * 1024) throw new Error('Attachments are limited to 3 MiB per message.');
          const artifact = await client.uploadArtifact(this.projectId, message.filename, message.mimeType, content, this.sessionId);
          this.post({type: 'attached', artifact: {artifactId: artifact.id, filename: artifact.filename, mimeType: artifact.mimeType, size: artifact.size}});
        } catch (error) { this.post({type: 'attachFailed', error: reason(error)}); }
        return;
      }
      case 'loadMedia': {
        try {
          const artifact = await client.get<import('../api/types').Artifact>(`/api/v1/artifacts/${message.artifactId}`);
          const kind = artifactKind(artifact.mimeType, artifact.filename);
          if (!['image', 'audio', 'video'].includes(kind)) return;
          const content = await client.artifactContent(artifact.id);
          this.post({type: 'media', artifactId: artifact.id, source: `data:${mediaType(artifact.mimeType, artifact.filename)};base64,${Buffer.from(content).toString('base64')}`});
        } catch (error) { this.post({type: 'media', artifactId: message.artifactId, error: reason(error)}); }
        return;
      }
      case 'saveArtifact':
        await artifacts.save(message);
        return;
    }
  }

  private async send(text: string, delivery: Delivery, artifactIds?: string[]) {
    const action = onSend(this.target);
    if (action === 'start') return this.start(text, artifactIds);
    if (action === 'wait') await this.starting;
    const sessionId = this.sessionId;
    if (!sessionId) {
      // The Session it waited for was not created: the draft's own error says why.
      this.post({ type: 'sendFailed', text, error: 'The session was not created.' });
      return;
    }
    try {
      const job = await this.core.client.postMessage(sessionId, text, delivery, artifactIds);
      if (delivery === 'NOW' && job.status === 'RUNNING') {
        this.core.attention.dropJob(job.id, job.updatedAt);
        this.snapshotAttention = {
          validations: this.snapshotAttention.validations.filter((request) => request.scope.jobId !== job.id || Date.parse(request.createdAt) > Date.parse(job.updatedAt)),
          userInputs: this.snapshotAttention.userInputs.filter((request) => request.scope.jobId !== job.id || Date.parse(request.createdAt) > Date.parse(job.updatedAt)),
        };
        this.postView();
      }
      if (delivery === 'NOW') this.core.attention.schedule();
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
  private start(text: string, artifactIds?: string[]): Promise<void> {
    if (this.target.kind !== 'draft') return Promise.resolve();
    const body = { ...startBody(this.target.start, text), artifactIds };
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
    this.watchRepository();
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
