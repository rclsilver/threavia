import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import { refresh, RefusedError, type Tokens } from '../../../web/ui/src/auth/oidc';
import { SessionRenewal } from '../../../web/ui/src/auth/renewal';
import { authorizedFetch, setBearer, setTokenRenewal } from '../../../web/ui/src/api/client';

const initial = (): Tokens => ({ accessToken: 'a1', refreshToken: 'r1', expiresAt: Date.now() + 300_000 });
const config = { mode: 'oidc' as const, issuer: 'https://sso.example/realm', clientId: 'threavia' };

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
});
afterEach(() => {
  setTokenRenewal(null);
  setBearer(null);
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('keeps or rotates the refresh token according to the provider response', async () => {
  const fetcher = vi.fn()
    .mockResolvedValueOnce(Response.json({ token_endpoint: 'https://sso.example/token' }))
    .mockResolvedValueOnce(Response.json({ access_token: 'a2', expires_in: 300 }))
    .mockResolvedValueOnce(Response.json({ token_endpoint: 'https://sso.example/token' }))
    .mockResolvedValueOnce(Response.json({ access_token: 'a3', refresh_token: 'r3', expires_in: 300 }));
  vi.stubGlobal('fetch', fetcher);
  expect(await refresh(config, 'r1')).toMatchObject({ accessToken: 'a2', refreshToken: 'r1' });
  expect(await refresh(config, 'r1')).toMatchObject({ accessToken: 'a3', refreshToken: 'r3' });
  const request = fetcher.mock.calls[1][1] as RequestInit;
  expect(new URLSearchParams(request.body as string).get('grant_type')).toBe('refresh_token');
});

it('distinguishes a refused grant from a temporary provider outage', async () => {
  const fetcher = vi.fn().mockImplementation((url: string) => Promise.resolve(
    url.includes('.well-known') ? Response.json({ token_endpoint: 'https://sso.example/token' }) : new Response('', { status: 503 }),
  ));
  vi.stubGlobal('fetch', fetcher);
  await expect(refresh(config, 'r1')).rejects.not.toBeInstanceOf(RefusedError);
  fetcher.mockImplementation((url: string) => Promise.resolve(
    url.includes('.well-known') ? Response.json({ token_endpoint: 'https://sso.example/token' }) : Response.json({ error: 'invalid_grant' }, { status: 400 }),
  ));
  await expect(refresh(config, 'r1')).rejects.toBeInstanceOf(RefusedError);
});

it('retries a timer renewal after an outage without discarding the session', async () => {
  const exchange = vi.fn().mockRejectedValueOnce(new TypeError('offline')).mockResolvedValue({ ...initial(), accessToken: 'a2' });
  const changed = vi.fn();
  const expired = vi.fn();
  const session = new SessionRenewal(exchange, changed, expired);
  session.hold(initial());
  await vi.advanceTimersByTimeAsync(240_000);
  expect(exchange).toHaveBeenCalledTimes(1);
  expect(changed).toHaveBeenCalledTimes(1);
  expect(expired).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(30_000);
  expect(exchange).toHaveBeenCalledTimes(2);
  expect(changed).toHaveBeenLastCalledWith(expect.objectContaining({ accessToken: 'a2' }));
  session.dispose();
});

it('renews on wake or request even when the browser did not run its timer', async () => {
  let finish!: (tokens: Tokens) => void;
  const exchange = vi.fn(() => new Promise<Tokens>((resolve) => { finish = resolve; }));
  const changed = vi.fn();
  const session = new SessionRenewal(exchange, changed, vi.fn());
  session.hold(initial());
  vi.setSystemTime(Date.now() + 600_000);
  const wake = session.prepare();
  const request = session.prepare();
  const rejected = session.recover();
  expect(exchange).toHaveBeenCalledTimes(1);
  finish({ ...initial(), accessToken: 'a2', refreshToken: 'r2' });
  await Promise.all([wake, request, rejected]);
  expect(changed).toHaveBeenLastCalledWith(expect.objectContaining({ refreshToken: 'r2' }));
  session.dispose();
});

it('only restarts sign-in when the provider refuses the grant', async () => {
  const changed = vi.fn();
  const expired = vi.fn();
  const session = new SessionRenewal(vi.fn().mockRejectedValue(new RefusedError('invalid_grant')), changed, expired);
  session.hold({ ...initial(), expiresAt: 0 });
  await expect(session.prepare()).rejects.toBeInstanceOf(RefusedError);
  expect(changed).toHaveBeenLastCalledWith(null);
  expect(expired).toHaveBeenCalledTimes(1);
  expect(await session.recover()).toBe(false);
  expect(vi.getTimerCount()).toBe(0);
  session.dispose();
});

it('ignores a renewal that completes after the provider was unmounted', async () => {
  let finish!: (tokens: Tokens) => void;
  const changed = vi.fn();
  const session = new SessionRenewal(() => new Promise((resolve) => { finish = resolve; }), changed, vi.fn());
  session.hold(initial());
  const pending = session.recover();
  session.dispose();
  finish({ ...initial(), accessToken: 'late' });
  expect(await pending).toBe(false);
  expect(changed).toHaveBeenCalledTimes(1);
  expect(vi.getTimerCount()).toBe(0);
});

it('retries a Core 401 once with renewed headers and the same request body', async () => {
  const prepare = vi.fn().mockResolvedValue(undefined);
  const recover = vi.fn(() => { setBearer('a2'); return Promise.resolve(true); });
  setTokenRenewal({ prepare, recover });
  setBearer('a1');
  const fetcher = vi.fn().mockResolvedValueOnce(new Response('', { status: 401 })).mockResolvedValueOnce(new Response('', { status: 200 }));
  vi.stubGlobal('fetch', fetcher);
  const response = await authorizedFetch('/api/v1/command', { method: 'POST', body: '{"value":1}' });
  expect(response.status).toBe(200);
  expect(prepare).toHaveBeenCalledTimes(1);
  expect(recover).toHaveBeenCalledTimes(1);
  const first = fetcher.mock.calls[0][1] as RequestInit;
  const second = fetcher.mock.calls[1][1] as RequestInit;
  expect((first.headers as Headers).get('Authorization')).toBe('Bearer a1');
  expect((second.headers as Headers).get('Authorization')).toBe('Bearer a2');
  expect(second.body).toBe('{"value":1}');
});

it('does not renew twice when another request already replaced the rejected token', async () => {
  const recover = vi.fn();
  setTokenRenewal({ prepare: vi.fn().mockResolvedValue(undefined), recover });
  setBearer('a1');
  const fetcher = vi.fn().mockImplementationOnce(() => {
    setBearer('a2');
    return Promise.resolve(new Response('', { status: 401 }));
  }).mockResolvedValueOnce(new Response('', { status: 200 }));
  vi.stubGlobal('fetch', fetcher);
  expect((await authorizedFetch('/api/v1/me')).status).toBe(200);
  expect(recover).not.toHaveBeenCalled();
});

it('stops after one unauthorized retry and leaves server failures to the caller', async () => {
  const recover = vi.fn().mockResolvedValue(true);
  setTokenRenewal({ prepare: vi.fn().mockResolvedValue(undefined), recover });
  const fetcher = vi.fn().mockResolvedValue(new Response(null, { status: 401 }));
  vi.stubGlobal('fetch', fetcher);
  expect((await authorizedFetch('/api/v1/me')).status).toBe(401);
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(recover).toHaveBeenCalledTimes(1);
  recover.mockClear();
  fetcher.mockResolvedValue(new Response(null, { status: 503 }));
  expect((await authorizedFetch('/api/v1/me')).status).toBe(503);
  expect(recover).not.toHaveBeenCalled();
});

it('does not send an expired bearer when renewal failed on a suspended device', async () => {
  setTokenRenewal({ prepare: vi.fn().mockRejectedValue(new TypeError('offline')), recover: vi.fn() });
  setBearer('expired');
  const fetcher = vi.fn();
  vi.stubGlobal('fetch', fetcher);
  await expect(authorizedFetch('/api/v1/events', { headers: { Accept: 'text/event-stream' } })).rejects.toThrow('offline');
  expect(fetcher).not.toHaveBeenCalled();
});
