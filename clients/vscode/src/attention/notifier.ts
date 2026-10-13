import * as vscode from 'vscode';

import type { UserInputRequest, ValidationRequest } from '../api/types';
import { notifyAttention, notifyJobEnded } from '../config';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import { isSessionOpen, openSession } from '../sessions';
import { oneLine, UNTITLED, validationAsk, waitingCount, waitingSummary } from '../tree/model';

/** At most this many choices are offered directly; more go behind "Answer…". */
const CHOICE_BUTTONS = 3;
const CHOICE_BUTTON_LENGTH = 24;
const ACTION_COMMAND = 'threavia.notificationAction';

interface NotificationAction {
  coreId: string;
  requestId: string;
  action: 'approve' | 'deny' | 'answer' | 'reply' | 'open';
  value?: string;
}

function escapeMarkdown(value: string) {
  return value.replace(/[\\`*_{}[\]()<>!]/g, '\\$&');
}

/**
 * Tells the person when something waits for them, once per request, and when
 * work ends in a Session they are not looking at, on every Core. With several
 * Cores, each message starts with the name of the one it comes from.
 */
export class Notifier implements vscode.Disposable {
  private readonly disposables: vscode.Disposable[] = [];
  private readonly notifications = new Map<string, { coreId: string; requestId: string; close: () => void }>();

  constructor(
    private readonly cores: Cores,
    private readonly titleOf: (core: Core, sessionId: string) => Promise<string>,
  ) {
    this.disposables.push(
      cores.onAttention((core) => this.announce(core)),
      cores.onDidRemove((coreId) => {
        for (const [key, notification] of this.notifications) if (notification.coreId === coreId) this.close(key);
      }),
      vscode.commands.registerCommand(ACTION_COMMAND, (action: NotificationAction) => this.act(action)),
      cores.on('effect', (core, effect) => {
        if (effect.kind === 'jobEnded') void this.jobEnded(core, effect.sessionId, effect.failed);
      }),
    );
  }

  /** Announces what arrived since the last look, including what waited at start. */
  private announce(core: Core) {
    const { validations, userInputs } = core.attention.current;
    const pending = new Set([...validations, ...userInputs].map((request) => request.id));
    for (const [key, notification] of this.notifications) {
      if (notification.coreId === core.id && !pending.has(notification.requestId)) this.close(key);
    }
    // An empty list before the first answer is "not known yet", and taking it
    // would forget everything already announced.
    if (!core.attention.ready) return;
    const fresh = new Set(this.cores.ledgers.take(core.id, [...validations, ...userInputs].map((request) => request.id)));
    void this.cores.saveLedgers();
    if (!notifyAttention()) return;

    for (const request of validations) if (fresh.has(request.id)) void this.validation(core, request);
    for (const request of userInputs) if (fresh.has(request.id)) void this.userInput(core, request);
  }

  private validation(core: Core, request: ValidationRequest) {
    const { tool, headline } = validationAsk(request);
    const ask = oneLine(headline ? `${tool ? `${tool}: ` : ''}${headline}` : request.summary || request.title, 160);
    const title = request.context?.sessionTitle || UNTITLED;
    this.showRequest(core, request, `Approval needed · ${title} — ${ask}`, [
      { label: 'Approve', action: 'approve' }, { label: 'Deny', action: 'deny' }, { label: 'Open Session', action: 'open' },
    ]);
  }

  private userInput(core: Core, request: UserInputRequest) {
    const title = request.context?.sessionTitle || UNTITLED;
    const choices = request.choices ?? [];
    // A few short choices answer from the notification itself; anything longer
    // would crowd the notification, so it goes through the pick.
    const buttons =
      choices.length > 0 &&
      choices.length <= CHOICE_BUTTONS &&
      choices.every((choice) => choice.length <= CHOICE_BUTTON_LENGTH)
        ? choices
        : [];
    this.showRequest(core, request, `Question · ${title} — ${oneLine(request.prompt, 160)}`, [
      ...buttons.map((value) => ({ label: value, action: 'reply' as const, value })),
      { label: 'Answer…', action: 'answer' }, { label: 'Open Session', action: 'open' },
    ]);
  }

  /** Progress notifications have a lifetime; ordinary message toasts cannot be closed by the extension. */
  private showRequest(core: Core, request: UserInputRequest | ValidationRequest, title: string, actions: { label: string; action: NotificationAction['action']; value?: string }[]) {
    const key = JSON.stringify([core.id, request.id]);
    if (this.notifications.has(key)) return;
    const done = new Promise<void>((close) => this.notifications.set(key, { coreId: core.id, requestId: request.id, close }));
    const links = actions.map(({ label, action, value }) => {
      const args = encodeURIComponent(JSON.stringify([{ coreId: core.id, requestId: request.id, action, value }]))
        .replace(/[!'()*]/g, (character) => `%${character.charCodeAt(0).toString(16).toUpperCase()}`);
      return `[${escapeMarkdown(label)}](command:${ACTION_COMMAND}?${args})`;
    });
    void vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: escapeMarkdown(core.label(title)), cancellable: true }, (progress, token) => {
      // Cancel dismisses this notification; the request remains in Waiting.
      const cancelled = token.onCancellationRequested(() => this.close(key));
      progress.report({ message: links.join(' · ') });
      return done.finally(() => { cancelled.dispose(); });
    }).then(() => this.close(key), () => this.close(key));
  }

  private async act(action: NotificationAction) {
    const core = this.cores.get(action.coreId);
    if (!core) return;
    const validation = core.attention.findValidation(action.requestId);
    const question = core.attention.findUserInput(action.requestId);
    const request = validation ?? question;
    if (!request) return;
    if (action.action === 'open') await openSession({ coreId: core.id, sessionId: request.scope.sessionId });
    else if (validation && (action.action === 'approve' || action.action === 'deny')) await core.answers.decide(validation, action.action === 'approve');
    else if (question && action.action === 'answer') await core.answers.ask(question);
    else if (question && action.action === 'reply' && action.value !== undefined) await core.answers.reply(question, action.value);
  }

  private close(key: string) {
    const notification = this.notifications.get(key);
    this.notifications.delete(key);
    notification?.close();
  }

  /**
   * Work ended. Quiet: an information message that leads to the Session, and
   * nothing when that Session is open in a focused editor, where the
   * conversation already says it.
   */
  private async jobEnded(core: Core, sessionId: string, failed: boolean) {
    if (!notifyJobEnded()) return;
    const ref = { coreId: core.id, sessionId };
    if (isSessionOpen(ref) && vscode.window.state.focused) return;
    const title = await this.titleOf(core, sessionId);
    const choice = await vscode.window.showInformationMessage(
      core.label(`${failed ? 'Failed' : 'Done'} · ${title}`),
      'Open Session',
    );
    if (choice) await openSession(ref);
  }

  dispose() {
    for (const key of this.notifications.keys()) this.close(key);
    for (const disposable of this.disposables) disposable.dispose();
  }
}

/**
 * "$(inbox) N" in the status bar while something waits on any Core, leading
 * to the Waiting nodes. Hidden when nothing does: a count of zero is not news.
 */
export class WaitingStatus implements vscode.Disposable {
  private readonly item = vscode.window.createStatusBarItem('threavia.waiting', vscode.StatusBarAlignment.Left, 50);
  private readonly subscriptions: vscode.Disposable[];

  constructor(private readonly cores: Cores) {
    this.item.name = 'Threavia: Waiting';
    this.item.command = 'threavia.showWaiting';
    this.subscriptions = [cores.onAttention(() => this.update()), cores.onDidChange(() => this.update())];
    this.update();
  }

  private update() {
    const { count, tooltip } = waitingSummary(
      this.cores.list().map((core) => ({ name: core.name, count: waitingCount(core.attention.current) })),
    );
    if (count === 0) {
      this.item.hide();
      return;
    }
    this.item.text = `$(inbox) ${count}`;
    this.item.tooltip = tooltip;
    this.item.backgroundColor = new vscode.ThemeColor('statusBarItem.warningBackground');
    this.item.show();
  }

  dispose() {
    for (const subscription of this.subscriptions) subscription.dispose();
    this.item.dispose();
  }
}
