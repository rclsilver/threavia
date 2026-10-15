import { expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import { EventStream } from '../../../web/ui/src/api/stream';
import { backendAudit } from '../../../web/ui/src/lib/backend-audit';
import type { AuditEntry } from '../../../web/ui/src/api/types';

it('refreshes account audit after backend notifications from another client', () => {
  const queries = new QueryClient();
  const stream = new EventStream(queries);
  for (const type of ['backend.registered', 'backend.revoked', 'backend.status_changed']) {
    queries.setQueryData(['audit'], []);
    stream.deliver({ id: type, type, sequence: 1, timestamp: new Date().toISOString(), payload: {} });
    expect(queries.getQueryState(['audit'])?.isInvalidated, type).toBe(true);
  }
});

it.each([
  ['backend.registered', { registrationFlow: 'token' }, 'Registered backend “Historical name”'],
  ['backend.claimed', {}, 'Claimed backend “Historical name”'],
  ['backend.revoked', {}, 'Revoked backend “Historical name”'],
  ['backend.availability_changed', { from: 'READY', to: 'OFFLINE' }, 'Backend “Historical name” is offline'],
])('describes %s after the backend has left the live list', (action, detail, title) => {
  const entry: AuditEntry = { id: 'history', ownerId: 'alex', actorId: 'alex', createdAt: new Date().toISOString(), action, subjectId: 'gone', detail: { name: 'Historical name', ...detail } };
  expect(backendAudit(entry)?.title).toBe(title);
});
