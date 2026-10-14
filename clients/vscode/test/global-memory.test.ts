import { describe, expect, it } from 'vitest';

import { CoreClient } from '../src/api/client';
import type { Decision } from '../src/api/types';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

describe('global memory across clients', () => {
  it('uses the selected Core’s global endpoints and retains project-specific endpoints', async () => {
    const calls: string[] = [];
    const client = new CoreClient({
      baseUrl: () => 'https://selected-core.example', authorization: () => Promise.resolve(undefined),
      identity: { clientId: 'editor', clientName: () => 'VS Code' },
      fetch: (input, init) => {
        const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
        calls.push(`${init?.method} ${url}`);
        return Promise.resolve(Response.json(init?.method === 'GET' ? { items: [] } : { id: 'decision', title: 'Fact' }));
      },
    });
    await client.decisions('', true);
    await client.createDecision('', { title: 'Shared fact', content: 'Across projects', importance: 'IMPORTANT' });
    await client.decisions('p1', false);
    await client.createDecision('p1', { title: 'Local fact', content: '', importance: 'NORMAL' });
    expect(calls).toEqual([
      'GET https://selected-core.example/api/v1/me/decisions?includeSuperseded=true',
      'POST https://selected-core.example/api/v1/me/decisions',
      'GET https://selected-core.example/api/v1/projects/p1/decisions',
      'POST https://selected-core.example/api/v1/projects/p1/decisions',
    ]);
  });

  it('plays global creation, replacement, search and revival without mixing in project facts', async () => {
    const first = await demoRequest('POST', '/api/v1/me/decisions', { title: 'Shared retention policy', content: 'Keep notes', importance: 'IMPORTANT' }) as Decision;
    expect(first.projectId).toBeUndefined();
    await demoRequest('POST', `/api/v1/projects/${IDS.website}/decisions`, { title: 'Local retention policy' });
    const replacement = await demoRequest('POST', '/api/v1/me/decisions', { title: 'Shared retention revised', supersedes: first.id }) as Decision;
    const search = await demoRequest('GET', '/api/v1/me/memory/search?q=retention', null) as { items: Decision[] };
    expect(search.items.map((entry) => entry.id)).toEqual([replacement.id]);
    await expect(demoRequest('POST', `/api/v1/projects/${IDS.website}/decisions`, { title: 'Wrong scope', supersedes: replacement.id })).rejects.toMatchObject({ status: 400 });
    await demoRequest('DELETE', `/api/v1/decisions/${replacement.id}`, null);
    const globals = await demoRequest('GET', '/api/v1/me/decisions', null) as { items: Decision[] };
    expect(globals.items.find((entry) => entry.id === first.id)?.status).toBe('ACTIVE');
    expect(globals.items.every((entry) => !entry.projectId)).toBe(true);
    const project = await demoRequest('GET', `/api/v1/projects/${IDS.website}/decisions`, null) as { items: Decision[] };
    expect(project.items.some((entry) => entry.id === first.id)).toBe(false);
  });
});
