import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { UserInputRequest, ValidationRequest } from '../api/types';
import { oneLine } from '../tree/model';
import type { AttentionStore } from './store';

/**
 * Answering what waits, shared by the notifications and the sidebar.
 *
 * Every answer goes to the route the web client uses, and Core keeps the first
 * valid one: an answer given on another device first is said as such, never
 * as a failure.
 */
export class Answers {
  constructor(
    private readonly client: CoreClient,
    private readonly attention: AttentionStore,
  ) {}

  /** Resolves to true once the decision landed. */
  async decide(request: ValidationRequest, approved: boolean): Promise<boolean> {
    if (this.attention.ready && !this.attention.findValidation(request.id)) {
      void vscode.window.showInformationMessage('Already answered elsewhere.');
      return false;
    }
    try {
      await this.client.resolveValidation(request.id, approved);
      this.attention.drop(request.id);
      vscode.window.setStatusBarMessage(approved ? '$(check) Approved, the agent continues' : '$(close) Denied', 4000);
      return true;
    } catch (error) {
      this.failed(error, approved ? 'Not approved' : 'Not denied');
      return false;
    }
  }

  /**
   * Asks for an answer: a pick among the choices when there are some, a text
   * box when free text is allowed or nothing else is offered.
   */
  async ask(request: UserInputRequest): Promise<void> {
    const choices = request.choices ?? [];
    let value: string | undefined;
    if (choices.length > 0) {
      const other = '$(edit) Something else…';
      const pick = vscode.window.createQuickPick();
      pick.items = (request.freeText ? [...choices, other] : choices).map((label) => ({ label }));
      pick.title = oneLine(request.prompt, 80);
      pick.placeholder = request.prompt;
      pick.ignoreFocusOut = true;
      const picked = await this.waitForAnswer(request, pick, () => pick.selectedItems[0]?.label);
      if (picked === undefined) return;
      value = picked === other ? await this.type(request) : picked;
    } else {
      value = await this.type(request);
    }
    if (value) await this.reply(request, value);
  }

  private type(request: UserInputRequest): Promise<string | undefined> {
    const input = vscode.window.createInputBox();
    input.title = 'Answer the agent';
    input.prompt = request.prompt;
    input.placeholder = 'Your answer';
    input.ignoreFocusOut = true;
    return this.waitForAnswer(request, input, () => {
      input.validationMessage = input.value.trim() ? undefined : 'Write an answer.';
      return input.value.trim() ? input.value : undefined;
    });
  }

  /** The picker/text field belongs to this request, including when another device interrupts it. */
  private waitForAnswer(request: UserInputRequest, input: vscode.InputBox | vscode.QuickPick<vscode.QuickPickItem>, accepted: () => string | undefined): Promise<string | undefined> {
    return new Promise((resolve) => {
      const subscriptions: vscode.Disposable[] = [];
      let finished = false;
      const finish = (value?: string) => {
        if (finished) return;
        finished = true;
        for (const subscription of subscriptions) subscription.dispose();
        input.dispose();
        resolve(value);
      };
      subscriptions.push(
        input.onDidHide(() => finish()),
        input.onDidAccept(() => { const value = accepted(); if (value !== undefined) finish(value); }),
        this.attention.onChange(() => {
          if (this.attention.isObsolete(request) || (this.attention.ready && !this.attention.findUserInput(request.id))) finish();
        }),
      );
      if (this.attention.isObsolete(request) || (this.attention.ready && !this.attention.findUserInput(request.id))) finish();
      else input.show();
    });
  }

  /** Resolves to true once the answer landed. */
  async reply(request: UserInputRequest, value: string): Promise<boolean> {
    if (this.attention.ready && !this.attention.findUserInput(request.id)) {
      void vscode.window.showInformationMessage('Already answered elsewhere.');
      return false;
    }
    try {
      await this.client.resolveUserInput(request.id, value.trim());
      this.attention.drop(undefined, request.id);
      vscode.window.setStatusBarMessage(`$(check) Answered: ${oneLine(value, 60)}`, 4000);
      return true;
    } catch (error) {
      this.failed(error, 'Not answered');
      return false;
    }
  }

  private failed(error: unknown, outcome: string) {
    if (error instanceof ApiError && error.isConflict) {
      // The first valid answer wins: another device got there first.
      void vscode.window.showInformationMessage('Already answered elsewhere.');
      void this.attention.refresh();
      return;
    }
    const reason = error instanceof Error ? error.message : String(error);
    void vscode.window.showErrorMessage(`${outcome}: ${reason} The request is still waiting.`);
  }
}
