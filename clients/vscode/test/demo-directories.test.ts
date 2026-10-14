import { describe, expect, it } from 'vitest';

import type { BackendInstance, KnownDirectory } from '../../../web/ui/src/api/types';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

describe('the playable demo', () => {
  it('shares global directories, preserves edits and refuses to forget a directory used by a session', async () => {
    const directory = await demoRequest('POST', '/api/v1/directories', { name: 'shared-test', description: 'Shared notes', gitRemote: 'https://example.org/notes.git' }) as KnownDirectory;
    expect(directory.projectId).toBeUndefined();
    for (const projectId of [IDS.homelab, IDS.website]) {
      const list = await demoRequest('GET', `/api/v1/projects/${projectId}/directories`, null) as { items: KnownDirectory[] };
      expect(list.items.some((item) => item.id === directory.id)).toBe(true);
    }
    const updated = await demoRequest('PATCH', `/api/v1/directories/${directory.id}`, { description: '', gitRemote: '' }) as KnownDirectory;
    expect(updated.name).toBe('shared-test'); expect(updated.description).toBe(''); expect(updated.gitRemote).toBeUndefined();
    await demoRequest('POST', `/api/v1/directories/${directory.id}/bindings`, { backendInstanceId: IDS.laptop, path: '/home/alex/notes' });
    await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.website, backendInstanceId: IDS.laptop, workingDirectoryId: directory.id, message: 'Read the notes' });
    await expect(demoRequest('DELETE', `/api/v1/directories/${directory.id}`, null)).rejects.toMatchObject({ status: 409 });
    await demoRequest('DELETE', `/api/v1/directories/${directory.id}/bindings/${IDS.laptop}`, null);
    expect(await demoRequest('GET', `/api/v1/directories/${directory.id}/bindings`, null)).toEqual({ items: [] });
  });

  it('switches and clears the default backend and keeps a single default', async () => {
    await demoRequest('PUT', '/api/v1/me/default-backend', { backendInstanceId: IDS.nas });
    const list = await demoRequest('GET', '/api/v1/backends', null) as { items: BackendInstance[] };
    expect(list.items.filter((backend) => backend.isDefault).map((backend) => backend.id)).toEqual([IDS.nas]);
    await demoRequest('PUT', '/api/v1/me/default-backend', { backendInstanceId: null });
    const cleared = await demoRequest('GET', '/api/v1/backends', null) as { items: BackendInstance[] };
    expect(cleared.items.some((backend) => backend.isDefault)).toBe(false);
  });
});
