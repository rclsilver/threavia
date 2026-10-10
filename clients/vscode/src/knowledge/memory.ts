import * as vscode from 'vscode';

import type { Core } from '../cores/core';
import { recordRefOf } from '../cores/refs';
import type { Cores } from '../cores/registry';
import type { Decision, DecisionImportance } from '../api/types';
import { ago, oneLine } from '../tree/model';
import type { CurrentProject } from './current';
import { LiveDocuments } from './documents';
import {
  ON_RECORD,
  excerpt,
  TRAVELS,
  decisionMarkdown,
  documentName,
  groupDecisions,
  newDecisionBody,
  recordedByAgent,
  replacementOf,
  toggledImportance,
  type DecisionGroup,
  type DecisionGroupKind,
} from './model';
import { ProjectView, type MessageNode } from './view';

export type DecisionNode =
  | { type: 'group'; group: DecisionGroup }
  | { type: 'decision'; coreId: string; decision: Decision; kind: DecisionGroupKind };
type DecisionRow = Extract<DecisionNode, { type: 'decision' }>;

/** The row's context value, which the menus in package.json match on. */
const CONTEXT: Record<DecisionGroupKind, string> = {
  IMPORTANT: 'decision.important',
  NORMAL: 'decision.normal',
  SUPERSEDED: 'decision.superseded',
};

const ICONS: Record<DecisionGroupKind, { icon: string; color?: string }> = {
  IMPORTANT: { icon: 'pinned', color: 'list.warningForeground' },
  NORMAL: { icon: 'book' },
  SUPERSEDED: { icon: 'history', color: 'disabledForeground' },
};

const RECORD = 'Record Decision';

/**
 * The Memory view: the current Project's decisions, in the order they are
 * relied on — what travels with every Job, what is on record — and, folded at
 * the end, what was superseded.
 *
 * What was decided is never edited: a decision that changed is superseded by
 * a new one, which keeps the history, and deleting is for one that should
 * never have been recorded.
 */
export class MemoryView extends ProjectView<Decision[], DecisionNode> {
  private readonly documents = new LiveDocuments(
    'threavia-decision',
    'This decision is no longer recorded. Open it again from the Memory view.\n',
  );
  /** The Decisions last read, of a Project named `<core>/<project>`. */
  private known: { project: string; ids: Set<string> } | undefined;

  constructor(current: CurrentProject, cores: Cores) {
    super('threavia.memory', current, cores, 'decisions');
  }

  protected fetch(core: Core, projectId: string): Promise<Decision[]> {
    return core.client.decisions(projectId, true);
  }

  protected loaded(core: Core, projectId: string, decisions: Decision[]) {
    const texts = new Map<string, string | undefined>();
    const project = `${core.id}/${projectId}`;
    if (this.known?.project === project) for (const id of this.known.ids) texts.set(id, undefined);
    for (const decision of decisions) texts.set(decision.id, decisionMarkdown(decision, decisions));
    this.known = { project, ids: new Set(decisions.map((decision) => decision.id)) };
    this.documents.update(core.id, texts);
  }

  protected children(decisions: Decision[], node: DecisionNode | undefined): (DecisionNode | MessageNode)[] {
    if (node?.type === 'decision') return [];
    const groups = groupDecisions(decisions);
    if (node?.type === 'group') {
      const { kind } = node.group;
      const group = groups.find((entry) => entry.kind === kind);
      return (group?.decisions ?? []).map((decision): DecisionNode => ({ type: 'decision', coreId: this.current.core?.id ?? '', decision, kind }));
    }
    if (groups.length === 0) {
      return [
        {
          type: 'message',
          text: 'Nothing recorded yet. Agents record what they decide as they work; a decision of your own goes in with Record Decision.',
        },
      ];
    }
    return groups.map((group): DecisionNode => ({ type: 'group', group }));
  }

  protected item(node: DecisionNode): vscode.TreeItem {
    if (node.type === 'group') {
      const { group } = node;
      const item = new vscode.TreeItem(
        group.title,
        group.kind === 'SUPERSEDED' ? vscode.TreeItemCollapsibleState.Collapsed : vscode.TreeItemCollapsibleState.Expanded,
      );
      item.id = `group:${group.kind}`;
      item.description = String(group.decisions.length);
      if (group.kind === 'IMPORTANT') item.tooltip = 'The agent reads these before it starts each Job.';
      return item;
    }
    return this.decisionItem(node);
  }

  private decisionItem(node: DecisionRow): vscode.TreeItem {
    const { decision, kind } = node;
    const item = new vscode.TreeItem(decision.title, vscode.TreeItemCollapsibleState.None);
    item.id = `decision:${decision.id}`;
    const mark = ICONS[kind];
    item.iconPath = new vscode.ThemeIcon(mark.icon, mark.color ? new vscode.ThemeColor(mark.color) : undefined);
    item.contextValue = CONTEXT[kind];
    const replacement = kind === 'SUPERSEDED' ? replacementOf(decision, this.data ?? []) : undefined;
    item.description =
      [
        recordedByAgent(decision) ? 'agent' : undefined,
        replacement ? `by ${replacement.title}` : ago(decision.createdAt),
      ]
        .filter(Boolean)
        .join(' · ') || undefined;
    item.tooltip = tooltip(decision, kind);
    item.command = { command: 'threavia.openDecision', title: 'Open Decision', arguments: [{ coreId: node.coreId, id: decision.id }] };
    return item;
  }

  // ---------------------------------------------------------------- commands

  register(command: (name: string, run: (...args: never[]) => unknown) => void, disposables: vscode.Disposable[]) {
    disposables.push(this.documents);
    command('threavia.openDecision', (target: unknown) => this.open(target));
    command('threavia.recordDecision', () => this.record());
    command('threavia.pinDecision', (node: DecisionRow) => this.pin(node.coreId, node.decision));
    command('threavia.unpinDecision', (node: DecisionRow) => this.pin(node.coreId, node.decision));
    command('threavia.supersedeDecision', (node: DecisionRow) => this.record(node.decision, node.coreId));
    command('threavia.deleteDecision', (node: DecisionRow) => this.remove(node.coreId, node.decision));
  }

  /** Opens a Decision: `{ coreId, id }` from a row, or an id alone from an earlier version. */
  private async open(target: unknown) {
    const ref = recordRefOf(target, this.current.core?.id);
    if (!ref) return;
    const decision =
      ref.coreId === this.current.core?.id ? (await this.dataNow())?.find((entry) => entry.id === ref.id) : undefined;
    await this.documents.open(ref.coreId, ref.id, documentName(decision?.title ?? 'Decision'));
  }

  private pin(coreId: string, decision: Decision) {
    const importance = toggledImportance(decision);
    return this.act(importance === 'IMPORTANT' ? 'Not pinned' : 'Not unpinned', () =>
      this.clientOf(coreId).setDecisionImportance(decision.id, importance),
    );
  }

  /**
   * Recording a decision, or one that supersedes `replaced`: what was
   * decided, why, and whether it travels with every Job, asked one after the
   * other. Each answer is a line: the why of a decision is a sentence or two,
   * and a document to write it in would be one more tab to find and close.
   */
  private async record(replaced?: Decision, coreId?: string) {
    const { project, core } = this.current;
    if (!project || !core) {
      void vscode.window.showInformationMessage('Choose a Project first, with Switch Project….');
      return;
    }
    const heading = replaced ? `Supersede “${oneLine(replaced.title, 60)}”` : `${RECORD} in ${project.name}`;
    const title = await vscode.window.showInputBox({
      title: `${heading} (1/3)`,
      placeHolder: 'What was decided?',
      value: replaced?.title,
      ignoreFocusOut: true,
      validateInput: (value) => (value.trim() ? undefined : 'A decision needs a title.'),
    });
    if (title === undefined) return;
    const content = await vscode.window.showInputBox({
      title: `${heading} (2/3)`,
      placeHolder: 'Why, and what it rules out (optional)',
      prompt: 'Press Enter with nothing written to skip.',
      ignoreFocusOut: true,
    });
    if (content === undefined) return;
    const importance = await pickImportance(heading, replaced?.importance ?? 'NORMAL');
    if (importance === undefined) return;

    const body = newDecisionBody(title, content, importance, replaced?.id);
    if (await this.act(replaced ? 'Not superseded' : 'Not recorded', () => this.clientOf(coreId ?? core.id).createDecision(project.id, body), true)) {
      vscode.window.setStatusBarMessage(`Recorded: ${body.title}.`, 4000);
    }
  }

  private async remove(coreId: string, decision: Decision) {
    const choice = await vscode.window.showWarningMessage(
      'Delete this decision?',
      {
        modal: true,
        detail: `“${decision.title}” is for one that should never have been recorded; a decision that changed is superseded instead. Whatever it had superseded becomes current again.`,
      },
      'Delete',
    );
    if (choice === 'Delete') await this.act('Not deleted', () => this.clientOf(coreId).deleteDecision(decision.id));
  }
}

/** Whether it travels with every Job, said as what it does; the current choice first. */
async function pickImportance(heading: string, preselected: DecisionImportance): Promise<DecisionImportance | undefined> {
  const items: (vscode.QuickPickItem & { value: DecisionImportance })[] = [
    { label: '$(book) Normal', description: ON_RECORD, value: 'NORMAL' },
    { label: `$(pinned) ${TRAVELS}`, description: 'the agent reads it before it starts', value: 'IMPORTANT' },
  ];
  items.sort((a, b) => Number(b.value === preselected) - Number(a.value === preselected));
  const picked = await vscode.window.showQuickPick(items, {
    title: `${heading} (3/3)`,
    placeHolder: 'Does it travel with every Job?',
    ignoreFocusOut: true,
  });
  return picked?.value;
}

function tooltip(decision: Decision, kind: DecisionGroupKind): vscode.MarkdownString {
  const markdown = new vscode.MarkdownString(undefined, true);
  markdown.appendMarkdown(`**${escape(decision.title)}**\n\n`);
  const facts = [
    kind === 'IMPORTANT' ? `$(pinned) ${TRAVELS}` : kind === 'NORMAL' ? ON_RECORD : 'Superseded',
    recordedByAgent(decision) ? 'recorded by an agent during a job, not by a person' : undefined,
    ago(decision.createdAt),
  ].filter(Boolean);
  markdown.appendMarkdown(facts.join(' · '));
  if (decision.content?.trim()) markdown.appendMarkdown(`\n\n${escape(excerpt(decision.content))}`);
  return markdown;
}

function escape(text: string): string {
  return text.replace(/[\\`*_{}[\]()#+\-.!|<>]/g, '\\$&');
}
