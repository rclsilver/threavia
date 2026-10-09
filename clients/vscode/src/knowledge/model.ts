import type { NewDecisionBody, NewTaskBody } from '../api/client';
import type { Decision, DecisionImportance, Project, Task, TaskStatus } from '../api/types';
import { isCurrentProject, orderProjects } from '../tree/model';

/**
 * What the Tasks and Memory views show, decided without an editor.
 *
 * The rules are the web client's (web/ui/src/routes/tasks.tsx and the
 * decisions of web/ui/src/routes/project.tsx): Tasks grouped by where they
 * stand, with the one move that usually comes next; Decisions in the order
 * they are relied on. The tree providers only turn these into TreeItems.
 */

// -------------------------------------------------------------- the Project

/**
 * The Project both views show.
 *
 * The one last followed (a Session or a Project chosen in the Sessions view,
 * the conversation in front, or Switch Project…) wins, because it is what the
 * person is looking at now; then the one the `threavia.project` setting
 * names; then the first active one, so a person with a single Project never
 * has to choose it. A followed Project that is gone falls back the same way.
 */
export function resolveCurrentProject(
  projects: Project[],
  setting: string,
  followed: string | undefined,
): Project | undefined {
  const chosen = followed ? projects.find((project) => project.id === followed) : undefined;
  if (chosen) return chosen;
  const named = projects.find((project) => isCurrentProject(project, setting));
  if (named) return named;
  return orderProjects(projects, setting)[0];
}

// ------------------------------------------------------------------- tasks

/** Where a Task stands, as a row shows it: ready and waiting are derived. */
export type Standing = 'IN_PROGRESS' | 'READY' | 'WAITING' | 'DONE';

/**
 * Ready is Core's answer, not a computation here: a Task is ready when every
 * Task it waits on is done, and Core is the one that knows the whole graph.
 */
export function standingOf(task: Task, ready: ReadonlySet<string>): Standing {
  if (task.status === 'DONE') return 'DONE';
  if (task.status === 'IN_PROGRESS') return 'IN_PROGRESS';
  return ready.has(task.id) ? 'READY' : 'WAITING';
}

export interface TaskGroup {
  standing: Standing;
  title: string;
  tasks: Task[];
}

/** The groups in the web client's order, with its titles. */
export const GROUP_TITLES: Record<Standing, string> = {
  IN_PROGRESS: 'In progress',
  READY: 'Ready to start',
  WAITING: 'Waiting on other work',
  DONE: 'Done',
};

const GROUP_ORDER: Standing[] = ['IN_PROGRESS', 'READY', 'WAITING', 'DONE'];

/**
 * The Tasks in their groups, each keeping Core's order. An open group with
 * nothing in it is left out, as on the web page; Done is always there, folded
 * and counted, so finished work stays one click away.
 */
export function groupTasks(tasks: Task[], ready: ReadonlySet<string>): TaskGroup[] {
  return GROUP_ORDER.map((standing) => ({
    standing,
    title: GROUP_TITLES[standing],
    tasks: tasks.filter((task) => standingOf(task, ready) === standing),
  })).filter((group) => group.standing === 'DONE' || group.tasks.length > 0);
}

/** The statuses a person sets, with the web client's words. */
export const STATUSES: { status: TaskStatus; label: string }[] = [
  { status: 'TODO', label: 'To do' },
  { status: 'IN_PROGRESS', label: 'In progress' },
  { status: 'DONE', label: 'Done' },
];

export interface NextStep {
  status: TaskStatus;
  label: string;
}

/**
 * The one move a row offers outright, named as a verb. Start and Mark done
 * are the web client's; Reopen is the editor's, since its Done group is a
 * list of rows rather than a page with a status menu on each mark. A Task
 * waiting on other work has none: starting it is possible, but rarely meant.
 */
export function nextStep(standing: Standing): NextStep | undefined {
  switch (standing) {
    case 'READY':
      return { status: 'IN_PROGRESS', label: 'Start' };
    case 'IN_PROGRESS':
      return { status: 'DONE', label: 'Mark done' };
    case 'DONE':
      return { status: 'TODO', label: 'Reopen' };
    case 'WAITING':
      return undefined;
  }
}

/** The status the person would see a row as, for its tooltip and preview. */
export function statusLabel(status: TaskStatus): string {
  return STATUSES.find((entry) => entry.status === status)?.label ?? 'Cancelled';
}

/**
 * Whether making `task` wait on `blocker` would close a loop. Core refuses
 * such an edge, because nothing on a cycle is ever ready; the whole graph is
 * already known, so the choice is simply not offered.
 */
export function wouldCycle(tasks: Task[], task: Task, blocker: Task): boolean {
  const byId = new Map(tasks.map((entry) => [entry.id, entry]));
  const seen = new Set<string>();
  const walk = (id: string): boolean => {
    if (id === task.id) return true;
    if (seen.has(id)) return false;
    seen.add(id);
    return (byId.get(id)?.dependsOn ?? []).some(walk);
  };
  return walk(blocker.id);
}

/**
 * What a Task may wait on: the other open Tasks that would not close a loop,
 * and whatever it already waits on, so that can be unticked too. With no
 * `task`, the choice for a new one, which nothing waits on yet.
 */
export function dependencyOptions(tasks: Task[], task?: Task): Task[] {
  const waitsOn = new Set(task?.dependsOn ?? []);
  return tasks.filter((entry) => {
    if (entry.id === task?.id) return false;
    if (waitsOn.has(entry.id)) return true;
    if (entry.status === 'DONE') return false;
    return !task || !wouldCycle(tasks, task, entry);
  });
}

/** The edges to add and to remove to go from what a Task waits on to what was ticked. */
export function dependencyChanges(
  current: readonly string[],
  chosen: readonly string[],
): { add: string[]; remove: string[] } {
  return {
    add: chosen.filter((id) => !current.includes(id)),
    remove: current.filter((id) => !chosen.includes(id)),
  };
}

/** What a Task waits on that is not done yet: what actually holds it back. */
export function blockersOf(task: Task, tasks: Task[]): string[] {
  return (task.dependsOn ?? []).map((id) => {
    const blocker = tasks.find((entry) => entry.id === id);
    return blocker ? (blocker.status === 'DONE' ? '' : blocker.title) : 'a task outside this project';
  }).filter(Boolean);
}

export function newTaskBody(title: string, details: string, dependsOn: string[]): NewTaskBody {
  return { title: title.trim(), description: details.trim(), dependsOn };
}

/**
 * The patch an edit sends, or nothing when there is nothing to send. Core
 * keeps a field it is sent empty, so emptying one is not a change it can make.
 */
export function taskEdit(task: Task, field: 'title' | 'description', value: string): { title: string } | { description: string } | undefined {
  const next = value.trim();
  if (!next || next === (task[field] ?? '').trim()) return undefined;
  return field === 'title' ? { title: next } : { description: next };
}

/** A Task as its read-only preview shows it: what it is, where it stands, what it waits on. */
export function taskMarkdown(task: Task, tasks: Task[], ready: ReadonlySet<string>): string {
  const lines = [`# ${task.title}`, '', `**${GROUP_TITLES[standingOf(task, ready)]}**`, ''];
  if (task.description?.trim()) lines.push(task.description.trim(), '');
  const waitsOn = task.dependsOn ?? [];
  if (waitsOn.length > 0) {
    lines.push('## Waits on', '');
    for (const id of waitsOn) {
      const blocker = tasks.find((entry) => entry.id === id);
      if (!blocker) lines.push('- a task outside this project');
      else lines.push(`- ${blocker.status === 'DONE' ? '✓ ' : ''}${blocker.title}${blocker.status === 'DONE' ? ' (done)' : ''}`);
    }
    lines.push('');
  }
  return lines.join('\n');
}

// --------------------------------------------------------------- decisions

export type DecisionGroupKind = 'IMPORTANT' | 'NORMAL' | 'SUPERSEDED';

export interface DecisionGroup {
  kind: DecisionGroupKind;
  title: string;
  decisions: Decision[];
}

export const TRAVELS = 'Travels with every Job';
export const ON_RECORD = 'On record';
export const SUPERSEDED = 'Superseded';

/**
 * The Project's memory in the order it is relied on: what travels with every
 * Job, then what is on record, then what was replaced, folded at the end. A
 * group with nothing in it is left out; each keeps Core's order.
 */
export function groupDecisions(decisions: Decision[]): DecisionGroup[] {
  const current = decisions.filter((decision) => decision.status !== 'SUPERSEDED');
  const groups: DecisionGroup[] = [
    { kind: 'IMPORTANT', title: TRAVELS, decisions: current.filter((decision) => decision.importance === 'IMPORTANT') },
    { kind: 'NORMAL', title: ON_RECORD, decisions: current.filter((decision) => decision.importance !== 'IMPORTANT') },
    {
      kind: 'SUPERSEDED',
      title: SUPERSEDED,
      decisions: decisions.filter((decision) => decision.status === 'SUPERSEDED'),
    },
  ];
  return groups.filter((group) => group.decisions.length > 0);
}

/** The Decision that replaced this one, when it is listed. */
export function replacementOf(decision: Decision, decisions: Decision[]): Decision | undefined {
  return decisions.find((entry) => entry.supersedes === decision.id);
}

/**
 * Pinning is a toggle: an important Decision goes back on record, one on
 * record travels with every Job.
 */
export function toggledImportance(decision: Decision): DecisionImportance {
  return decision.importance === 'IMPORTANT' ? 'NORMAL' : 'IMPORTANT';
}

export function newDecisionBody(
  title: string,
  content: string,
  importance: DecisionImportance,
  supersedes?: string,
): NewDecisionBody {
  const body: NewDecisionBody = { title: title.trim(), content: content.trim(), importance };
  if (supersedes) body.supersedes = supersedes;
  return body;
}

/**
 * Who recorded it, said only when it was an agent: an important Decision
 * travels into every later run, so one an agent recorded on its own has to
 * read as such, not as the person's ruling.
 */
export function recordedByAgent(decision: Decision): boolean {
  return Boolean(decision.createdByJobId);
}

/** A Decision as its read-only preview shows it. */
export function decisionMarkdown(decision: Decision, decisions: Decision[]): string {
  const facts = [
    decision.status === 'SUPERSEDED'
      ? SUPERSEDED
      : decision.importance === 'IMPORTANT'
        ? TRAVELS
        : ON_RECORD,
    recordedByAgent(decision) ? 'recorded by an agent' : undefined,
    `recorded ${decision.createdAt.slice(0, 10)}`,
  ].filter(Boolean);
  const lines = [`# ${decision.title}`, '', `*${facts.join(' · ')}*`, ''];
  if (decision.content?.trim()) lines.push(decision.content.trim(), '');
  const replacement = replacementOf(decision, decisions);
  if (replacement) lines.push(`Superseded by **${replacement.title}**.`, '');
  const replaced = decision.supersedes ? decisions.find((entry) => entry.id === decision.supersedes) : undefined;
  if (replaced) lines.push(`Supersedes **${replaced.title}**.`, '');
  return lines.join('\n');
}

/** A file name for a preview's tab: the title, without what a path cannot hold. */
export function documentName(title: string): string {
  const name = title.replace(/[\\/:*?"<>|#%\n\r\t]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 80);
  return `${name || 'Untitled'}.md`;
}

/** The start of a long text, for a tooltip: the whole of it is one click away. */
export function excerpt(text: string, max = 600): string {
  const trimmed = text.trim();
  return trimmed.length > max ? `${trimmed.slice(0, max - 1)}…` : trimmed;
}
