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
      const picked = await vscode.window.showQuickPick(request.freeText ? [...choices, other] : choices, {
        title: oneLine(request.prompt, 80),
        placeHolder: request.prompt,
        ignoreFocusOut: true,
      });
      if (picked === undefined) return;
      value = picked === other ? await this.type(request) : picked;
    } else {
      value = await this.type(request);
    }
    if (value) await this.reply(request, value);
  }

  private type(request: UserInputRequest): Thenable<string | undefined> {
    return vscode.window.showInputBox({
      title: 'Answer the agent',
      prompt: request.prompt,
      placeHolder: 'Your answer',
      ignoreFocusOut: true,
      validateInput: (value) => (value.trim() ? undefined : 'Write an answer.'),
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
