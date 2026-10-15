import { expect, it } from 'vitest';
import { QueryClient } from '../../../web/ui/node_modules/@tanstack/react-query';
import type { HandoffPreview, Snapshot } from '../../../web/ui/src/api/types';
import { EventStream } from '../../../web/ui/src/api/stream';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoLifecycleEvents, demoRequest } from '../../../web/ui/src/demo/server';
import { describe, rowsOf } from '../src/conversation/timeline';

it('previews a destination, confirms the reviewed run and tells both clients about the move', async () => {
  const created = await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'Prepare the move' }) as { session: { id: string } };
  const path = `/api/v1/sessions/${created.session.id}`;
  const before = await demoRequest('GET', path, null) as Snapshot;
  const preview = await demoRequest('GET', `${path}/handoff?backendInstanceId=${IDS.nas}`, null) as HandoffPreview;
  expect(preview).toMatchObject({ canMove: true, fromRunId: before.runs[0].id, nativeSessionTransferred: false, backend: { id: IDS.nas } });
  expect((await demoRequest('GET', path, null) as Snapshot).runs).toEqual(before.runs);
  const queries = new QueryClient();
  queries.setQueryData(['audit'], []);
  queries.setQueryData(['snapshot', created.session.id], before);
  queries.setQueryData(['repository', created.session.id], {});
  const web = new EventStream(queries);
  const stop = demoLifecycleEvents((event) => web.deliver(event));
  try {
    await demoRequest('PATCH', path, { backendInstanceId: IDS.nas, expectedRunId: preview.fromRunId });
    expect(queries.getQueryState(['audit'])?.isInvalidated).toBe(true);
    expect(queries.getQueryState(['snapshot', created.session.id])?.isInvalidated).toBe(true);
    expect(queries.getQueryState(['repository', created.session.id])?.isInvalidated).toBe(true);
    const moved = await demoRequest('GET', path, null) as Snapshot;
    expect(moved.runs).toHaveLength(2);
    const notice = moved.events.find((event) => event.type === 'run.created')!;
    expect(notice.payload).toMatchObject({ fromBackendInstanceId: IDS.laptop, backendInstanceId: IDS.nas });
    expect(rowsOf([notice]).filter((row) => row.kind === 'event')).toHaveLength(1);
    expect(describe(notice).text).toContain('fresh provider session');
    await expect(demoRequest('PATCH', path, { backendInstanceId: IDS.laptop, expectedRunId: preview.fromRunId })).rejects.toMatchObject({ status: 409 });
    await expect(demoRequest('PATCH', path, { backendInstanceId: IDS.laptop, title: 'Must not rename' })).rejects.toMatchObject({ status: 400 });
    expect((await demoRequest('GET', path, null) as Snapshot).session.title).toBe(before.session.title);
    await demoRequest('PATCH', path, { model: 'demo-codex' });
    const incompatible = await demoRequest('GET', `${path}/handoff?backendInstanceId=${IDS.laptop}`, null) as HandoffPreview;
    expect(incompatible.canMove).toBe(false);
    expect(incompatible.reasons.join(' ')).toContain('model');
  } finally { stop(); queries.clear(); }
});
