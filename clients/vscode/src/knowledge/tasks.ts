import * as vscode from 'vscode';

import type { CoreClient } from '../api/client';
import type { EventBus } from '../api/events';
import type { Task, TaskStatus } from '../api/types';
import { oneLine } from '../tree/model';
import type { CurrentProject } from './current';
import { LiveDocuments } from './documents';
import {
  GROUP_TITLES,
  blockersOf,
  dependencyChanges,
  dependencyOptions,
  excerpt,
  documentName,
  groupTasks,
  newTaskBody,
  nextStep,
  statusLabel,
  taskEdit,
  taskMarkdown,
  type Standing,
  type TaskGroup,
} from './model';
import { ProjectView, type MessageNode } from './view';

interface TaskData {
  tasks: Task[];
  ready: Set<string>;
}

export type TaskNode = { type: 'group'; group: TaskGroup } | { type: 'task'; task: Task; standing: Standing };
type TaskRow = Extract<TaskNode, { type: 'task' }>;

/** A Task's row says where it stands by shape, as the web page's marks do. */
const MARKS: Record<Standing, { icon: string; color?: string }> = {
  READY: { icon: 'circle-large-outline' },
  IN_PROGRESS: { icon: 'record', color: 'charts.blue' },
  WAITING: { icon: 'lock' },
  DONE: { icon: 'pass', color: 'testing.iconPassed' },
};

/** The row's context value, which the menus in package.json match on. */
const CONTEXT: Record<Standing, string> = {
  READY: 'task.ready',
  IN_PROGRESS: 'task.inProgress',
  WAITING: 'task.waiting',
  DONE: 'task.done',
};

const NEW_TASK = 'New Task';

/**
 * The Tasks view: what the current Project still owes, grouped by where each
 * Task stands, with the next step inline and everything else in the menu.
 *
 * Agents file most Tasks, so the view is for steering what exists — what can
 * start, what waits on what — and filing one is a button in its title bar.
 */
export class TasksView extends ProjectView<TaskData, TaskNode> {
  private readonly documents = new LiveDocuments(
    'threavia-task',
    'This task is no longer in the list. Open it again from the Tasks view.\n',
  );
  private known: { projectId: string; ids: Set<string> } | undefined;

  constructor(
    private readonly client: CoreClient,
    current: CurrentProject,
    bus: EventBus,
  ) {
    super('threavia.tasks', current, bus, 'tasks');
  }

  protected async fetch(projectId: string): Promise<TaskData> {
    // One list with finished work included: a Task waiting on one that is done
    // is the normal way of being ready, and the Done group counts them.
    const [tasks, ready] = await Promise.all([
      this.client.tasks(projectId, true),
      this.client.readyTasks(projectId),
    ]);
    return { tasks, ready: new Set(ready.map((task) => task.id)) };
  }

  protected loaded(projectId: string, data: TaskData) {
    const texts = new Map<string, string | undefined>();
    if (this.known?.projectId === projectId) for (const id of this.known.ids) texts.set(id, undefined);
    for (const task of data.tasks) texts.set(task.id, taskMarkdown(task, data.tasks, data.ready));
    this.known = { projectId, ids: new Set(data.tasks.map((task) => task.id)) };
    this.documents.update(texts);
  }

  protected children(data: TaskData, node: TaskNode | undefined): (TaskNode | MessageNode)[] {
    if (node?.type === 'task') return [];
    const groups = groupTasks(data.tasks, data.ready);
    if (node?.type === 'group') {
      const { standing } = node.group;
      const group = groups.find((entry) => entry.standing === standing);
      return (group?.tasks ?? []).map((task): TaskNode => ({ type: 'task', task, standing }));
    }
    const open = groups.filter((group) => group.standing !== 'DONE');
    return [
      ...(open.length === 0
        ? [{ type: 'message', text: 'Nothing open. Ask an agent for something, or file a task with New Task.' } as const]
        : []),
      ...groups.map((group): TaskNode => ({ type: 'group', group })),
    ];
  }

  protected item(node: TaskNode): vscode.TreeItem {
    if (node.type === 'group') {
      const { group } = node;
      // Finished work stays one click away, where the list ends, and counted
      // so the click is not a guess.
      const item = new vscode.TreeItem(
        group.title,
        group.standing === 'DONE' ? vscode.TreeItemCollapsibleState.Collapsed : vscode.TreeItemCollapsibleState.Expanded,
      );
      item.id = `group:${group.standing}`;
      item.description = String(group.tasks.length);
      return item;
    }
    return this.taskItem(node);
  }

  private taskItem(node: TaskRow): vscode.TreeItem {
    const { task, standing } = node;
    const all = this.data?.tasks ?? [];
    const item = new vscode.TreeItem(task.title, vscode.TreeItemCollapsibleState.None);
    item.id = `task:${task.id}`;
    const mark = MARKS[standing];
    item.iconPath = new vscode.ThemeIcon(mark.icon, mark.color ? new vscode.ThemeColor(mark.color) : undefined);
    item.contextValue = CONTEXT[standing];
    // What holds a waiting Task back is named, not counted: "depends on 2
    // tasks" says nothing a person can act on.
    const blockers = standing === 'WAITING' ? blockersOf(task, all) : [];
    item.description =
      blockers.length > 0 ? `waits on ${blockers.join(', ')}` : task.description ? oneLine(task.description, 80) : undefined;
    item.tooltip = tooltip(task, standing, all);
    item.command = { command: 'threavia.openTask', title: 'Open Task', arguments: [task.id] };
    return item;
  }

  // ---------------------------------------------------------------- commands

  register(command: (name: string, run: (...args: never[]) => unknown) => void, disposables: vscode.Disposable[]) {
    disposables.push(this.documents);
    command('threavia.openTask', (id: string) => this.open(id));
    command('threavia.newTask', () => this.create());
    // Start, Mark done and Reopen are one move each, shown inline on the rows
    // whose standing they follow.
    const next = (node: TaskRow) => {
      const step = nextStep(node.standing);
      if (step) return this.setStatus(node, step.status);
    };
    command('threavia.startTask', next);
    command('threavia.completeTask', next);
    command('threavia.reopenTask', next);
    command('threavia.setTaskTodo', (node: TaskRow) => this.setStatus(node, 'TODO'));
    command('threavia.setTaskInProgress', (node: TaskRow) => this.setStatus(node, 'IN_PROGRESS'));
    command('threavia.setTaskDone', (node: TaskRow) => this.setStatus(node, 'DONE'));
    command('threavia.editTaskTitle', (node: TaskRow) => this.editTitle(node.task));
    command('threavia.editTaskDetails', (node: TaskRow) => this.editDetails(node.task));
    command('threavia.taskDependencies', (node: TaskRow) => this.dependencies(node.task));
    command('threavia.deleteTask', (node: TaskRow) => this.remove(node.task));
  }

  private async open(id: string) {
    const task = (await this.dataNow())?.tasks.find((entry) => entry.id === id);
    await this.documents.open(id, documentName(task?.title ?? 'Task'));
  }

  private setStatus(node: TaskRow, status: TaskStatus) {
    if (node.task.status === status) return;
    return this.act('The task is unchanged', () => this.client.updateTask(node.task.id, { status }));
  }

  /**
   * Filing a Task: a title, what it is about, and what has to happen first,
   * asked one after the other. The moment a Task is filed is when the person
   * knows what it waits on, not after hunting for the row it landed in.
   */
  private async create() {
    const project = this.current.project;
    if (!project) {
      void vscode.window.showInformationMessage('Choose a Project first, with Switch Project….');
      return;
    }
    const title = await vscode.window.showInputBox({
      title: `${NEW_TASK} in ${project.name} (1/3)`,
      placeHolder: 'What needs doing?',
      ignoreFocusOut: true,
      validateInput: (value) => (value.trim() ? undefined : 'A task needs a title.'),
    });
    if (title === undefined) return;
    const details = await vscode.window.showInputBox({
      title: `${NEW_TASK} (2/3)`,
      placeHolder: 'Details, for whoever picks it up (optional)',
      prompt: 'Press Enter with nothing written to skip.',
      ignoreFocusOut: true,
    });
    if (details === undefined) return;

    const data = await this.dataNow();
    // A new Task cannot close a loop: nothing waits on it yet.
    const options = dependencyOptions(data?.tasks ?? []);
    let dependsOn: string[] = [];
    if (options.length > 0) {
      const picked = await vscode.window.showQuickPick(
        options.map((task) => ({ label: task.title, description: statusLabel(task.status), id: task.id })),
        {
          title: `${NEW_TASK} (3/3): Waits on`,
          placeHolder: 'Tick every task that must come first, or press Enter to skip',
          canPickMany: true,
          ignoreFocusOut: true,
          matchOnDescription: true,
        },
      );
      if (picked === undefined) return;
      dependsOn = picked.map((entry) => entry.id);
    }

    const body = newTaskBody(title, details, dependsOn);
    if (await this.act('Not filed', () => this.client.createTask(project.id, body), true)) {
      vscode.window.setStatusBarMessage(`Filed: ${body.title}.`, 4000);
    }
  }

  private async editTitle(task: Task) {
    const value = await vscode.window.showInputBox({
      title: 'Edit the title',
      value: task.title,
      ignoreFocusOut: true,
      validateInput: (input) => (input.trim() ? undefined : 'A task needs a title.'),
    });
    const patch = value === undefined ? undefined : taskEdit(task, 'title', value);
    if (patch) await this.act('The task is unchanged', () => this.client.updateTask(task.id, patch));
  }

  private async editDetails(task: Task) {
    const current = task.description ?? '';
    const value = await vscode.window.showInputBox({
      title: `Edit the details: ${task.title}`,
      value: current,
      placeHolder: 'Details, for whoever picks it up',
      // The box holds one line; details written over several lose their
      // breaks only if they are changed here.
      prompt: current.includes('\n') ? 'Edited here, the details become one paragraph.' : undefined,
      ignoreFocusOut: true,
      validateInput: (input) =>
        input.trim() || !current.trim() ? undefined : 'Details cannot be emptied, only rewritten.',
    });
    if (value === undefined) return;
    // The box shows details over several lines as one; sent back untouched,
    // they would lose their breaks for nothing.
    if (value.trim() === current.replace(/\r?\n/g, '').trim()) return;
    const patch = taskEdit(task, 'description', value);
    if (patch) await this.act('The task is unchanged', () => this.client.updateTask(task.id, patch));
  }

  /**
   * What a Task waits on, ticked and unticked in one go: a Task often waits on
   * several others, and one trip through a menu per edge made that tedious.
   */
  private async dependencies(task: Task) {
    const all = (await this.dataNow())?.tasks ?? [];
    const options = dependencyOptions(all, task);
    if (options.length === 0) {
      void vscode.window.showInformationMessage('There is no other open task to wait on.');
      return;
    }
    const waitsOn = task.dependsOn ?? [];
    const picked = await vscode.window.showQuickPick(
      options.map((entry) => ({
        label: entry.title,
        description: statusLabel(entry.status),
        id: entry.id,
        picked: waitsOn.includes(entry.id),
      })),
      {
        title: `Dependencies: ${task.title}`,
        placeHolder: 'Waits on — tick every task that must come first',
        canPickMany: true,
        ignoreFocusOut: true,
        matchOnDescription: true,
      },
    );
    if (picked === undefined) return;
    const { add, remove } = dependencyChanges(
      waitsOn,
      picked.map((entry) => entry.id),
    );
    if (add.length === 0 && remove.length === 0) return;
    await this.act('Its dependencies are unchanged', async () => {
      for (const id of add) await this.client.addTaskDependency(task.id, id);
      for (const id of remove) await this.client.removeTaskDependency(task.id, id);
    });
  }

  private async remove(task: Task) {
    const choice = await vscode.window.showWarningMessage(
      'Delete this task?',
      { modal: true, detail: `“${task.title}” leaves the list. What was done about it stays in the timeline.` },
      'Delete',
    );
    if (choice === 'Delete') await this.act('Not deleted', () => this.client.deleteTask(task.id));
  }
}

function tooltip(task: Task, standing: Standing, all: Task[]): vscode.MarkdownString {
  const markdown = new vscode.MarkdownString(undefined, true);
  markdown.appendMarkdown(`**${escape(task.title)}**\n\n`);
  markdown.appendMarkdown(`$(${MARKS[standing].icon}) ${GROUP_TITLES[standing]}`);
  if (task.description?.trim()) markdown.appendMarkdown(`\n\n${escape(excerpt(task.description))}`);
  const waitsOn = (task.dependsOn ?? []).map((id) => all.find((entry) => entry.id === id));
  if (waitsOn.length > 0) {
    markdown.appendMarkdown('\n\nWaits on:');
    for (const blocker of waitsOn) {
      markdown.appendMarkdown(
        `\n- ${blocker ? `${blocker.status === 'DONE' ? '$(pass) ' : ''}${escape(blocker.title)}` : 'a task outside this project'}`,
      );
    }
  }
  return markdown;
}

function escape(text: string): string {
  return text.replace(/[\\`*_{}[\]()#+\-.!|<>]/g, '\\$&');
}
