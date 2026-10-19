import { webcrypto } from 'node:crypto';
import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => { vi.unstubAllGlobals(); vi.resetModules(); });

it('starts a fresh Android HTTP client and sends commands without secure-context randomUUID', async () => {
  const values = new Map<string, string>();
  vi.stubGlobal('crypto', { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) });
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  });
  vi.stubGlobal('navigator', { userAgent: 'Android Chrome/143.0' });
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  const fetcher = vi.fn().mockResolvedValue(Response.json({ ok: true }));
  vi.stubGlobal('fetch', fetcher);

  const client = await import('../../../web/ui/src/api/client');
  const device = await import('../../../web/ui/src/lib/device');
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  expect(device.clientId).toMatch(uuid);
  expect(values.get('threavia.client.id')).toBe(device.clientId);
  const first = new Headers(client.idempotencyKey()).get('Idempotency-Key');
  const second = new Headers(client.idempotencyKey()).get('Idempotency-Key');
  expect(first).toMatch(uuid);
  expect(second).toMatch(uuid);
  expect(first).not.toBe(second);
  await client.api.post('/api/v1/test', {}, client.idempotencyKey());
  const init = fetcher.mock.calls[0][1] as RequestInit;
  expect(new Headers(init.headers).get('X-Threavia-Client')).toBe(device.clientId);
  expect(new Headers(init.headers).get('Idempotency-Key')).toMatch(uuid);

  const { uuid: newId } = await import('../../../web/ui/src/lib/uuid');
  const { demoReview, demoReviewAction } = await import('../../../web/ui/src/demo/review');
  const session = newId();
  let review = structuredClone(demoReview(session));
  const revisionId = review.revisions[0].id;
  review = structuredClone(demoReviewAction(session, { action: 'comment', expectedVersion: review.version, revisionId, body: 'HTTP phone review' }));
  review = structuredClone(demoReviewAction(session, { action: 'publish', expectedVersion: review.version, revisionId, batchId: newId() }));
  expect(review.threads[0].messages[0].draft).toBe(false);
  expect(review.revisions).toHaveLength(2);

  vi.resetModules();
  expect((await import('../../../web/ui/src/lib/device')).clientId).toBe(device.clientId);
});

it('keeps using the native secure-context UUID generator when available', async () => {
  const native = vi.fn(() => '00000000-0000-4000-8000-000000000001');
  vi.stubGlobal('crypto', { randomUUID: native });
  const { uuid } = await import('../../../web/ui/src/lib/uuid');
  expect(uuid()).toBe('00000000-0000-4000-8000-000000000001');
  expect(native).toHaveBeenCalledOnce();
});
