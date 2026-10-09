import { describe, expect, it } from 'vitest';

import { authorizeUrl, readCallback, redirectUri, renewalDelay, refreshTokens, RefusedError } from '../src/auth/oidc';
import { base64url, challengeOf, createState, createVerifier } from '../src/auth/pkce';

describe('PKCE', () => {
  it('derives the S256 challenge of RFC 7636', () => {
    // The example of RFC 7636, appendix B.
    expect(challengeOf('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).toBe(
      'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM',
    );
  });

  it('makes verifiers the RFC accepts, never twice the same', () => {
    const verifier = createVerifier();
    expect(verifier).toMatch(/^[A-Za-z0-9\-._~]{43,128}$/);
    expect(createVerifier()).not.toBe(verifier);
    expect(createState()).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it('encodes base64 for a URL, without padding', () => {
    expect(base64url(new Uint8Array([0xfb, 0xff, 0xfe]))).toBe('-__-');
    expect(base64url(new Uint8Array([1]))).toBe('AQ');
  });
});

describe('the authorization request', () => {
  it('sends the browser back to the editor that asked', () => {
    expect(redirectUri('vscode')).toBe('vscode://rclsilver.threavia/auth/callback');
    expect(redirectUri('vscode-insiders')).toBe('vscode-insiders://rclsilver.threavia/auth/callback');
  });

  it('carries the challenge, and spaces that survive the editor re-encoding the URI', () => {
    const url = new URL(
      authorizeUrl('https://sso.example.com/auth', {
        clientId: 'threavia',
        redirectUri: 'vscode://rclsilver.threavia/auth/callback',
        state: 'st',
        challenge: 'ch',
      }),
    );
    expect(url.searchParams.get('code_challenge')).toBe('ch');
    expect(url.searchParams.get('code_challenge_method')).toBe('S256');
    expect(url.searchParams.get('redirect_uri')).toBe('vscode://rclsilver.threavia/auth/callback');
    expect(url.search).not.toContain('+');
    expect(url.searchParams.get('scope')).toBe('openid profile email');
  });

  it('accepts only the callback of the sign-in it started', () => {
    expect(readCallback('code=abc&state=st', 'st')).toEqual({ code: 'abc' });
    expect(() => readCallback('code=abc&state=other', 'st')).toThrow('did not start here');
    expect(() => readCallback('error=access_denied&error_description=No', 'st')).toThrow('No');
  });
});

describe('renewal', () => {
  it('renews a minute early, and never spins', () => {
    expect(renewalDelay(10 * 60_000, 0)).toBe(9 * 60_000);
    expect(renewalDelay(30_000, 0)).toBe(15_000);
  });

  it('keeps the refresh token when the provider sends none back', async () => {
    const fetcher = (() =>
      Promise.resolve(new Response(JSON.stringify({ access_token: 'a2', expires_in: 300 })))) as typeof fetch;
    const tokens = await refreshTokens('https://sso/token', { clientId: 'c', refreshToken: 'r1' }, fetcher, () => 0);
    expect(tokens).toMatchObject({ accessToken: 'a2', refreshToken: 'r1', expiresAt: 300_000 });
  });

  it('tells a refused grant from a provider that is down', async () => {
    const answer = (status: number) => (() => Promise.resolve(new Response('{}', { status }))) as typeof fetch;
    await expect(refreshTokens('t', { clientId: 'c', refreshToken: 'r' }, answer(400))).rejects.toBeInstanceOf(
      RefusedError,
    );
    await expect(refreshTokens('t', { clientId: 'c', refreshToken: 'r' }, answer(502))).rejects.not.toBeInstanceOf(
      RefusedError,
    );
  });
});
