import * as vscode from 'vscode';
import { sessionsByTitle } from '../../../shared/sessions';

import { ApiError } from '../api/client';
import type { Monitor, Project, Repository, Session } from '../api/types';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import { isSessionOpen } from '../sessions';
import {
  activityDescription,
  ago,
  coreIcon,
  coreMessage,
  coreSections,
  isCurrentProject,
  orderSessions,
  pinnedDescription,
  repositoryDescription,
  repositoryLines,
  sessionState,
  sidebarRoots,
  stateIcon,
  titleOf,
  unreadSessions,
  waitingByProject,
  waitingBySession,
  waitingCount,
  waitingItems,
  type CoreState,
  type IconSpec,
  type WaitingItem,
} from './model';

/** The nodes of the sidebar. Every one but a message names its Core. */
export type Node =
  | { type: 'core'; coreId: string }
  | { type: 'waiting'; coreId: string }
  | { type: 'request'; coreId: string; item: WaitingItem }
  | { type: 'pinned'; coreId: string }
  | { type: 'project'; coreId: string; project: Project }
  | { type: 'session'; coreId: string; session: Session; under: 'pinned' | 'project' }
  | { type: 'message'; text: string; parent?: Node; icon?: string; command?: vscode.Command };

/** A session node, as the context menu commands receive it. */
export type SessionNode = Extract<Node, { type: 'session' }>;
export type RequestNode = Extract<Node, { type: 'request' }>;
export type ProjectNode = Extract<Node, { type: 'project' }>;
export type CoreNode = Extract<Node, { type: 'core' }>;

/** How long a repository read stays good: the web client's staleTime. */
const REPOSITORY_FRESH_FOR = 30_000;

type RepositoryEntry = { repo?: Repository; error?: string; at: number };

/** What the sidebar keeps for one Core. */
class CoreCache {
  projects: Project[] | undefined;
  /**
   * The unread Sessions over its Projects, as last read. Kept while the lists
   * are read again, so the Core's row does not blink to nothing meanwhile.
   */
  unread = 0;
  pinned: Session[] | undefined;
  readonly monitors = new Map<string, Monitor[]>();
  readonly sessions = new Map<string, Session[]>();
  readonly repositories = new Map<string, RepositoryEntry>();
  readonly loadingRepositories = new Map<string, Promise<void>>();
  /** The Core's state when its lists were read; a change of it makes them stale. */
  state: CoreState | undefined;
  /**
   * The rows that stay the same objects, so `reveal` finds the Waiting node
   * by identity and getParent answers with the node the tree already holds.
   */
  readonly core: CoreNode;
  readonly waiting: Node;
  readonly pinnedNode: Node;

  constructor(coreId: string) {
    this.core = { type: 'core', coreId };
    this.waiting = { type: 'waiting', coreId };
    this.pinnedNode = { type: 'pinned', coreId };
  }

  forgetLists() {
    this.projects = undefined;
    this.pinned = undefined;
    this.sessions.clear();
    this.monitors.clear();
  }
}

/**
 * The sidebar: for each Core, what waits, what is pinned, and each Project's
 * Sessions; with several Cores, under a row per Core.
 *
 * Lists are fetched when a node is first shown and kept until the stream says
 * they changed, so expanding and collapsing costs nothing. The repository of a
 * Session is read when hovered or selected, and the selected visible row is
 * kept current. Only that row is polled, rather than every Session in a list.
 */
export class SidebarProvider implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private readonly caches = new Map<string, CoreCache>();
  private selected: { coreId: string; sessionId: string } | undefined;
  private visible = false;
  private repositoryWatch: vscode.Disposable | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private readonly cores: Cores) {
    const monitorTimer = setInterval(() => { if (!this.visible) return; for (const cache of this.caches.values()) cache.forgetLists(); this.redraw(); }, 15_000);
    this.disposables.push({ dispose: () => clearInterval(monitorTimer) });
    this.disposables.push(
      cores.onAttention(() => this.redraw()),
      cores.on('effect', (core, effect) => {
        if (effect.kind === 'projects') this.reload(core.id);
        if (effect.kind === 'sessions') this.invalidateSessions(core.id);
        if (effect.kind === 'repository') this.invalidateRepository(core.id, effect.sessionId);
      }),
      // A Core that came, went, or changed state: its part is read again. A
      // renewed token is a change too, and changes nothing that was read.
      cores.onDidChange((core) => {
        if (core) {
          const cache = this.caches.get(core.id);
          if (cache?.state !== undefined && cache.state !== core.state) this.caches.delete(core.id);
        } else {
          for (const id of this.caches.keys()) if (!cores.get(id)) this.caches.delete(id);
        }
        this.watchRepository();
        this.changed.fire(undefined);
      }),
    );
  }

  private cache(coreId: string): CoreCache {
    let cache = this.caches.get(coreId);
    if (!cache) {
      cache = new CoreCache(coreId);
      this.caches.set(coreId, cache);
    }
    return cache;
  }

  /** Drops everything cached, for one Core or all of them, and draws again. */
  reload(coreId?: string) {
    if (coreId) this.caches.delete(coreId);
    else this.caches.clear();
    this.changed.fire(undefined);
  }

  /** Lists changed: re-read them, coalescing the burst a replay produces. */
  invalidateSessions(coreId: string) {
    this.caches.get(coreId)?.forgetLists();
    this.redraw();
  }

  private invalidateRepository(coreId: string, sessionId: string) {
    if (!this.caches.get(coreId)?.repositories.delete(sessionId)) return;
    // The one being looked at is read again now; the others when they are.
    if (this.selected?.coreId === coreId && this.selected.sessionId === sessionId) {
      void this.loadRepository(coreId, sessionId).then(() => this.redraw());
    } else {
      this.redraw();
    }
  }

  private redraw() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.changed.fire(undefined), 100);
  }

  /** The Waiting node of a Core, which `reveal` expands when the status bar is clicked. */
  waitingNode(coreId: string): Node {
    return this.cache(coreId).waiting;
  }

  /** Every Session the sidebar has loaded on a Core, for a title the event does not carry. */
  findSession(coreId: string, sessionId: string): Session | undefined {
    const cache = this.caches.get(coreId);
    if (!cache) return undefined;
    for (const list of [cache.pinned ?? [], ...cache.sessions.values()]) {
      const found = list.find((session) => session.id === sessionId);
      if (found) return found;
    }
    return undefined;
  }

  // --------------------------------------------------------------- children

  async getChildren(node?: Node): Promise<Node[]> {
    try {
      if (!node) return await this.roots();
      const core = 'coreId' in node ? this.cores.get(node.coreId) : undefined;
      if (!core) return [];
      switch (node.type) {
        case 'core':
          return await this.coreChildren(core);
        case 'waiting':
          return this.waitingChildren(core);
        case 'pinned':
          return await this.pinnedChildren(core);
        case 'project':
          return await this.projectChildren(core, node);
        default:
          return [];
      }
    } catch (error) {
      return [{ type: 'message', text: describe(error), parent: node, icon: 'error' }];
    }
  }

  private async roots(): Promise<Node[]> {
    const roots = sidebarRoots(this.cores.list().map((core) => ({ id: core.id, state: core.state })));
    switch (roots.kind) {
      case 'empty':
        return [];
      case 'cores':
        return roots.coreIds.map((coreId) => this.cache(coreId).core);
      case 'flat': {
        const core = this.cores.get(roots.coreId);
        return core ? this.sections(core) : [];
      }
    }
  }

  /** A Core's row's children: its sections, or why it cannot show them. */
  private async coreChildren(core: Core): Promise<Node[]> {
    const parent = this.cache(core.id).core;
    const message = coreMessage(core.state);
    if (!message) return this.sections(core);
    const command: vscode.Command | undefined =
      message.command === 'signIn'
        ? { command: 'threavia.signIn', title: 'Sign In', arguments: [core.id] }
        : message.command === 'refresh'
          ? { command: 'threavia.refresh', title: 'Try Again', arguments: [parent] }
          : undefined;
    return [{ type: 'message', text: message.text, parent, icon: message.icon, command }];
  }

  private async sections(core: Core): Promise<Node[]> {
    const cache = this.cache(core.id);
    cache.state = core.state;
    const [projects, pinned] = await Promise.all([this.loadProjects(core), this.loadPinned(core)]);
    const nodes = coreSections(pinned.length, projects, core.projectSetting).map((section): Node => {
      switch (section.type) {
        case 'waiting':
          return cache.waiting;
        case 'pinned':
          return cache.pinnedNode;
        case 'project':
          return { type: 'project', coreId: core.id, project: section.project };
      }
    });
    if (projects.length === 0) nodes.push({
      type: 'message', text: 'Create a project…', icon: 'new-folder',
      parent: this.cores.several ? cache.core : undefined,
      command: { command: 'threavia.newProject', title: 'New Project', arguments: [core.id] },
    });
    return nodes;
  }

  private waitingChildren(core: Core): Node[] {
    const items = waitingItems(core.attention.current);
    const parent = this.cache(core.id).waiting;
    if (items.length === 0) return [{ type: 'message', text: 'Nothing waits for you.', parent }];
    return items.map((item): Node => ({ type: 'request', coreId: core.id, item }));
  }

  private async loadMonitors(core: Core, projectId: string) {
    const cache = this.cache(core.id);
    if (!cache.monitors.has(projectId)) {
      try { cache.monitors.set(projectId, await core.client.projectMonitors(projectId)); }
      catch { cache.monitors.set(projectId, []); }
    }
  }

  private async pinnedChildren(core: Core): Promise<Node[]> {
    const pinned = await this.loadPinned(core);
    await Promise.all([...new Set(pinned.map(s => s.projectId))].map(id => this.loadMonitors(core, id)));
    return sessionsByTitle(pinned).map((session): Node => ({ type: 'session', coreId: core.id, session, under: 'pinned' }));
  }

  private async projectChildren(core: Core, node: ProjectNode): Promise<Node[]> {
    const cache = this.cache(core.id);
    let sessions = cache.sessions.get(node.project.id);
    if (!sessions) {
      sessions = await core.client.sessions(node.project.id);
      cache.sessions.set(node.project.id, sessions);
    }
    await this.loadMonitors(core, node.project.id);
    const ordered = orderSessions(sessions);
    if (ordered.length === 0) return [{ type: 'message', text: 'No sessions yet.', parent: node }];
    return ordered.map((session): Node => ({ type: 'session', coreId: core.id, session, under: 'project' }));
  }

  private async loadProjects(core: Core): Promise<Project[]> {
    const cache = this.cache(core.id);
    if (!cache.projects) {
      cache.projects = await core.client.projects();
      const unread = unreadSessions(cache.projects);
      // The Core's row is drawn before its Projects are read: it is drawn
      // again when what they hold changed.
      if (unread !== cache.unread && this.cores.several) this.changed.fire(cache.core);
      cache.unread = unread;
    }
    return cache.projects;
  }

  /**
   * The unread Sessions of a Core, read with the lists the sidebar shows, so
   * the activity bar's badge and the rows never disagree.
   */
  async unread(core: Core): Promise<number> {
    await this.loadProjects(core);
    return this.cache(core.id).unread;
  }

  private async loadPinned(core: Core): Promise<Session[]> {
    const cache = this.cache(core.id);
    cache.pinned ??= await core.client.pinnedSessions();
    return cache.pinned;
  }

  getParent(node: Node): Node | undefined {
    if (node.type === 'message') return node.parent;
    const cache = this.cache(node.coreId);
    // With one Core, its sections are the roots.
    const top = this.cores.several ? cache.core : undefined;
    switch (node.type) {
      case 'core':
        return undefined;
      case 'waiting':
      case 'pinned':
      case 'project':
        return top;
      case 'request':
        return cache.waiting;
      case 'session': {
        if (node.under === 'pinned') return cache.pinnedNode;
        const project = cache.projects?.find((candidate) => candidate.id === node.session.projectId);
        return project ? { type: 'project', coreId: node.coreId, project } : undefined;
      }
    }
  }

  // ------------------------------------------------------------------ items

  getTreeItem(node: Node): vscode.TreeItem {
    switch (node.type) {
      case 'core':
        return this.coreItem(node.coreId);
      case 'waiting':
        return this.waitingItem(node.coreId);
      case 'pinned': {
        const item = new vscode.TreeItem('Pinned', vscode.TreeItemCollapsibleState.Expanded);
        item.id = `${node.coreId}:pinned`;
        item.iconPath = new vscode.ThemeIcon('pinned');
        return item;
      }
      case 'project':
        return this.projectItem(node);
      case 'session':
        return this.sessionItem(node);
      case 'request':
        return requestItem(node);
      case 'message': {
        const item = new vscode.TreeItem(node.text, vscode.TreeItemCollapsibleState.None);
        if (node.icon) {
          const color = node.icon === 'error' ? 'errorForeground' : undefined;
          item.iconPath = new vscode.ThemeIcon(node.icon, color ? new vscode.ThemeColor(color) : undefined);
        }
        item.command = node.command;
        return item;
      }
    }
  }

  /**
   * A Core's row: its name, its host, and how it stands. Its context value
   * says both its state and whether it has a sign-in, which the menus in
   * package.json match on.
   */
  private coreItem(coreId: string): vscode.TreeItem {
    const core = this.cores.get(coreId);
    const item = new vscode.TreeItem(core?.name ?? coreId, vscode.TreeItemCollapsibleState.Expanded);
    item.id = `core:${coreId}`;
    if (!core) return item;
    const icon = coreIcon(core.state);
    const activity = core.state === 'ready'
      ? activityDescription(waitingCount(core.attention.current), this.cache(coreId).unread)
      : undefined;
    item.description = [activity, core.host].filter(Boolean).join(' · ');
    item.iconPath = themeIcon(icon);
    item.contextValue = `core.${core.state}${core.auth.signedIn ? '.signedIn' : ''}`;
    const account = core.auth.accountLabel;
    item.tooltip = [`${core.name} — ${core.url}`, account ? `${icon.label} as ${account}` : icon.label].join('\n');
    return item;
  }

  private waitingItem(coreId: string): vscode.TreeItem {
    const count = waitingCount(this.cores.get(coreId)?.attention.current ?? { validations: [], userInputs: [] });
    // Quiet when nothing waits; loud when something does.
    const item = new vscode.TreeItem(
      'Waiting',
      count > 0 ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.Collapsed,
    );
    item.id = `${coreId}:waiting`;
    item.description = count > 0 ? String(count) : undefined;
    item.tooltip = count > 0 ? `${count} waiting for you` : 'Nothing waits for you';
    item.iconPath =
      count > 0
        ? new vscode.ThemeIcon('inbox', new vscode.ThemeColor('list.warningForeground'))
        : new vscode.ThemeIcon('inbox');
    return item;
  }

  private projectItem(node: ProjectNode): vscode.TreeItem {
    const { project } = node;
    const current = isCurrentProject(project, this.cores.get(node.coreId)?.projectSetting ?? '');
    const item = new vscode.TreeItem(
      project.name,
      current ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.Collapsed,
    );
    item.id = `${node.coreId}:project:${project.id}`;
    item.contextValue = 'project';
    item.iconPath = new vscode.ThemeIcon(current ? 'folder-active' : 'folder');
    const waiting = waitingByProject(this.cores.get(node.coreId)?.attention.current ?? { validations: [], userInputs: [] });
    item.description = activityDescription(waiting.get(project.id) ?? 0, project.unreadSessions ?? 0);
    item.tooltip = project.description || project.name;
    return item;
  }

  private sessionItem(node: SessionNode): vscode.TreeItem {
    const { session, coreId } = node;
    const core = this.cores.get(coreId);
    const cache = this.cache(coreId);
    const state = sessionState(session, waitingBySession(core?.attention.current ?? { validations: [], userInputs: [] }));
    const item = new vscode.TreeItem(titleOf(session), vscode.TreeItemCollapsibleState.None);
    // A Session pinned also appears under its Project: the ids keep the two
    // rows apart.
    item.id = `${coreId}:${node.under}:${session.id}`;
    // The one open in the window the person is in is being read: it does not
    // stand out for the moment before Core hears it was.
    const unread = Boolean(session.unread) && !(isSessionOpen({ coreId, sessionId: session.id }) && vscode.window.state.focused);
    item.iconPath = themeIcon(stateIcon(state, unread));
    item.contextValue = session.pinnedAt ? 'session.pinned' : 'session';

    const elsewhere =
      node.under === 'pinned' ? pinnedDescription(session, cache.projects ?? [], core?.projectSetting ?? '') : undefined;
    const entry = cache.repositories.get(session.id);
    const git = entry?.repo ? repositoryDescription(entry.repo) : undefined;
    const monitors = cache.monitors.get(session.projectId)?.filter(m => m.sessionId === session.id && ['STARTING', 'RUNNING', 'STOPPING'].includes(m.state)) ?? [];
    const live = monitors.every(m => m.confirmed && Date.now() - Date.parse(m.lastSeenAt) < 30_000);
    const watching = monitors.length ? `${monitors.length} monitor${monitors.length > 1 ? 's' : ''}${live ? ' active' : ' unconfirmed'}` : undefined;
    if (watching && !session.activeJobStatus) item.iconPath = new vscode.ThemeIcon('radio-tower');
    item.description = [unread && 'unread', watching, git, elsewhere].filter(Boolean).join(' · ') || undefined;
    item.command = {
      command: 'threavia.openSession',
      title: 'Open Session',
      arguments: [{ coreId, sessionId: session.id }],
    };
    return item;
  }

  /**
   * Fills a Session's tooltip when it is hovered, with its repository read
   * then: the only moment the person asked about it.
   */
  async resolveTreeItem(item: vscode.TreeItem, node: Node): Promise<vscode.TreeItem> {
    if (node.type !== 'session') return item;
    await this.loadRepository(node.coreId, node.session.id);
    item.tooltip = this.sessionTooltip(node.coreId, node.session);
    return item;
  }

  private sessionTooltip(coreId: string, session: Session): vscode.MarkdownString {
    const pending = this.cores.get(coreId)?.attention.current ?? { validations: [], userInputs: [] };
    const state = stateIcon(sessionState(session, waitingBySession(pending)), session.unread);
    const markdown = new vscode.MarkdownString(undefined, true);
    markdown.appendMarkdown(`**${escape(titleOf(session))}**\n\n`);
    markdown.appendMarkdown(`$(${state.id.replace('~spin', '')}) ${state.label} · updated ${ago(session.updatedAt)}\n\n`);
    const entry = this.caches.get(coreId)?.repositories.get(session.id);
    if (entry?.repo) {
      markdown.appendMarkdown(`$(git-branch) ${repositoryLines(entry.repo).map(escape).join('  \n')}`);
    } else if (entry?.error) {
      markdown.appendMarkdown(`$(git-branch) ${escape(entry.error)}`);
    }
    return markdown;
  }

  /** Follows the selection, so the selected Session shows its branch in its row. */
  select(node: Node | undefined) {
    this.selected = node?.type === 'session' ? { coreId: node.coreId, sessionId: node.session.id } : undefined;
    this.watchRepository();
  }

  setVisible(visible: boolean) {
    this.visible = visible;
    this.watchRepository();
  }

  private watchRepository() {
    this.repositoryWatch?.dispose();
    this.repositoryWatch = undefined;
    const selected = this.selected;
    if (!this.visible || !selected) return;
    const core = this.cores.get(selected.coreId);
    if (!core) return;
    this.repositoryWatch = core.repositories.watch(selected.sessionId, (value) => {
      const cache = this.cache(selected.coreId);
      cache.repositories.set(selected.sessionId, value.repo
        ? { repo: value.repo, at: Date.now() }
        : { error: repositoryError(value.error), at: Date.now() });
      this.redraw();
    });
  }

  private loadRepository(coreId: string, sessionId: string, fetch = false): Promise<void> {
    const core = this.cores.get(coreId);
    if (!core) return Promise.resolve();
    const cache = this.cache(coreId);
    const entry = cache.repositories.get(sessionId);
    if (!fetch && entry && Date.now() - entry.at < REPOSITORY_FRESH_FOR) return Promise.resolve();
    const loading = cache.loadingRepositories.get(sessionId);
    if (loading && !fetch) return loading;

    const load = core.repositories
      .refresh(sessionId, fetch)
      .then((repo) => {
        cache.repositories.set(sessionId, { repo, at: Date.now() });
      })
      .catch((error: unknown) => {
        cache.repositories.set(sessionId, { error: repositoryError(error), at: Date.now() });
        if (fetch) throw error;
      })
      .finally(() => cache.loadingRepositories.delete(sessionId));
    cache.loadingRepositories.set(sessionId, load);
    return load;
  }

  /** Asks the backend to fetch origin first, then shows what it found. */
  async refreshRepository(coreId: string, sessionId: string): Promise<Repository | undefined> {
    await this.loadRepository(coreId, sessionId, true);
    this.redraw();
    return this.caches.get(coreId)?.repositories.get(sessionId)?.repo;
  }

  dispose() {
    this.repositoryWatch?.dispose();
    clearTimeout(this.timer);
    this.changed.dispose();
    for (const disposable of this.disposables) disposable.dispose();
  }
}

function requestItem(node: RequestNode): vscode.TreeItem {
  const waiting = node.item;
  const item = new vscode.TreeItem(waiting.label, vscode.TreeItemCollapsibleState.None);
  item.id = `${node.coreId}:request:${waiting.id}`;
  item.description = waiting.description;
  item.tooltip = waiting.detail;
  item.contextValue = waiting.kind;
  item.iconPath = new vscode.ThemeIcon(
    waiting.kind === 'validation' ? 'shield' : 'question',
    new vscode.ThemeColor('list.warningForeground'),
  );
  item.command = {
    command: 'threavia.openSession',
    title: 'Open Session',
    arguments: [{ coreId: node.coreId, sessionId: waiting.sessionId }],
  };
  return item;
}

function themeIcon(spec: IconSpec): vscode.ThemeIcon {
  return new vscode.ThemeIcon(spec.id, spec.color ? new vscode.ThemeColor(spec.color) : undefined);
}

/** Why a repository could not be read, in the words of the web client. */
function repositoryError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.isConflict) return 'The working directory is not located on its backend yet.';
    if (error.isUnavailable) return 'Its backend is offline.';
    return error.message;
  }
  return 'The repository could not be read.';
}

function describe(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return 'Core did not answer.';
}

function escape(text: string): string {
  return text.replace(/[\\`*_{}[\]()#+\-.!|<>]/g, '\\$&');
}
