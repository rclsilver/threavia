import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { SecretStorage } from 'vscode';

vi.mock('vscode', () => ({
  EventEmitter: class {
    event = () => ({ dispose() {} });
    fire() {}
    dispose() {}
  },
  window: { showWarningMessage: vi.fn().mockResolvedValue(undefined) },
  commands: { executeCommand: vi.fn() },
}));

import { CoreAuth } from '../src/auth/core';
import type { CoreClient } from '../src/api/client';

let raw: string | undefined;
const config = { mode: 'oidc' as const, issuer: 'https://sso.example', clientId: 'threavia' };
const sessions: CoreAuth[] = [];

beforeEach(() => {
  vi.useFakeTimers();
  raw = JSON.stringify({ type: 'oidc', label: 'Thomas', tokens: { accessToken: 'a1', refreshToken: 'r1', expiresAt: Date.now() + 120_000 } });
});
afterEach(() => {
  for (const session of sessions.splice(0)) session.dispose();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

async function create() {
  const secrets = {
    get: () => Promise.resolve(raw),
    store: (_key: string, value: string) => { raw = value; return Promise.resolve(); },
    delete: () => { raw = undefined; return Promise.resolve(); },
    onDidChange: () => ({ dispose() {} }),
  } as unknown as SecretStorage;
  const auth = new CoreAuth(secrets, { id: 'core', name: () => 'Threavia', several: () => false },
    () => ({} as CoreClient), { expect: () => Promise.resolve(''), cancel() {} });
  sessions.push(auth);
  await auth.load(config);
  return auth;
}

it('keeps SecretStorage after a failed 401 recovery and retries the renewal', async () => {
  let attempts = 0;
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (url.includes('.well-known')) return Promise.resolve(Response.json({ token_endpoint: 'https://sso.example/token' }));
    attempts++;
    return Promise.resolve(attempts === 1 ? new Response('', { status: 503 }) : Response.json({ access_token: 'a2', refresh_token: 'r2', expires_in: 300 }));
  }));
  const auth = await create();
  const before = raw;
  expect(await auth.recover()).toBe(false);
  expect(auth.signedIn).toBe(true);
  expect(raw).toBe(before);
  await vi.advanceTimersByTimeAsync(30_000);
  expect(auth.accessToken).toBe('a2');
  const saved = JSON.parse(raw!) as { tokens: { refreshToken: string } };
  expect(saved.tokens.refreshToken).toBe('r2');
});

it('clears a genuinely expired provider session', async () => {
  vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(url.includes('.well-known')
    ? Response.json({ token_endpoint: 'https://sso.example/token' })
    : Response.json({ error: 'invalid_grant' }, { status: 400 }))));
  const auth = await create();
  expect(await auth.recover()).toBe(false);
  expect(auth.signedIn).toBe(false);
  expect(raw).toBeUndefined();
});

it('does not restore credentials if renewal completes after signing out', async () => {
  let finish!: (response: Response) => void;
  const fetcher = vi.fn((url: string) => url.includes('.well-known')
    ? Promise.resolve(Response.json({ token_endpoint: 'https://sso.example/token' }))
    : new Promise<Response>((resolve) => { finish = resolve; }));
  vi.stubGlobal('fetch', fetcher);
  const auth = await create();
  const pending = auth.recover();
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  await auth.signOut();
  finish(Response.json({ access_token: 'late', refresh_token: 'late-r', expires_in: 300 }));
  expect(await pending).toBe(false);
  expect(auth.signedIn).toBe(false);
  expect(raw).toBeUndefined();
});
