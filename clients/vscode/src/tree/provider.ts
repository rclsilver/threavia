import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { EventBus } from '../api/events';
import type { Project, Repository, Session } from '../api/types';
import type { AttentionStore } from '../attention/store';
import { currentProjectSetting } from '../config';
import {
  ago,
  isCurrentProject,
  orderProjects,
  orderSessions,
  pinnedDescription,
  repositoryDescription,
  repositoryLines,
  sessionState,
  stateIcon,
  titleOf,
  waitingBySession,
  waitingCount,
  waitingItems,
  type IconSpec,
  type WaitingItem,
} from './model';

/** The nodes of the sidebar. */
export type Node =
  | { type: 'waiting' }
  | { type: 'request'; item: WaitingItem }
  | { type: 'pinned' }
  | { type: 'project'; project: Project }
  | { type: 'session'; session: Session; under: 'pinned' | 'project' }
  | { type: 'message'; text: string; parent?: Node; icon?: string };

/** A session node, as the context menu commands receive it. */
export type SessionNode = Extract<Node, { type: 'session' }>;
export type RequestNode = Extract<Node, { type: 'request' }>;

/** The roots are singletons, so `reveal` finds the Waiting node by identity. */
const WAITING: Node = { type: 'waiting' };
const PINNED: Node = { type: 'pinned' };

/** How long a repository read stays good: the web client's staleTime. */
const REPOSITORY_FRESH_FOR = 30_000;

type RepositoryEntry = { repo?: Repository; error?: string; at: number };

/**
 * The sidebar: what waits, what is pinned, and each Project's Sessions.
 *
 * Lists are fetched when a node is first shown and kept until the stream says
 * they changed, so expanding and collapsing costs nothing. The repository of a
 * Session is read only when the Session is hovered or selected: asking every
 * backend for git state of fifty Sessions to draw a list is the eager fetch
 * the web client avoids too.
 */
export class SidebarProvider implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private projects: Project[] | undefined;
  private pinned: Session[] | undefined;
  private readonly sessions = new Map<string, Session[]>();
  private readonly repositories = new Map<string, RepositoryEntry>();
  private readonly loadingRepositories = new Map<string, Promise<void>>();
  private selected: string | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private enabled = false;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly client: CoreClient,
    private readonly attention: AttentionStore,
    bus: EventBus,
  ) {
    this.disposables.push(
      attention.onChange(() => this.redraw()),
      bus.on('effect', (effect) => {
        if (effect.kind === 'sessions') this.invalidateSessions();
        if (effect.kind === 'repository') this.invalidateRepository(effect.sessionId);
      }),
      vscode.workspace.onDidChangeConfiguration((event) => {
        if (event.affectsConfiguration('threavia.project')) this.redraw();
      }),
    );
  }

  /** Shows Core's data, or nothing at all while there is no Core to ask. */
  setEnabled(enabled: boolean) {
    this.enabled = enabled;
    this.reload();
  }

  /** Drops everything cached and draws again. */
  reload() {
    this.projects = undefined;
    this.pinned = undefined;
    this.sessions.clear();
    this.repositories.clear();
    this.changed.fire(undefined);
  }

  /** Lists changed: re-read them, coalescing the burst a replay produces. */
  invalidateSessions() {
    this.pinned = undefined;
    this.sessions.clear();
    this.projects = undefined;
    this.redraw();
  }

  private invalidateRepository(sessionId: string) {
    if (!this.repositories.delete(sessionId)) return;
    // The one being looked at is read again now; the others when they are.
    if (this.selected === sessionId) void this.loadRepository(sessionId).then(() => this.redraw());
    else this.redraw();
  }

  private redraw() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.changed.fire(undefined), 100);
  }

  /** The node `reveal` expands when the status bar is clicked. */
  get waitingNode(): Node {
    return WAITING;
  }

  /** Every Session the sidebar has loaded, for a title the event does not carry. */
  findSession(sessionId: string): Session | undefined {
    for (const list of [this.pinned ?? [], ...this.sessions.values()]) {
      const found = list.find((session) => session.id === sessionId);
      if (found) return found;
    }
    return undefined;
  }

  // --------------------------------------------------------------- children

  async getChildren(node?: Node): Promise<Node[]> {
    if (!this.enabled) return [];
    try {
      if (!node) return await this.roots();
      switch (node.type) {
        case 'waiting':
          return this.waitingChildren();
        case 'pinned':
          return this.pinnedChildren();
        case 'project':
          return await this.projectChildren(node);
        default:
          return [];
      }
    } catch (error) {
      return [{ type: 'message', text: describe(error), parent: node, icon: 'error' }];
    }
  }

  private async roots(): Promise<Node[]> {
    const [projects, pinned] = await Promise.all([this.loadProjects(), this.loadPinned()]);
    const setting = currentProjectSetting();
    return [
      WAITING,
      // Nothing at all until something is pinned, so a person who never pins
      // sees the sidebar they had.
      ...(pinned.length > 0 ? [PINNED] : []),
      ...orderProjects(projects, setting).map((project): Node => ({ type: 'project', project })),
    ];
  }

  private waitingChildren(): Node[] {
    const items = waitingItems(this.attention.current);
    if (items.length === 0) return [{ type: 'message', text: 'Nothing waits for you.', parent: WAITING }];
    return items.map((item): Node => ({ type: 'request', item }));
  }

  private async pinnedChildren(): Promise<Node[]> {
    const pinned = await this.loadPinned();
    return pinned.map((session): Node => ({ type: 'session', session, under: 'pinned' }));
  }

  private async projectChildren(node: Extract<Node, { type: 'project' }>): Promise<Node[]> {
    let sessions = this.sessions.get(node.project.id);
    if (!sessions) {
      sessions = await this.client.sessions(node.project.id);
      this.sessions.set(node.project.id, sessions);
    }
    const ordered = orderSessions(sessions, waitingBySession(this.attention.current));
    if (ordered.length === 0) return [{ type: 'message', text: 'No sessions yet.', parent: node }];
    return ordered.map((session): Node => ({ type: 'session', session, under: 'project' }));
  }

  private async loadProjects(): Promise<Project[]> {
    this.projects ??= await this.client.projects();
    return this.projects;
  }

  private async loadPinned(): Promise<Session[]> {
    this.pinned ??= await this.client.pinnedSessions();
    return this.pinned;
  }

  getParent(node: Node): Node | undefined {
    switch (node.type) {
      case 'request':
        return WAITING;
      case 'session':
        return node.under === 'pinned'
          ? PINNED
          : (() => {
              const project = this.projects?.find((candidate) => candidate.id === node.session.projectId);
              return project ? { type: 'project', project } : undefined;
            })();
      case 'message':
        return node.parent;
      default:
        return undefined;
    }
  }

  // ------------------------------------------------------------------ items

  getTreeItem(node: Node): vscode.TreeItem {
    switch (node.type) {
      case 'waiting':
        return this.waitingItem();
      case 'pinned': {
        const item = new vscode.TreeItem('Pinned', vscode.TreeItemCollapsibleState.Expanded);
        item.id = 'pinned';
        item.iconPath = new vscode.ThemeIcon('pinned');
        return item;
      }
      case 'project':
        return this.projectItem(node.project);
      case 'session':
        return this.sessionItem(node);
      case 'request':
        return requestItem(node.item);
      case 'message': {
        const item = new vscode.TreeItem(node.text, vscode.TreeItemCollapsibleState.None);
        if (node.icon) item.iconPath = new vscode.ThemeIcon(node.icon, new vscode.ThemeColor('errorForeground'));
        return item;
      }
    }
  }

  private waitingItem(): vscode.TreeItem {
    const count = waitingCount(this.attention.current);
    // Quiet when nothing waits; loud when something does.
    const item = new vscode.TreeItem(
      'Waiting',
      count > 0 ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.Collapsed,
    );
    item.id = 'waiting';
    item.description = count > 0 ? String(count) : undefined;
    item.tooltip = count > 0 ? `${count} waiting for you` : 'Nothing waits for you';
    item.iconPath =
      count > 0
        ? new vscode.ThemeIcon('inbox', new vscode.ThemeColor('list.warningForeground'))
        : new vscode.ThemeIcon('inbox');
    return item;
  }

  private projectItem(project: Project): vscode.TreeItem {
    const current = isCurrentProject(project, currentProjectSetting());
    const item = new vscode.TreeItem(
      project.name,
      current ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.Collapsed,
    );
    item.id = `project:${project.id}`;
    item.contextValue = 'project';
    item.iconPath = new vscode.ThemeIcon(current ? 'folder-active' : 'folder');
    item.tooltip = project.description || project.name;
    return item;
  }

  private sessionItem(node: SessionNode): vscode.TreeItem {
    const { session } = node;
    const state = sessionState(session, waitingBySession(this.attention.current));
    const item = new vscode.TreeItem(titleOf(session), vscode.TreeItemCollapsibleState.None);
    // A Session pinned also appears under its Project: the ids keep the two
    // rows apart.
    item.id = `${node.under}:${session.id}`;
    item.iconPath = themeIcon(stateIcon(state));
    item.contextValue = session.pinnedAt ? 'session.pinned' : 'session';

    const elsewhere =
      node.under === 'pinned' ? pinnedDescription(session, this.projects ?? [], currentProjectSetting()) : undefined;
    const entry = this.repositories.get(session.id);
    const git = entry?.repo ? repositoryDescription(entry.repo) : undefined;
    item.description = [git, elsewhere].filter(Boolean).join(' · ') || undefined;
    item.command = { command: 'threavia.openSession', title: 'Open Session', arguments: [session.id] };
    return item;
  }

  /**
   * Fills a Session's tooltip when it is hovered, with its repository read
   * then: the only moment the person asked about it.
   */
  async resolveTreeItem(item: vscode.TreeItem, node: Node): Promise<vscode.TreeItem> {
    if (node.type !== 'session') return item;
    await this.loadRepository(node.session.id);
    item.tooltip = this.sessionTooltip(node.session);
    return item;
  }

  private sessionTooltip(session: Session): vscode.MarkdownString {
    const state = stateIcon(sessionState(session, waitingBySession(this.attention.current)));
    const markdown = new vscode.MarkdownString(undefined, true);
    markdown.appendMarkdown(`**${escape(titleOf(session))}**\n\n`);
    markdown.appendMarkdown(`$(${state.id.replace('~spin', '')}) ${state.label} · updated ${ago(session.updatedAt)}\n\n`);
    const entry = this.repositories.get(session.id);
    if (entry?.repo) {
      markdown.appendMarkdown(`$(git-branch) ${repositoryLines(entry.repo).map(escape).join('  \n')}`);
    } else if (entry?.error) {
      markdown.appendMarkdown(`$(git-branch) ${escape(entry.error)}`);
    }
    return markdown;
  }

  /** Follows the selection, so the selected Session shows its branch in its row. */
  select(node: Node | undefined) {
    this.selected = node?.type === 'session' ? node.session.id : undefined;
    if (this.selected) void this.loadRepository(this.selected).then(() => this.redraw());
  }

  private loadRepository(sessionId: string, fetch = false): Promise<void> {
    const entry = this.repositories.get(sessionId);
    if (!fetch && entry && Date.now() - entry.at < REPOSITORY_FRESH_FOR) return Promise.resolve();
    const loading = this.loadingRepositories.get(sessionId);
    if (loading && !fetch) return loading;

    const load = this.client
      .repository(sessionId, fetch)
      .then((repo) => {
        this.repositories.set(sessionId, { repo, at: Date.now() });
      })
      .catch((error: unknown) => {
        this.repositories.set(sessionId, { error: repositoryError(error), at: Date.now() });
        if (fetch) throw error;
      })
      .finally(() => this.loadingRepositories.delete(sessionId));
    this.loadingRepositories.set(sessionId, load);
    return load;
  }

  /** Asks the backend to fetch origin first, then shows what it found. */
  async refreshRepository(sessionId: string): Promise<Repository | undefined> {
    await this.loadRepository(sessionId, true);
    this.redraw();
    return this.repositories.get(sessionId)?.repo;
  }

  dispose() {
    clearTimeout(this.timer);
    this.changed.dispose();
    for (const disposable of this.disposables) disposable.dispose();
  }
}

function requestItem(waiting: WaitingItem): vscode.TreeItem {
  const item = new vscode.TreeItem(waiting.label, vscode.TreeItemCollapsibleState.None);
  item.id = `request:${waiting.id}`;
  item.description = waiting.description;
  item.tooltip = waiting.detail;
  item.contextValue = waiting.kind;
  item.iconPath = new vscode.ThemeIcon(
    waiting.kind === 'validation' ? 'shield' : 'question',
    new vscode.ThemeColor('list.warningForeground'),
  );
  item.command = { command: 'threavia.openSession', title: 'Open Session', arguments: [waiting.sessionId] };
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
