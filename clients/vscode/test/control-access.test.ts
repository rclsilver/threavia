import { expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import { demoRequest } from '../../../web/ui/src/demo/server';
import { EventStream } from '../../../web/ui/src/api/stream';
import type { components } from '../../../web/ui/src/api/schema';
import type { Event } from '../../../web/ui/src/api/types';
type Access = components['schemas']['ControlAccess'];

it('plays access creation and revocation without returning a saved secret', async () => {
  const created = await demoRequest('POST', '/api/v1/me/control-accesses', { label: 'Fixture controller', scopes: ['read', 'answer'] }) as { access: Access; token: string };
  expect(created.access.scopes).toEqual(['read', 'answer']);
  expect(created.access.scopes).not.toContain('approve');
  const list = await demoRequest('GET', '/api/v1/me/control-accesses', null) as { items: Access[] };
  expect(list.items).toContainEqual(created.access);
  expect(JSON.stringify(list)).not.toContain(created.token);
  await demoRequest('DELETE', `/api/v1/control-accesses/${created.access.id}`, null);
  const revoked = await demoRequest('GET', '/api/v1/me/control-accesses', null) as { items: Access[] };
  expect(revoked.items.find(a => a.id === created.access.id)?.revokedAt).toBeTruthy();
  await expect(demoRequest('POST', '/api/v1/me/control-accesses', { label: 'Bad rights', scopes: ['admin'] })).rejects.toMatchObject({ status: 400 });
});

it('refreshes accesses and audit when another client changes a grant', () => {
  const queries = new QueryClient();
  queries.setQueryData(['control-accesses'], { items: [] });
  queries.setQueryData(['audit'], []);
  new EventStream(queries).deliver({ id: 'fixture', type: 'control_access.changed', sequence: 1, timestamp: '2026-10-26T10:00:00Z', payload: { action: 'revoked', accessId: 'fixture' } } as Event);
  expect(queries.getQueryState(['control-accesses'])?.isInvalidated).toBe(true);
  expect(queries.getQueryState(['audit'])?.isInvalidated).toBe(true);
  queries.clear();
});
