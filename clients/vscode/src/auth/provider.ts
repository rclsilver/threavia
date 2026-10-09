import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { AuthPublic } from '../api/types';
import {
  CALLBACK_PATH,
  RefusedError,
  accountLabelOf,
  authorizeUrl,
  discover,
  exchangeCode,
  isExpiring,
  readCallback,
  redirectUri,
  refreshTokens,
  renewalDelay,
  type Tokens,
} from './oidc';
import { challengeOf, createState, createVerifier } from './pkce';

export const PROVIDER_ID = 'threavia';

/** What is kept in SecretStorage, one entry per Core URL. */
type Credential =
  | { type: 'oidc'; tokens: Tokens; label: string }
  | { type: 'basic'; username: string; password: string };

/** How long a person has to finish signing in in their browser. */
const SIGN_IN_TIMEOUT = 5 * 60_000;

/**
 * Signing in to Core, in whichever mode it runs.
 *
 * - `none` asks for nothing: Core attributes every request to one configured
 *   user. It is how a laptop Core runs, so it costs nothing here either.
 * - `basic` asks for a username and password once and keeps them.
 * - `oidc` runs Authorization Code + PKCE in the system browser against the
 *   provider Core names, and keeps the access and refresh tokens.
 *
 * Credentials live in SecretStorage, keyed by Core URL so a local Core and a
 * deployed one each keep their own. It is also a vscode.AuthenticationProvider,
 * so the account shows in the editor's Accounts menu and signs out from there.
 */
export class ThreaviaAuth implements vscode.AuthenticationProvider, vscode.UriHandler, vscode.Disposable {
  private config: AuthPublic | undefined;
  private credential: Credential | undefined;
  private renewal: ReturnType<typeof setTimeout> | undefined;
  private refreshing: Promise<boolean> | null = null;
  private pending: { state: string; resolve: (query: string) => void } | undefined;

  private readonly sessionsChanged =
    new vscode.EventEmitter<vscode.AuthenticationProviderAuthenticationSessionsChangeEvent>();
  readonly onDidChangeSessions = this.sessionsChanged.event;

  /** Fired whenever whether requests carry a credential changes. */
  private readonly changed = new vscode.EventEmitter<void>();
  readonly onDidChange = this.changed.event;

  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly secrets: vscode.SecretStorage,
    private readonly client: () => CoreClient,
    private readonly url: () => string,
  ) {
    // Another window signed in, out, or renewed the tokens: follow it rather
    // than sending a refresh token that window may already have spent.
    this.disposables.push(
      secrets.onDidChange((event) => {
        if (event.key === this.key()) void this.reload();
      }),
    );
  }

  get mode(): AuthPublic['mode'] | undefined {
    return this.config?.mode;
  }

  /** True when requests can be sent: nothing is needed, or something is held. */
  get ready(): boolean {
    return this.config?.mode === 'none' || this.credential !== undefined;
  }

  /** Whether there is anything to sign out of. */
  get signedIn(): boolean {
    return this.credential !== undefined;
  }

  get accountLabel(): string | undefined {
    if (!this.credential) return undefined;
    return this.credential.type === 'basic' ? this.credential.username : this.credential.label;
  }

  /** Learns how this Core authenticates, and loads what is stored for it. */
  async load(config: AuthPublic) {
    this.config = config;
    await this.reload();
  }

  /** Forgets the Core, for an unset or changed URL. */
  forget() {
    this.config = undefined;
    this.credential = undefined;
    clearTimeout(this.renewal);
  }

  private key(): string {
    return `threavia.credential:${this.url()}`;
  }

  private async reload() {
    const before = this.credential;
    const raw = await this.secrets.get(this.key());
    let credential: Credential | undefined;
    try {
      credential = raw ? (JSON.parse(raw) as Credential) : undefined;
    } catch {
      credential = undefined;
    }
    // A credential of another mode is from before Core changed mode; it would
    // only earn a 401.
    if (credential && credential.type !== this.config?.mode) credential = undefined;
    this.credential = credential;
    this.arm();
    if (JSON.stringify(before) !== JSON.stringify(credential)) this.announce(before, credential);
  }

  private async store(credential: Credential | undefined) {
    const before = this.credential;
    this.credential = credential;
    this.arm();
    if (credential) await this.secrets.store(this.key(), JSON.stringify(credential));
    else await this.secrets.delete(this.key());
    this.announce(before, credential);
  }

  private announce(before: Credential | undefined, after: Credential | undefined) {
    const was = before ? this.sessionOf(before) : undefined;
    const is = after ? this.sessionOf(after) : undefined;
    this.sessionsChanged.fire({
      added: !was && is ? [is] : [],
      removed: was && !is ? [was] : [],
      changed: was && is ? [is] : [],
    });
    this.changed.fire();
  }

  // ------------------------------------------------------------ the header

  /** The Authorization header for the next request, renewing a token first if due. */
  async authorization(): Promise<string | undefined> {
    const credential = this.credential;
    if (!credential || this.config?.mode === 'none') return undefined;
    if (credential.type === 'basic') {
      return `Basic ${Buffer.from(`${credential.username}:${credential.password}`).toString('base64')}`;
    }
    if (isExpiring(credential.tokens)) await this.refresh();
    const tokens = this.credential?.type === 'oidc' ? this.credential.tokens : undefined;
    return tokens ? `Bearer ${tokens.accessToken}` : undefined;
  }

  /**
   * Core answered 401. A token may have been revoked or expired early, so one
   * renewal is worth trying; past that, the credential is wrong and is dropped,
   * which shows the sign-in again rather than failing every request.
   */
  async recover(): Promise<boolean> {
    if (this.credential?.type === 'oidc' && (await this.refresh())) return true;
    if (this.credential) {
      await this.store(undefined);
      void vscode.window
        .showWarningMessage('Threavia no longer accepts your sign-in.', 'Sign In')
        .then((choice) => choice && vscode.commands.executeCommand('threavia.signIn'));
    }
    return false;
  }

  /** Renews the tokens. Concurrent callers share one exchange. */
  private refresh(): Promise<boolean> {
    this.refreshing ??= this.doRefresh().finally(() => {
      this.refreshing = null;
    });
    return this.refreshing;
  }

  private async doRefresh(): Promise<boolean> {
    const credential = this.credential;
    const config = this.config;
    if (credential?.type !== 'oidc' || !credential.tokens.refreshToken || !config?.issuer) return false;
    try {
      const endpoints = await discover(config.issuer);
      const tokens = await refreshTokens(endpoints.token_endpoint, {
        clientId: config.clientId ?? '',
        refreshToken: credential.tokens.refreshToken,
      });
      await this.store({ ...credential, tokens });
      return true;
    } catch (error) {
      if (error instanceof RefusedError) {
        // The provider's session is over. Starting the flow again is the honest
        // answer, but only when the person asks: a browser tab opening by itself
        // is not something an editor should do.
        await this.store(undefined);
        void vscode.window
          .showWarningMessage('Your Threavia sign-in has expired.', 'Sign In')
          .then((choice) => choice && vscode.commands.executeCommand('threavia.signIn'));
        return false;
      }
      // The provider is unreachable: keep the tokens and try again shortly.
      clearTimeout(this.renewal);
      this.renewal = setTimeout(() => void this.refresh(), 30_000);
      return false;
    }
  }

  /** Renews ahead of expiry, so a request rarely waits for it. */
  private arm() {
    clearTimeout(this.renewal);
    const credential = this.credential;
    if (credential?.type !== 'oidc' || !credential.tokens.refreshToken) return;
    this.renewal = setTimeout(() => void this.refresh(), renewalDelay(credential.tokens.expiresAt));
  }

  // ---------------------------------------------------------- signing in

  /** Signs in the way this Core asks for. Resolves to false when it did not happen. */
  async signIn(): Promise<boolean> {
    switch (this.config?.mode) {
      case undefined:
        return false;
      case 'none':
        void vscode.window.showInformationMessage('This Core asks for no sign-in.');
        return true;
      case 'basic':
        return this.signInBasic();
      case 'oidc':
        return this.signInOidc();
    }
  }

  async signOut() {
    await this.store(undefined);
  }

  private async signInBasic(): Promise<boolean> {
    const username = await vscode.window.showInputBox({
      title: 'Sign in to Threavia',
      prompt: 'Your Threavia username',
      ignoreFocusOut: true,
      validateInput: (value) => (value.trim() ? undefined : 'Enter your username.'),
    });
    if (!username) return false;
    const password = await vscode.window.showInputBox({
      title: 'Sign in to Threavia',
      prompt: `The password of ${username.trim()}`,
      password: true,
      ignoreFocusOut: true,
    });
    if (password === undefined) return false;

    const credential: Credential = { type: 'basic', username: username.trim(), password };
    const before = this.credential;
    this.credential = credential;
    try {
      // Checked before it is kept, so a typo is said now rather than as a
      // sidebar that stays empty.
      await this.client().me();
    } catch (error) {
      this.credential = before;
      const refused = error instanceof ApiError && error.isUnauthorized;
      void vscode.window.showErrorMessage(
        refused ? 'Core refused that username and password.' : `Not signed in: ${message(error)}`,
      );
      return false;
    }
    await this.store(credential);
    return true;
  }

  private async signInOidc(): Promise<boolean> {
    const config = this.config;
    if (!config?.issuer || !config.clientId) {
      void vscode.window.showErrorMessage('Core asks for OIDC but names no issuer or client id.');
      return false;
    }

    try {
      const endpoints = await discover(config.issuer);
      const verifier = createVerifier();
      const state = createState();
      const redirect = redirectUri(vscode.env.uriScheme);

      const callback = new Promise<string>((resolve) => {
        this.pending = { state, resolve };
      });
      const opened = await vscode.env.openExternal(
        vscode.Uri.parse(
          authorizeUrl(endpoints.authorization_endpoint, {
            clientId: config.clientId,
            redirectUri: redirect,
            state,
            challenge: challengeOf(verifier),
          }),
        ),
      );
      if (!opened) return false;

      const query = await vscode.window.withProgress(
        {
          location: vscode.ProgressLocation.Notification,
          title: 'Signing in to Threavia: finish in your browser…',
          cancellable: true,
        },
        (_progress, cancelled) =>
          Promise.race([
            callback,
            new Promise<undefined>((resolve) => {
              cancelled.onCancellationRequested(() => resolve(undefined));
              setTimeout(() => resolve(undefined), SIGN_IN_TIMEOUT);
            }),
          ]),
      );
      this.pending = undefined;
      if (query === undefined) return false;

      const { code } = readCallback(query, state);
      const tokens = await exchangeCode(endpoints.token_endpoint, {
        clientId: config.clientId,
        code,
        redirectUri: redirect,
        verifier,
      });
      await this.store({ type: 'oidc', tokens, label: accountLabelOf(tokens) });
      return true;
    } catch (error) {
      this.pending = undefined;
      void vscode.window.showErrorMessage(`Not signed in: ${message(error)}`);
      return false;
    }
  }

  /** The browser coming back with the code, through the editor's URI scheme. */
  handleUri(uri: vscode.Uri) {
    if (uri.path !== CALLBACK_PATH) return;
    if (!this.pending) {
      void vscode.window.showWarningMessage('This sign-in did not start here, or took too long. Sign in again.');
      return;
    }
    this.pending.resolve(uri.query);
  }

  // ------------------------------------------- vscode.AuthenticationProvider

  private sessionOf(credential: Credential): vscode.AuthenticationSession {
    const label = credential.type === 'basic' ? credential.username : credential.label;
    return {
      id: `${this.url()}#${label}`,
      accessToken: credential.type === 'oidc' ? credential.tokens.accessToken : '',
      account: { id: label, label },
      scopes: [],
    };
  }

  getSessions(): Promise<vscode.AuthenticationSession[]> {
    return Promise.resolve(this.credential ? [this.sessionOf(this.credential)] : []);
  }

  async createSession(): Promise<vscode.AuthenticationSession> {
    if (this.config?.mode === 'none') throw new Error('This Core asks for no sign-in.');
    if (!(await this.signIn()) || !this.credential) throw new Error('Not signed in.');
    return this.sessionOf(this.credential);
  }

  async removeSession(): Promise<void> {
    await this.signOut();
  }

  dispose() {
    clearTimeout(this.renewal);
    this.sessionsChanged.dispose();
    this.changed.dispose();
    for (const disposable of this.disposables) disposable.dispose();
  }
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
