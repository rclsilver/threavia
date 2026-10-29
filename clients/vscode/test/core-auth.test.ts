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
import { CoreClient } from '../src/api/client';

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

const secrets = {
  get: () => Promise.resolve(raw),
  store: (_key: string, value: string) => { raw = value; return Promise.resolve(); },
  delete: () => { raw = undefined; return Promise.resolve(); },
  onDidChange: () => ({ dispose() {} }),
} as unknown as SecretStorage;

function unloaded(client: () => CoreClient = () => ({} as CoreClient)) {
  const auth = new CoreAuth(secrets, { id: 'core', name: () => 'Threavia', several: () => false },
    client, { expect: () => Promise.resolve(''), cancel() {} });
  sessions.push(auth);
  return auth;
}

async function create() {
  const auth = unloaded();
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

it('keeps a basic sign-in when Core refuses a request sent before it was read', async () => {
  raw = JSON.stringify({ type: 'basic', username: 'thomas', password: 'secret' });
  const auth = unloaded();
  const fetcher = vi.fn((_url: string, init: RequestInit) => {
    if (new Headers(init.headers).get('Authorization')) return Promise.resolve(Response.json({ id: 'thomas' }));
    // A conversation restored at start asks before the sign-in is read, and
    // the sign-in is read while Core's refusal is on its way back.
    return auth.load({ mode: 'basic' }).then(() => new Response('', { status: 401 }));
  });
  const client = new CoreClient({
    baseUrl: () => 'https://core.example',
    identity: { clientId: 'editor', clientName: () => 'Editor' },
    authorization: () => auth.authorization(),
    onUnauthorized: () => auth.recover(),
    fetch: fetcher as unknown as typeof fetch,
  });
  // Core never saw that sign-in, so it cannot have refused it: it is sent
  // instead of being forgotten, which would ask for it at the next start.
  await expect(client.me()).resolves.toEqual({ id: 'thomas' });
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(auth.signedIn).toBe(true);
  expect(raw).toContain('thomas');
});
