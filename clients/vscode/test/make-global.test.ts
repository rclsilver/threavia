import { describe, expect, it } from 'vitest';

import { CoreClient } from '../src/api/client';
import type { KnownDirectory, KnownDirectoryBinding } from '../src/api/types';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

describe('making existing records global', () => {
  it('promotes through the selected Core and preserves decision history in the demo', async () => {
    const client = new CoreClient({
      baseUrl: () => 'https://selected-core.example', authorization: () => Promise.resolve(undefined),
      identity: { clientId: 'editor', clientName: () => 'VS Code' },
      fetch: async (input, init) => {
        const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url);
        const result = await demoRequest(init?.method ?? 'GET', url.pathname + url.search, typeof init?.body === 'string' ? JSON.parse(init.body) : null);
        return Response.json(result);
      },
    });
    const local = await client.createDecision(IDS.website, { title: 'Promotion test', content: 'Original reason', importance: 'IMPORTANT' });
    const global = await client.makeDecisionGlobal(local.id);
    expect(global).toMatchObject({ title: local.title, content: local.content, importance: local.importance, supersedes: local.id });
    expect(global.projectId).toBeUndefined();
    expect((await client.decisions(IDS.website, true)).find((d) => d.id === local.id)?.status).toBe('SUPERSEDED');
    expect((await client.decisions('', false)).some((d) => d.id === global.id)).toBe(true);
    expect((await client.makeDecisionGlobal(global.id)).id).toBe(global.id);
    await expect(client.makeDecisionGlobal(local.id)).rejects.toMatchObject({ status: 409 });
    await demoRequest('DELETE', `/api/v1/decisions/${global.id}`, null);
    expect((await client.decisions(IDS.website, false)).some((d) => d.id === local.id)).toBe(false);
  });

  it('keeps paths and identity, shares directories and leaves collisions unchanged', async () => {
    const local = await demoRequest('POST', `/api/v1/projects/${IDS.website}/directories`, { name: 'directory-promotion', description: 'Shared notes' }) as KnownDirectory;
    await demoRequest('POST', `/api/v1/directories/${local.id}/bindings`, { backendInstanceId: IDS.laptop, path: '/home/alex/shared' });
    const promoted = await demoRequest('POST', `/api/v1/directories/${local.id}/global`, null) as KnownDirectory;
    expect(promoted).toMatchObject({ id: local.id, name: local.name, description: local.description });
    expect(promoted.projectId).toBeUndefined();
    const paths = await demoRequest('GET', `/api/v1/directories/${local.id}/bindings`, null) as { items: KnownDirectoryBinding[] };
    expect(paths.items[0].path).toBe('/home/alex/shared');
    const list = await demoRequest('GET', `/api/v1/projects/${IDS.homelab}/directories`, null) as { items: KnownDirectory[] };
    expect(list.items.some((d) => d.id === local.id)).toBe(true);
    const collision = await demoRequest('POST', `/api/v1/projects/${IDS.website}/directories`, { name: local.name }) as KnownDirectory;
    await expect(demoRequest('POST', `/api/v1/directories/${collision.id}/global`, null)).rejects.toMatchObject({ status: 409 });
    expect(collision.projectId).toBe(IDS.website);
  });
});
