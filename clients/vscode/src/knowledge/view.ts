import * as vscode from 'vscode';

import { ApiError } from '../api/client';
import type { Project } from '../api/types';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import type { CurrentProject } from './current';

/** A row that only says something: an empty list, an error. */
export type MessageNode = { type: 'message'; text: string; icon?: string };

/**
 * What the Tasks and Memory views share: one Project at a time, the one
 * CurrentProject names, its name (and its Core's, when there are several) in
 * the view's description; a list read when first shown and read again when
 * that Core's stream says it changed; nothing at all while no Core can be
 * asked, so the view's welcome asks to sign in.
 */
export abstract class ProjectView<Data, Node extends { type: string }> implements vscode.TreeDataProvider<Node | MessageNode>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<undefined>();
  readonly onDidChangeTreeData = this.changed.event;
  readonly view: vscode.TreeView<Node | MessageNode>;

  /** The list of the Project shown, once read; replaced as a whole on each read. */
  protected data: Data | undefined;
  /** Which Project `data` is, as `<core>/<project>`. */
  private dataFor: string | undefined;
  private error: string | undefined;
  private loading: Promise<void> | undefined;
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    viewId: string,
    protected readonly current: CurrentProject,
    protected readonly cores: Cores,
    effect: 'tasks' | 'decisions',
  ) {
    this.view = vscode.window.createTreeView(viewId, { treeDataProvider: this, showCollapseAll: true });
    this.describe();
    this.disposables.push(
      this.view,
      current.onChange(() => {
        this.describe();
        this.forget();
        this.changed.fire(undefined);
      }),
      cores.on('effect', (core, change) => {
        if (change.kind !== effect || core.id !== this.current.core?.id) return;
        // An event without a Project could be about any of them.
        if (change.projectId && change.projectId !== this.current.project?.id) return;
        this.invalidate();
      }),
      // A stream that came back may have missed changes it does not replay.
      cores.on('connection', (core, connected) => {
        if (connected && core.id === this.current.core?.id) this.invalidate();
      }),
      // No Core to ask, or one again: the welcome shows, or goes.
      cores.onDidChange(() => this.changed.fire(undefined)),
    );
  }

  private describe() {
    const { project, core } = this.current;
    this.view.description = project && core ? (this.cores.several ? `${project.name} · ${core.name}` : project.name) : undefined;
  }

  /** Drops what was read and draws again. */
  reload() {
    this.forget();
    this.changed.fire(undefined);
  }

  /**
   * The list changed: read it again, coalescing the burst a replay or an
   * agent filing ten Tasks produces. What is shown stays until the new list
   * arrives, so rows do not blink.
   */
  invalidate() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      const { core, project } = this.current;
      if (!core?.ready || !project) return;
      // A read already under way may have started before the change.
      this.generation++;
      this.loading = undefined;
      void this.load(core, project).then(() => this.changed.fire(undefined));
    }, 150);
  }

  private forget() {
    this.generation++;
    this.data = undefined;
    this.dataFor = undefined;
    this.error = undefined;
    this.loading = undefined;
  }

  private load(core: Core, project: Project): Promise<void> {
    const generation = this.generation;
    this.loading ??= this.fetch(core, project.id)
      .then((data) => {
        if (generation !== this.generation) return;
        this.data = data;
        this.dataFor = keyOf(core, project);
        this.error = undefined;
        this.loaded(core, project.id, data);
      })
      .catch((error: unknown) => {
        if (generation !== this.generation) return;
        this.error = error instanceof ApiError ? error.message : 'Core did not answer.';
      })
      .finally(() => {
        if (generation === this.generation) this.loading = undefined;
      });
    return this.loading;
  }

  async getChildren(node?: Node | MessageNode): Promise<(Node | MessageNode)[]> {
    if (this.cores.ready().length === 0) return [];
    const { project, core } = this.current;
    if (!project || !core) {
      // Until the Projects are read, there is nothing to say yet.
      return node || !this.current.settled ? [] : [{ type: 'message', text: 'Create a project to begin.' }];
    }
    const key = keyOf(core, project);
    if (this.dataFor !== key) await this.load(core, project);
    if (this.data === undefined || this.dataFor !== key) {
      return node ? [] : [{ type: 'message', text: this.error ?? 'Core did not answer.', icon: 'error' }];
    }
    if (node && isMessage(node)) return [];
    return this.children(this.data, node);
  }

  getTreeItem(node: Node | MessageNode): vscode.TreeItem {
    if (isMessage(node)) {
      const item = new vscode.TreeItem(node.text, vscode.TreeItemCollapsibleState.None);
      if (node.icon) item.iconPath = new vscode.ThemeIcon(node.icon, new vscode.ThemeColor('errorForeground'));
      return item;
    }
    return this.item(node);
  }

  /**
   * The list of the Project shown, read now if it was not yet: a command run
   * from the palette may come before the view was ever opened.
   */
  protected async dataNow(): Promise<Data | undefined> {
    const { project, core } = this.current;
    if (!project || !core) return undefined;
    const key = keyOf(core, project);
    if (this.dataFor !== key) await this.load(core, project);
    return this.dataFor === key ? this.data : undefined;
  }

  /**
   * The client of the Core a row came from. Throws when that Core was
   * removed meanwhile, which `act` says as the reason nothing changed.
   */
  protected clientOf(coreId: string) {
    const core = this.cores.get(coreId);
    if (!core) throw new Error('That Core is no longer configured.');
    return core.client;
  }

  /**
   * Runs an action against Core, then reads the list again; says why when
   * Core refuses. With `retry`, the message offers to send the same thing
   * again, for what the person typed and should not have to type twice.
   */
  protected async act(outcome: string, run: () => Promise<unknown>, retry = false): Promise<boolean> {
    try {
      await run();
      // The stream says so too; reading here is what makes the click count
      // when this window's stream is down.
      this.invalidate();
      return true;
    } catch (error) {
      const reason = error instanceof ApiError || error instanceof Error ? error.message : String(error);
      this.invalidate();
      if (!retry) {
        void vscode.window.showErrorMessage(`${outcome}: ${reason}`);
        return false;
      }
      const choice = await vscode.window.showErrorMessage(`${outcome}: ${reason}`, 'Try Again');
      return choice ? this.act(outcome, run, retry) : false;
    }
  }

  protected abstract fetch(core: Core, projectId: string): Promise<Data>;
  /** Told each time the list is read, for the documents open on its records. */
  protected abstract loaded(core: Core, projectId: string, data: Data): void;
  protected abstract children(data: Data, node: Node | undefined): (Node | MessageNode)[];
  protected abstract item(node: Node): vscode.TreeItem;

  dispose() {
    clearTimeout(this.timer);
    this.changed.dispose();
    for (const disposable of this.disposables) disposable.dispose();
  }
}

function isMessage(node: { type: string }): node is MessageNode {
  return node.type === 'message';
}

function keyOf(core: Core, project: Project): string {
  return `${core.id}/${project.id}`;
}
