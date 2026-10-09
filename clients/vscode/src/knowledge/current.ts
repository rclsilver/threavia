import * as vscode from 'vscode';

import type { CoreClient } from '../api/client';
import type { Project } from '../api/types';
import { currentProjectSetting } from '../config';
import { pick } from '../start/pickers';
import { isCurrentProject, orderProjects } from '../tree/model';
import { resolveCurrentProject } from './model';

/** The context key that hides New Task and Record Decision until there is a Project. */
const HAS_PROJECT_KEY = 'threavia.hasProject';

/**
 * The Project the Tasks and Memory views show, shared by both.
 *
 * It follows the person's attention rather than asking for it: the Session or
 * Project last selected in the Sessions view and the conversation in front
 * each make their Project current, and Switch Project… chooses one outright.
 * Until any of that happens, the `threavia.project` setting decides. What was
 * followed is not kept across a reload: the setting is the lasting choice.
 */
export class CurrentProject implements vscode.Disposable {
  private projects: Project[] = [];
  private followed: string | undefined;
  private shown: Project | undefined;
  private enabled = false;
  private read = false;
  private loading: Promise<void> | undefined;
  private readonly listeners = new Set<(project: Project | undefined) => void>();
  private readonly subscription: vscode.Disposable;

  constructor(private readonly client: CoreClient) {
    // A new setting is a choice made on purpose, so it outranks whatever was
    // followed before it.
    this.subscription = vscode.workspace.onDidChangeConfiguration((event) => {
      if (!event.affectsConfiguration('threavia.project')) return;
      this.followed = undefined;
      this.settle();
    });
  }

  get project(): Project | undefined {
    return this.shown;
  }

  /**
   * Whether the Projects were read at least once since Core could be asked:
   * until then, no Project means "not known yet" rather than "none exists".
   */
  get settled(): boolean {
    return this.read;
  }

  /** Told when the Project shown changes, or when there is none any more. */
  onChange(listener: (project: Project | undefined) => void): vscode.Disposable {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  }

  /** Reads the Projects once Core can be asked, and forgets them when it cannot. */
  setEnabled(enabled: boolean) {
    this.enabled = enabled;
    if (enabled) {
      void this.load();
    } else {
      this.projects = [];
      this.read = false;
      this.settle();
    }
  }

  /**
   * Makes a Project current because the person turned to it. A Project not
   * known yet was probably just created: the list is read again for it.
   */
  follow(projectId: string | undefined) {
    if (!projectId || projectId === this.followed) return;
    this.followed = projectId;
    if (this.projects.some((project) => project.id === projectId)) this.settle();
    else void this.load();
  }

  /** Switch Project…: the Projects in a Quick Pick, the setting's first. */
  async choose(): Promise<void> {
    const setting = currentProjectSetting();
    const projects = orderProjects(await this.client.projects(), setting);
    if (projects.length === 0) {
      void vscode.window.showInformationMessage('There is no Project yet. Create one in Threavia first.');
      return;
    }
    const project = await pick(
      'Switch Project',
      'Which Project do Tasks and Memory show?',
      projects.map((project) => ({
        label: project.name,
        description:
          project.id === this.shown?.id ? 'shown now' : isCurrentProject(project, setting) ? 'this workspace' : undefined,
        value: project,
      })),
    );
    if (!project) return;
    this.projects = projects;
    this.followed = project.id;
    this.settle();
  }

  private load(): Promise<void> {
    this.loading ??= this.client
      .projects()
      .then((projects) => {
        if (this.enabled) this.projects = projects;
      })
      .catch(() => {
        // The views say Core did not answer when they ask it themselves; the
        // Project stays what it was until the next try.
      })
      .finally(() => {
        this.loading = undefined;
        // Settled before telling anyone, so a view drawn now trusts the answer;
        // told even without a Project, which a view says differently now.
        const first = this.enabled && !this.read;
        if (this.enabled) this.read = true;
        this.settle(first);
      });
    return this.loading;
  }

  /** Works out the Project shown; `always` tells the views even when it is the same one. */
  private settle(always = false) {
    const next = this.enabled ? resolveCurrentProject(this.projects, currentProjectSetting(), this.followed) : undefined;
    void vscode.commands.executeCommand('setContext', HAS_PROJECT_KEY, Boolean(next));
    const changed = next?.id !== this.shown?.id || next?.name !== this.shown?.name;
    this.shown = next;
    if (!changed && !always) return;
    for (const listener of this.listeners) listener(next);
  }

  dispose() {
    this.subscription.dispose();
    this.listeners.clear();
  }
}
