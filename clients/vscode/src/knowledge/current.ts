import * as vscode from 'vscode';

import type { Project } from '../api/types';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import { isCurrentProject, orderProjects } from '../tree/model';
import { readProjectRef, resolveCurrentAcross, type ProjectRef } from './model';

/** The context key that hides New Task and Record Decision until there is a Project. */
const HAS_PROJECT_KEY = 'threavia.hasProject';

/** Where the Project last turned to is kept, per workspace. */
const FOLLOWED_KEY = 'threavia.currentProject';

/**
 * The Project the Tasks and Memory views show, shared by both: a Project of
 * one Core, among the Projects of every Core that can be asked.
 *
 * It follows the person's attention rather than asking for it: the Session or
 * Project last selected in the Sessions view and the conversation in front
 * each make their Project current, and Switch Project… chooses one outright.
 * What was followed is kept with the workspace, so a reload shows the same
 * Project; until anything was, the `project` of each Core's setting decides.
 */
export class CurrentProject implements vscode.Disposable {
  private readonly projects = new Map<string, Project[]>();
  private readonly read = new Set<string>();
  private readonly loading = new Map<string, Promise<void>>();
  private readonly reread = new Set<string>();
  private followed: ProjectRef | undefined;
  private shown: { core: Core; project: Project } | undefined;
  private readonly listeners = new Set<() => void>();
  private readonly subscription: vscode.Disposable;
  private readonly eventSubscription: vscode.Disposable;

  constructor(
    private readonly cores: Cores,
    private readonly state: vscode.Memento,
  ) {
    this.followed = readProjectRef(state.get(FOLLOWED_KEY));
    // A Core that can be asked has its Projects read; one that cannot has
    // them forgotten. A change of its setting (its Project) settles again.
    this.subscription = cores.onDidChange(() => this.sync());
    this.eventSubscription = cores.on('effect', (core, effect) => {
      if (effect.kind !== 'projects') return;
      this.read.delete(core.id);
      if (this.loading.has(core.id)) this.reread.add(core.id);
      else void this.load(core);
    });
  }

  get project(): Project | undefined {
    return this.shown?.project;
  }

  /** The Core of the Project shown. */
  get core(): Core | undefined {
    return this.shown?.core;
  }

  /**
   * Whether the Projects of every Core that can be asked were read at least
   * once: until then, no Project means "not known yet" rather than "none
   * exists".
   */
  get settled(): boolean {
    return this.cores.ready().every((core) => this.read.has(core.id));
  }

  /** Told when the Project shown changes, or when there is none any more. */
  onChange(listener: () => void): vscode.Disposable {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  }

  /** Reads the Projects of the Cores that became ready; forgets the others'. */
  private sync() {
    for (const core of this.cores.list()) {
      if (core.ready && !this.read.has(core.id)) void this.load(core);
      if (!core.ready && (this.read.has(core.id) || this.projects.has(core.id))) {
        this.read.delete(core.id);
        this.projects.delete(core.id);
      }
    }
    for (const id of [...this.projects.keys()]) {
      if (!this.cores.get(id)) {
        this.projects.delete(id);
        this.read.delete(id);
      }
    }
    this.settle();
  }

  /** Reads every Core's Projects again, for Refresh. */
  reload() {
    for (const id of this.loading.keys()) this.reread.add(id);
    this.read.clear();
    this.sync();
  }

  /**
   * Makes a Project current because the person turned to it. A Project not
   * known yet was probably just created: the list is read again for it.
   */
  follow(coreId: string | undefined, projectId: string | undefined) {
    if (!coreId || !projectId) return;
    if (this.followed?.coreId === coreId && this.followed.projectId === projectId) return;
    this.followed = { coreId, projectId };
    void this.state.update(FOLLOWED_KEY, this.followed);
    const core = this.cores.get(coreId);
    if (this.projects.get(coreId)?.some((project) => project.id === projectId)) this.settle();
    else if (core?.ready) void this.load(core);
  }

  /**
   * Switch Project…: the Projects of every Core that can be asked in a Quick
   * Pick, grouped under each Core's name when there are several, each Core's
   * own Project first.
   */
  async choose(): Promise<void> {
    const cores = this.cores.ready();
    const several = this.cores.several;
    type Item = vscode.QuickPickItem & { value?: { core: Core; project: Project } };
    const items: Item[] = [];
    for (const core of cores) {
      let projects: Project[];
      try {
        projects = orderProjects(await core.client.projects(), core.projectSetting);
      } catch {
        continue;
      }
      this.projects.set(core.id, projects);
      if (projects.length === 0) continue;
      if (several) items.push({ label: core.name, kind: vscode.QuickPickItemKind.Separator });
      for (const project of projects) {
        const shown = this.shown?.core.id === core.id && this.shown.project.id === project.id;
        items.push({
          label: project.name,
          description: shown ? 'shown now' : isCurrentProject(project, core.projectSetting) ? 'default' : undefined,
          value: { core, project },
        });
      }
    }
    if (items.length === 0) {
      void vscode.window.showInformationMessage('There is no Project yet. Create one in Threavia first.');
      return;
    }
    const picked = await vscode.window.showQuickPick(items, {
      title: 'Switch Project',
      placeHolder: 'Which Project do Tasks and Memory show?',
      ignoreFocusOut: true,
      matchOnDescription: true,
    });
    if (!picked?.value) return;
    this.follow(picked.value.core.id, picked.value.project.id);
    this.settle();
  }

  private load(core: Core): Promise<void> {
    let loading = this.loading.get(core.id);
    if (loading) return loading;
    loading = core.client
      .projects()
      .then((projects) => {
        if (core.ready) this.projects.set(core.id, projects);
      })
      .catch(() => {
        // The views say Core did not answer when they ask it themselves; the
        // Project stays what it was until the next try.
      })
      .finally(() => {
        this.loading.delete(core.id);
        // An event can arrive while an older HTTP response is in flight.
        // Read once more before accepting that response as current.
        if (this.reread.delete(core.id) && core.ready) {
          void this.load(core);
          return;
        }
        // Settled before telling anyone, so a view drawn now trusts the answer;
        // told even without a Project, which a view says differently now.
        const first = core.ready && !this.read.has(core.id);
        if (core.ready) this.read.add(core.id);
        this.settle(first);
      });
    this.loading.set(core.id, loading);
    return loading;
  }

  /** Works out the Project shown; `always` tells the views even when it is the same one. */
  private settle(always = false) {
    const resolved = resolveCurrentAcross(
      this.cores.ready().map((core) => ({
        coreId: core.id,
        projects: this.projects.get(core.id) ?? [],
        setting: core.projectSetting,
      })),
      this.followed,
    );
    const core = resolved && this.cores.get(resolved.coreId);
    const next = core && resolved ? { core, project: resolved.project } : undefined;
    void vscode.commands.executeCommand('setContext', HAS_PROJECT_KEY, Boolean(next));
    const changed =
      next?.core.id !== this.shown?.core.id ||
      next?.project.id !== this.shown?.project.id ||
      next?.project.name !== this.shown?.project.name;
    this.shown = next;
    if (!changed && !always) return;
    for (const listener of this.listeners) listener();
  }

  dispose() {
    this.subscription.dispose();
    this.eventSubscription.dispose();
    this.listeners.clear();
  }
}
