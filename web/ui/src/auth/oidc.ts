/**
 * Authorization Code + PKCE against whatever provider Core names.
 *
 * Core runs no login flow: it verifies a bearer token and says so. This is the
 * half that obtains one. The client is public — a page anyone can read holds no
 * secret — so PKCE is what replaces the client secret: the provider refuses a
 * code that does not come with the verifier matching the challenge.
 *
 * Tokens live in memory and nowhere else. A reload therefore starts a redirect,
 * which the provider answers without a prompt while its own session stands, and
 * which leaves no token in storage for a script on this page to read.
 */

/** What Core says about the authentication of this deployment. */
export interface AuthPublic {
  mode: 'none' | 'basic' | 'oidc';
  issuer?: string;
  clientId?: string;
  audience?: string;
}

/** The provider endpoints, as discovery describes them. */
interface Endpoints {
  authorization_endpoint: string;
  token_endpoint: string;
  end_session_endpoint?: string;
}

export interface Tokens {
  accessToken: string;
  refreshToken?: string;
  /** Epoch milliseconds. */
  expiresAt: number;
}

/** Where the provider sends the browser back. One route, registered once. */
export const CALLBACK_PATH = '/auth/callback';

const VERIFIER_KEY = 'threavia.oidc.verifier';
const STATE_KEY = 'threavia.oidc.state';
const RETURN_KEY = 'threavia.oidc.return';

export async function discover(issuer: string): Promise<Endpoints> {
  const url = `${issuer.replace(/\/$/, '')}/.well-known/openid-configuration`;
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`the provider at ${issuer} did not answer discovery (${response.status})`);
  }
  return (await response.json()) as Endpoints;
}

/**
 * Sends the browser to the provider.
 *
 * The verifier and the state outlive the redirect in session storage, which is
 * what they are for: they are single-use, they are worthless to anyone who
 * cannot also complete this exact redirect, and they are gone when the tab is.
 */
export async function login(config: AuthPublic): Promise<never> {
  const endpoints = await discover(config.issuer ?? '');

  const verifier = randomString(64);
  const state = randomString(24);
  sessionStorage.setItem(VERIFIER_KEY, verifier);
  sessionStorage.setItem(STATE_KEY, state);
  // Where the person was going, so the redirect does not cost them their place.
  sessionStorage.setItem(RETURN_KEY, window.location.pathname + window.location.search);

  const query = new URLSearchParams({
    response_type: 'code',
    client_id: config.clientId ?? '',
    redirect_uri: redirectURI(),
    scope: 'openid profile email',
    state,
    code_challenge: await challenge(verifier),
    code_challenge_method: 'S256',
  });

  window.location.assign(`${endpoints.authorization_endpoint}?${query.toString()}`);
  // The assignment above never returns in practice; this keeps the type honest.
  return new Promise<never>(() => {});
}

/** Completes the redirect: exchanges the code for tokens. */
export async function complete(config: AuthPublic): Promise<{ tokens: Tokens; to: string }> {
  const params = new URLSearchParams(window.location.search);
  const error = params.get('error');
  if (error) {
    throw new Error(params.get('error_description') || error);
  }

  const code = params.get('code');
  const state = params.get('state');
  const verifier = sessionStorage.getItem(VERIFIER_KEY);
  // The state is what makes this redirect the one we started: without the check
  // any page could hand us a code of its own choosing.
  if (!code || !state || state !== sessionStorage.getItem(STATE_KEY) || !verifier) {
    throw new Error('this sign-in did not start here');
  }

  const to = sessionStorage.getItem(RETURN_KEY) || '/';
  sessionStorage.removeItem(VERIFIER_KEY);
  sessionStorage.removeItem(STATE_KEY);
  sessionStorage.removeItem(RETURN_KEY);

  const endpoints = await discover(config.issuer ?? '');
  const tokens = await exchange(endpoints.token_endpoint, {
    grant_type: 'authorization_code',
    client_id: config.clientId ?? '',
    code,
    redirect_uri: redirectURI(),
    code_verifier: verifier,
  });

  return { tokens, to };
}

/** Trades a refresh token for a fresh pair, before the access token expires. */
export async function refresh(config: AuthPublic, refreshToken: string): Promise<Tokens> {
  const endpoints = await discover(config.issuer ?? '');
  return exchange(endpoints.token_endpoint, {
    grant_type: 'refresh_token',
    client_id: config.clientId ?? '',
    refresh_token: refreshToken,
  });
}

async function exchange(endpoint: string, body: Record<string, string>): Promise<Tokens> {
  const response = await fetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams(body).toString(),
  });
  if (!response.ok) {
    throw new Error(`the provider refused the exchange (${response.status})`);
  }

  const payload = (await response.json()) as {
    access_token: string;
    refresh_token?: string;
    expires_in?: number;
  };
  return {
    accessToken: payload.access_token,
    refreshToken: payload.refresh_token,
    // A provider that says nothing about expiry gets a minute, so the refresh
    // loop still runs rather than trusting a token forever.
    expiresAt: Date.now() + (payload.expires_in ?? 60) * 1000,
  };
}

function redirectURI(): string {
  return `${window.location.origin}${CALLBACK_PATH}`;
}

/** URL-safe base64 of the SHA-256 of the verifier, which is S256. */
async function challenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier));
  return base64url(new Uint8Array(digest));
}

function randomString(bytes: number): string {
  return base64url(crypto.getRandomValues(new Uint8Array(bytes)));
}

function base64url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
