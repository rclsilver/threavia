import * as vscode from 'vscode';

import { accountSessionId, coreOfAccountSession, coreOfScopes, coreScope } from '../cores/refs';
import type { Callbacks, CoreAuth } from './core';
import { CALLBACK_PATH } from './oidc';

export const PROVIDER_ID = 'threavia';

/** A Core as the Accounts menu sees it. */
export interface AccountCore {
  id: string;
  readonly name: string;
  auth: CoreAuth;
}

/**
 * Every Core's sign-in, as the editor's Accounts menu shows it, and the one
 * way back from the browser.
 *
 * An extension registers one authentication provider, and the editor has one
 * URI handler per extension, so both are shared. Each Core signed in to is one
 * session of the provider, scoped `core:<id>`, its account named after the
 * Core when there are several; signing out of it there signs out of that Core
 * only. The tokens themselves stay with each Core's CoreAuth, which renews
 * them on its own: the provider only reports them.
 */
export class Accounts implements vscode.AuthenticationProvider, vscode.UriHandler, Callbacks, vscode.Disposable {
  private readonly pending = new Map<string, (query: string) => void>();
  private readonly sessionsChanged =
    new vscode.EventEmitter<vscode.AuthenticationProviderAuthenticationSessionsChangeEvent>();
  readonly onDidChangeSessions = this.sessionsChanged.event;

  constructor(
    private readonly cores: () => AccountCore[],
    /** Asks which Core to sign in to, for a request that names none. */
    private readonly choose: () => Promise<AccountCore | undefined>,
  ) {}

  private sessionOf(core: AccountCore, label: string): vscode.AuthenticationSession {
    const several = this.cores().length > 1;
    return {
      id: accountSessionId(core.id, label),
      accessToken: core.auth.accessToken,
      account: { id: accountSessionId(core.id, label), label: several ? `${label} (${core.name})` : label },
      scopes: [coreScope(core.id)],
    };
  }

  /** A Core's account changed: signed in, out, renewed or renamed. */
  changed(core: AccountCore, before: string | undefined, after: string | undefined) {
    const was = before ? this.sessionOf(core, before) : undefined;
    const is = after ? this.sessionOf(core, after) : undefined;
    this.sessionsChanged.fire({
      added: !was && is ? [is] : [],
      removed: was && (!is || was.id !== is.id) ? [was] : [],
      changed: was && is && was.id === is.id ? [is] : [],
    });
  }

  getSessions(scopes?: readonly string[]): Promise<vscode.AuthenticationSession[]> {
    const wanted = coreOfScopes(scopes);
    return Promise.resolve(
      this.cores()
        .filter((core) => !wanted || core.id === wanted)
        .flatMap((core) => {
          const label = core.auth.accountLabel;
          return label ? [this.sessionOf(core, label)] : [];
        }),
    );
  }

  async createSession(scopes: readonly string[]): Promise<vscode.AuthenticationSession> {
    const wanted = coreOfScopes(scopes);
    const core = wanted ? this.cores().find((candidate) => candidate.id === wanted) : await this.choose();
    if (!core) throw new Error('No Threavia Core to sign in to.');
    if (core.auth.mode === 'none') throw new Error(`${core.name} asks for no sign-in.`);
    const label = (await core.auth.signIn()) ? core.auth.accountLabel : undefined;
    if (!label) throw new Error('Not signed in.');
    return this.sessionOf(core, label);
  }

  async removeSession(sessionId: string): Promise<void> {
    const coreId = coreOfAccountSession(sessionId);
    await this.cores()
      .find((core) => core.id === coreId)
      ?.auth.signOut();
  }

  // ------------------------------------------------------ the way back

  expect(state: string): Promise<string> {
    return new Promise((resolve) => this.pending.set(state, resolve));
  }

  cancel(state: string) {
    this.pending.delete(state);
  }

  /**
   * The browser coming back with the code, through the editor's URI scheme.
   * The state it carries is the one a sign-in started with, which is how
   * several Cores signing in at once each get their own answer.
   */
  handleUri(uri: vscode.Uri) {
    if (uri.path !== CALLBACK_PATH) return;
    const state = new URLSearchParams(uri.query).get('state') ?? '';
    const resolve = this.pending.get(state);
    if (!resolve) {
      void vscode.window.showWarningMessage('This sign-in did not start here, or took too long. Sign in again.');
      return;
    }
    this.pending.delete(state);
    resolve(uri.query);
  }

  dispose() {
    this.sessionsChanged.dispose();
    this.pending.clear();
  }
}
