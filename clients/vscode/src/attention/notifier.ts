import * as vscode from 'vscode';

import type { UserInputRequest, ValidationRequest } from '../api/types';
import { notifyAttention, notifyJobEnded } from '../config';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import { isSessionOpen, openSession } from '../sessions';
import { oneLine, UNTITLED, validationAsk, waitingCount, waitingSummary } from '../tree/model';

/** At most this many choices become buttons; more go behind "Answer…". */
const CHOICE_BUTTONS = 3;
const CHOICE_BUTTON_LENGTH = 24;

/**
 * Tells the person when something waits for them, once per request, and when
 * work ends in a Session they are not looking at, on every Core. With several
 * Cores, each message starts with the name of the one it comes from.
 */
export class Notifier implements vscode.Disposable {
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly cores: Cores,
    private readonly titleOf: (core: Core, sessionId: string) => Promise<string>,
  ) {
    this.disposables.push(
      cores.onAttention((core) => this.announce(core)),
      cores.on('effect', (core, effect) => {
        if (effect.kind === 'jobEnded') void this.jobEnded(core, effect.sessionId, effect.failed);
      }),
    );
  }

  /** Announces what arrived since the last look, including what waited at start. */
  private announce(core: Core) {
    // An empty list before the first answer is "not known yet", and taking it
    // would forget everything already announced.
    if (!core.attention.ready) return;
    const { validations, userInputs } = core.attention.current;
    const fresh = new Set(this.cores.ledgers.take(core.id, [...validations, ...userInputs].map((request) => request.id)));
    void this.cores.saveLedgers();
    if (!notifyAttention()) return;

    for (const request of validations) if (fresh.has(request.id)) void this.validation(core, request);
    for (const request of userInputs) if (fresh.has(request.id)) void this.userInput(core, request);
  }

  private async validation(core: Core, request: ValidationRequest) {
    const { tool, headline } = validationAsk(request);
    const ask = oneLine(headline ? `${tool ? `${tool}: ` : ''}${headline}` : request.summary || request.title, 160);
    const title = request.context?.sessionTitle || UNTITLED;
    const choice = await vscode.window.showWarningMessage(
      core.label(`Approval needed · ${title} — ${ask}`),
      'Approve',
      'Deny',
      'Open Session',
    );
    if (choice === 'Approve' || choice === 'Deny') await core.answers.decide(request, choice === 'Approve');
    if (choice === 'Open Session') await openSession({ coreId: core.id, sessionId: request.scope.sessionId });
  }

  private async userInput(core: Core, request: UserInputRequest) {
    const title = request.context?.sessionTitle || UNTITLED;
    const choices = request.choices ?? [];
    // A few short choices answer from the notification itself; anything longer
    // would be cut off on a button, so it goes through the pick.
    const buttons =
      choices.length > 0 &&
      choices.length <= CHOICE_BUTTONS &&
      choices.every((choice) => choice.length <= CHOICE_BUTTON_LENGTH)
        ? choices
        : [];
    const choice = await vscode.window.showInformationMessage(
      core.label(`Question · ${title} — ${oneLine(request.prompt, 160)}`),
      ...buttons,
      'Answer…',
      'Open Session',
    );
    if (choice === undefined) return;
    if (choice === 'Answer…') await core.answers.ask(request);
    else if (choice === 'Open Session') await openSession({ coreId: core.id, sessionId: request.scope.sessionId });
    else await core.answers.reply(request, choice);
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
