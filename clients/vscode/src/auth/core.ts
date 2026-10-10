import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { AuthPublic } from '../api/types';
import { credentialKey } from '../cores/settings';
import {
  RefusedError,
  accountLabelOf,
  authorizeUrl,
  discover,
  exchangeCode,
  isExpiring,
  redirectUri,
  readCallback,
  refreshTokens,
  renewalDelay,
  type Tokens,
} from './oidc';
import { challengeOf, createState, createVerifier } from './pkce';

/** What is kept in SecretStorage, one entry per Core. */
type Credential =
  | { type: 'oidc'; tokens: Tokens; label: string }
  | { type: 'basic'; username: string; password: string };

/** How long a person has to finish signing in in their browser. */
const SIGN_IN_TIMEOUT = 5 * 60_000;

/**
 * Where the browser's way back is waited for. There is one URI handler per
 * extension, so every Core's sign-in comes back through the same one; the
 * OIDC state a sign-in started with says which Core it was.
 */
export interface Callbacks {
  expect(state: string): Promise<string>;
  cancel(state: string): void;
}

/** Which Core a sign-in is for, as the auth layer names it to the person. */
export interface CoreName {
  id: string;
  /** What the person calls it. */
  name: () => string;
  /** Whether there is more than one Core, so a message has to say which. */
  several: () => boolean;
}

/**
 * Signing in to one Core, in whichever mode it runs.
 *
 * - `none` asks for nothing: Core attributes every request to one configured
 *   user. It is how a laptop Core runs, so it costs nothing here either.
 * - `basic` asks for a username and password once and keeps them.
 * - `oidc` runs Authorization Code + PKCE in the system browser against the
 *   provider this Core names, and keeps the access and refresh tokens.
 *
 * Credentials live in SecretStorage keyed by the Core's id, so each Core keeps
 * its own and each discovers its own provider and client from its public auth
 * route. The editor's Accounts menu reaches them through one
 * AuthenticationProvider for the whole extension (see accounts.ts).
 */
export class CoreAuth implements vscode.Disposable {
  private config: AuthPublic | undefined;
  private credential: Credential | undefined;
  private renewal: ReturnType<typeof setTimeout> | undefined;
  private refreshing: Promise<boolean> | null = null;

  /** Fired whenever whether requests carry a credential changes. */
  private readonly changed = new vscode.EventEmitter<void>();
  readonly onDidChange = this.changed.event;

  /** Told the account before and after, for the editor's Accounts menu. */
  onAccount: (before: string | undefined, after: string | undefined) => void = () => undefined;

  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly secrets: vscode.SecretStorage,
    private readonly core: CoreName,
    private readonly client: () => CoreClient,
    private readonly callbacks: Callbacks,
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
    return labelOf(this.credential);
  }

  /** The access token, for the Accounts menu's session; none for basic. */
  get accessToken(): string {
    return this.credential?.type === 'oidc' ? this.credential.tokens.accessToken : '';
  }

  /** Learns how this Core authenticates, and loads what is stored for it. */
  async load(config: AuthPublic) {
    this.config = config;
    await this.reload();
  }

  /** Forgets how the Core authenticates, for one that does not answer. */
  forget() {
    this.config = undefined;
    clearTimeout(this.renewal);
  }

  private key(): string {
    return credentialKey(this.core.id);
  }

  /** "Threavia", or the Core's name when there are several to tell apart. */
  private get who(): string {
    return this.core.several() ? this.core.name() : 'Threavia';
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
    if (credential && this.config && credential.type !== this.config.mode) credential = undefined;
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
    this.onAccount(labelOf(before), labelOf(after));
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
      this.offerSignIn(`${this.who} no longer accepts your sign-in.`);
    }
    return false;
  }

  private offerSignIn(text: string) {
    void vscode.window
      .showWarningMessage(text, 'Sign In')
      .then((choice) => choice && vscode.commands.executeCommand('threavia.signIn', this.core.id));
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
        this.offerSignIn(`Your ${this.who} sign-in has expired.`);
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
        void vscode.window.showInformationMessage(`${this.core.name()} asks for no sign-in.`);
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
    const title = `Sign in to ${this.who}`;
    const username = await vscode.window.showInputBox({
      title,
      prompt: 'Your Threavia username',
      ignoreFocusOut: true,
      validateInput: (value) => (value.trim() ? undefined : 'Enter your username.'),
    });
    if (!username) return false;
    const password = await vscode.window.showInputBox({
      title,
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
        refused ? `${this.core.name()} refused that username and password.` : `Not signed in: ${message(error)}`,
      );
      return false;
    }
    await this.store(credential);
    return true;
  }

  private async signInOidc(): Promise<boolean> {
    const config = this.config;
    if (!config?.issuer || !config.clientId) {
      void vscode.window.showErrorMessage(`${this.core.name()} asks for OIDC but names no issuer or client id.`);
      return false;
    }

    const state = createState();
    try {
      const endpoints = await discover(config.issuer);
      const verifier = createVerifier();
      const redirect = redirectUri(vscode.env.uriScheme);

      const callback = this.callbacks.expect(state);
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
          title: `Signing in to ${this.who}: finish in your browser…`,
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
      void vscode.window.showErrorMessage(`Not signed in: ${message(error)}`);
      return false;
    } finally {
      this.callbacks.cancel(state);
    }
  }

  dispose() {
    clearTimeout(this.renewal);
    this.changed.dispose();
    for (const disposable of this.disposables) disposable.dispose();
  }
}

function labelOf(credential: Credential | undefined): string | undefined {
  if (!credential) return undefined;
  return credential.type === 'basic' ? credential.username : credential.label;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
