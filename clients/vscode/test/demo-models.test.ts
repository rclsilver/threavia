import { describe, expect, it } from 'vitest';
import type { Snapshot } from '../../../web/ui/src/api/types';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

describe('session models in the playable demo', () => {
  it('saves preferences independently, refuses incompatible efforts and retains job settings', async () => {
    const first = await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'First', model: 'opus', effort: 'max' }) as { session: { id: string } };
    const other = await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'Other', model: 'sonnet', effort: 'low' }) as { session: { id: string } };
    const path = `/api/v1/sessions/${first.session.id}`;
    await demoRequest('PATCH', path, { model: 'sonnet', effort: 'medium' });
    let snapshot = await demoRequest('GET', path, null) as Snapshot;
    expect(snapshot.session).toMatchObject({ model: 'sonnet', effort: 'medium' });
    expect(snapshot.jobs[0]).toMatchObject({ model: 'opus', effort: 'max' });
    const unchanged = await demoRequest('GET', `/api/v1/sessions/${other.session.id}`, null) as Snapshot;
    expect(unchanged.session).toMatchObject({ model: 'sonnet', effort: 'low' });
    await expect(demoRequest('PATCH', path, { model: 'sonnet', effort: 'max' })).rejects.toMatchObject({ status: 400 });
    await demoRequest('POST', `${path}/messages`, { message: 'Next' });
    snapshot = await demoRequest('GET', path, null) as Snapshot;
    expect(snapshot.jobs.at(-1)).toMatchObject({ model: 'sonnet', effort: 'medium' });
    expect(snapshot.events.some((entry) => entry.type === 'session.model_changed')).toBe(true);
  });
});
