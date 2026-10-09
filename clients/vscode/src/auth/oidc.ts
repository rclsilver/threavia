/**
 * Authorization Code + PKCE against whatever provider Core names, from an
 * editor rather than a page.
 *
 * Core runs no login flow: it verifies a bearer token. The extension learns the
 * issuer and the client id from Core's unauthenticated /api/v1/auth, the way
 * the web client does, sends the person's own browser to the provider, and is
 * handed the code back through the editor's URI handler.
 *
 * Nothing in this file imports `vscode`, so the protocol is tested as is.
 */

/** The provider endpoints, as discovery describes them. */
export interface Endpoints {
  authorization_endpoint: string;
  token_endpoint: string;
  end_session_endpoint?: string;
}

export interface Tokens {
  accessToken: string;
  refreshToken?: string;
  idToken?: string;
  /** Epoch milliseconds. */
  expiresAt: number;
}

/** Where the provider sends the browser back: the editor, by its URI scheme. */
export const CALLBACK_PATH = '/auth/callback';

export const EXTENSION_ID = 'rclsilver.threavia';

/**
 * The redirect registered with the provider. `uriScheme` is `vscode`, or
 * `vscode-insiders` in Insiders, each registered once.
 */
export function redirectUri(uriScheme: string): string {
  return `${uriScheme}://${EXTENSION_ID}${CALLBACK_PATH}`;
}

export async function discover(issuer: string, fetcher: typeof fetch = fetch): Promise<Endpoints> {
  const url = `${issuer.replace(/\/$/, '')}/.well-known/openid-configuration`;
  const response = await fetcher(url);
  if (!response.ok) {
    throw new Error(`The provider at ${issuer} did not answer discovery (${response.status}).`);
  }
  return (await response.json()) as Endpoints;
}

export interface AuthorizeRequest {
  clientId: string;
  redirectUri: string;
  state: string;
  challenge: string;
}

/**
 * The address the browser is sent to.
 *
 * Encoded with encodeURIComponent rather than URLSearchParams: the editor
 * re-encodes a URI it opens, and a `+` standing for a space would arrive as a
 * literal plus in the scope.
 */
export function authorizeUrl(endpoint: string, request: AuthorizeRequest): string {
  const query = Object.entries({
    response_type: 'code',
    client_id: request.clientId,
    redirect_uri: request.redirectUri,
    // The web client's scopes. offline_access would outlive the provider's
    // session, but a provider refuses a scope the client is not granted, and
    // the same client serves the web too.
    scope: 'openid profile email',
    state: request.state,
    code_challenge: request.challenge,
    code_challenge_method: 'S256',
  })
    .map(([key, value]) => `${key}=${encodeURIComponent(value)}`)
    .join('&');
  return `${endpoint}${endpoint.includes('?') ? '&' : '?'}${query}`;
}

/** What the provider sent back, or why it did not. */
export function readCallback(query: string, expectedState: string): { code: string } {
  const params = new URLSearchParams(query);
  const error = params.get('error');
  if (error) throw new Error(params.get('error_description') || error);

  const code = params.get('code');
  // The state is what makes this callback the one we started: without the
  // check any link could hand the editor a code of its own choosing.
  if (!code || params.get('state') !== expectedState) {
    throw new Error('This sign-in did not start here.');
  }
  return { code };
}

export function exchangeCode(
  endpoint: string,
  input: { clientId: string; code: string; redirectUri: string; verifier: string },
  fetcher: typeof fetch = fetch,
  now: () => number = Date.now,
): Promise<Tokens> {
  return exchange(
    endpoint,
    {
      grant_type: 'authorization_code',
      client_id: input.clientId,
      code: input.code,
      redirect_uri: input.redirectUri,
      code_verifier: input.verifier,
    },
    fetcher,
    now,
  );
}

/** Trades a refresh token for a fresh pair, before the access token expires. */
export function refreshTokens(
  endpoint: string,
  input: { clientId: string; refreshToken: string },
  fetcher: typeof fetch = fetch,
  now: () => number = Date.now,
): Promise<Tokens> {
  return exchange(
    endpoint,
    { grant_type: 'refresh_token', client_id: input.clientId, refresh_token: input.refreshToken },
    fetcher,
    now,
  ).then((tokens) => ({
    ...tokens,
    // A provider may keep the refresh token and send none back.
    refreshToken: tokens.refreshToken ?? input.refreshToken,
  }));
}

/** The provider said no for good: the refresh token is spent or revoked. */
export class RefusedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RefusedError';
  }
}

async function exchange(
  endpoint: string,
  body: Record<string, string>,
  fetcher: typeof fetch,
  now: () => number,
): Promise<Tokens> {
  const response = await fetcher(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams(body).toString(),
  });
  if (!response.ok) {
    // 400 and 401 are the provider's answer about the grant itself; anything
    // else is the provider being unwell, which a later try may get past.
    const message = `The provider refused the exchange (${response.status}).`;
    if (response.status === 400 || response.status === 401) throw new RefusedError(message);
    throw new Error(message);
  }

  const payload = (await response.json()) as {
    access_token: string;
    refresh_token?: string;
    id_token?: string;
    expires_in?: number;
  };
  return {
    accessToken: payload.access_token,
    refreshToken: payload.refresh_token,
    idToken: payload.id_token,
    // A provider that says nothing about expiry gets a minute, so the renewal
    // still runs rather than trusting a token forever.
    expiresAt: now() + (payload.expires_in ?? 60) * 1000,
  };
}

/**
 * When to renew: a minute before expiry, never in less than fifteen seconds so
 * a short-lived token does not make the timer spin.
 */
export function renewalDelay(expiresAt: number, now: number = Date.now()): number {
  return Math.max(expiresAt - now - 60_000, 15_000);
}

/** True when the token is too close to expiry to send. */
export function isExpiring(tokens: Tokens, now: number = Date.now()): boolean {
  return tokens.expiresAt - now < 30_000;
}

/**
 * The claims of a JWT, read without verifying it. Only used to name the
 * account in the editor; Core is what verifies a token.
 */
export function claimsOf(token: string | undefined): Record<string, unknown> {
  const part = token?.split('.')[1];
  if (!part) return {};
  try {
    return JSON.parse(Buffer.from(part, 'base64url').toString('utf8')) as Record<string, unknown>;
  } catch {
    return {};
  }
}

/** A name a person recognises: their username, email, or name, in that order. */
export function accountLabelOf(tokens: Tokens): string {
  const claims = { ...claimsOf(tokens.accessToken), ...claimsOf(tokens.idToken) };
  for (const key of ['preferred_username', 'email', 'name', 'sub']) {
    const value = claims[key];
    if (typeof value === 'string' && value) return value;
  }
  return 'Threavia';
}
