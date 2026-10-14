import { describe, expect, it } from 'vitest';
import type { KnownDirectory, Session, Snapshot } from '../../../web/ui/src/api/types';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

describe('session directory in the demo', () => {
  it('remembers an attachment, preserves it on unrelated updates and clears it explicitly', async () => {
    const directory = await demoRequest('POST', '/api/v1/directories', { name: 'session-docs', description: 'Notes' }) as KnownDirectory;
    const created = await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'Work on the docs' }) as { session: Session };
    const path = `/api/v1/sessions/${created.session.id}`;
    await demoRequest('PATCH', path, { workingDirectoryId: directory.id });
    let snapshot = await demoRequest('GET', path, null) as Snapshot;
    expect(snapshot.session.workingDirectoryId).toBe(directory.id);
    expect(snapshot.events.some((event) => event.type === 'working_directory.changed' && event.payload?.knownDirectoryId === directory.id)).toBe(true);
    await expect(demoRequest('DELETE', `/api/v1/directories/${directory.id}`, null)).rejects.toMatchObject({ status: 409 });
    await demoRequest('PATCH', path, { title: 'Notes session' });
    snapshot = await demoRequest('GET', path, null) as Snapshot;
    expect(snapshot.session.workingDirectoryId).toBe(directory.id);
    const foreign = await demoRequest('POST', `/api/v1/projects/${IDS.website}/directories`, { name: 'foreign-docs' }) as KnownDirectory;
    await expect(demoRequest('PATCH', path, { workingDirectoryId: foreign.id })).rejects.toMatchObject({ status: 400 });
    await demoRequest('PATCH', path, { workingDirectoryId: null });
    snapshot = await demoRequest('GET', path, null) as Snapshot;
    expect(snapshot.session.workingDirectoryId).toBeUndefined();
    await demoRequest('DELETE', `/api/v1/directories/${directory.id}`, null);
  });
});
