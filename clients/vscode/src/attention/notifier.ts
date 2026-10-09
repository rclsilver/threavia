import * as vscode from 'vscode';

import type { EventBus } from '../api/events';
import type { UserInputRequest, ValidationRequest } from '../api/types';
import { notifyAttention, notifyJobEnded } from '../config';
import { isSessionOpen, openSession } from '../sessions';
import { oneLine, UNTITLED, validationAsk, waitingCount } from '../tree/model';
import type { Answers } from './answer';
import { AttentionLedger } from './ledger';
import type { AttentionStore } from './store';

const LEDGER_KEY = 'threavia.announced';

/** At most this many choices become buttons; more go behind "Answer…". */
const CHOICE_BUTTONS = 3;
const CHOICE_BUTTON_LENGTH = 24;

/**
 * Tells the person when something waits for them, once per request, and when
 * work ends in a Session they are not looking at.
 */
export class Notifier implements vscode.Disposable {
  private readonly ledger: AttentionLedger;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly state: vscode.Memento,
    private readonly attention: AttentionStore,
    private readonly answers: Answers,
    bus: EventBus,
    private readonly titleOf: (sessionId: string) => Promise<string>,
  ) {
    this.ledger = new AttentionLedger(state.get<string[]>(LEDGER_KEY, []));
    this.disposables.push(
      attention.onChange(() => this.announce()),
      bus.on('effect', (effect) => {
        if (effect.kind === 'jobEnded') void this.jobEnded(effect.sessionId, effect.failed);
      }),
    );
  }

  /** Announces what arrived since the last look, including what waited at start. */
  private announce() {
    // An empty list before the first answer is "not known yet", and taking it
    // would forget everything already announced.
    if (!this.attention.ready) return;
    const { validations, userInputs } = this.attention.current;
    const fresh = new Set(this.ledger.take([...validations, ...userInputs].map((request) => request.id)));
    void this.state.update(LEDGER_KEY, this.ledger.known);
    if (!notifyAttention()) return;

    for (const request of validations) if (fresh.has(request.id)) void this.validation(request);
    for (const request of userInputs) if (fresh.has(request.id)) void this.userInput(request);
  }

  private async validation(request: ValidationRequest) {
    const { tool, headline } = validationAsk(request);
    const ask = oneLine(headline ? `${tool ? `${tool}: ` : ''}${headline}` : request.summary || request.title, 160);
    const title = request.context?.sessionTitle || UNTITLED;
    const choice = await vscode.window.showWarningMessage(
      `Approval needed · ${title} — ${ask}`,
      'Approve',
      'Deny',
      'Open Session',
    );
    if (choice === 'Approve' || choice === 'Deny') await this.answers.decide(request, choice === 'Approve');
    if (choice === 'Open Session') await openSession(request.scope.sessionId);
  }

  private async userInput(request: UserInputRequest) {
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
      `Question · ${title} — ${oneLine(request.prompt, 160)}`,
      ...buttons,
      'Answer…',
      'Open Session',
    );
    if (choice === undefined) return;
    if (choice === 'Answer…') await this.answers.ask(request);
    else if (choice === 'Open Session') await openSession(request.scope.sessionId);
    else await this.answers.reply(request, choice);
  }

  /**
   * Work ended. Quiet: an information message that leads to the Session, and
   * nothing when that Session is open in a focused editor, where the
   * conversation already says it.
   */
  private async jobEnded(sessionId: string, failed: boolean) {
    if (!notifyJobEnded()) return;
    if (isSessionOpen(sessionId) && vscode.window.state.focused) return;
    const title = await this.titleOf(sessionId);
    const choice = await vscode.window.showInformationMessage(`${failed ? 'Failed' : 'Done'} · ${title}`, 'Open Session');
    if (choice) await openSession(sessionId);
  }

  dispose() {
    for (const disposable of this.disposables) disposable.dispose();
  }
}

/**
 * "$(inbox) N" in the status bar while something waits, leading to the Waiting
 * node. Hidden when nothing does: a count of zero is not news.
 */
export class WaitingStatus implements vscode.Disposable {
  private readonly item = vscode.window.createStatusBarItem('threavia.waiting', vscode.StatusBarAlignment.Left, 50);
  private readonly subscription: vscode.Disposable;

  constructor(private readonly attention: AttentionStore) {
    this.item.name = 'Threavia: Waiting';
    this.item.command = 'threavia.showWaiting';
    this.subscription = attention.onChange(() => this.update());
    this.update();
  }

  private update() {
    const count = waitingCount(this.attention.current);
    if (count === 0) {
      this.item.hide();
      return;
    }
    this.item.text = `$(inbox) ${count}`;
    this.item.tooltip = `${count} waiting for you in Threavia`;
    this.item.backgroundColor = new vscode.ThemeColor('statusBarItem.warningBackground');
    this.item.show();
  }

  dispose() {
    this.subscription.dispose();
    this.item.dispose();
  }
}
