import { describe, expect, it } from 'vitest';

import { CoreClient } from '../src/api/client';
import type { ExecutionPolicy, PolicyView } from '../src/api/types';
import { permissionsPage } from '../src/permissions/page';

const policy: ExecutionPolicy = { mode: 'GUARDED', allowFilesystemWrite: true, allowGitCommit: true, allowGitPush: false, allowNetwork: true };
const parent: ExecutionPolicy = { ...policy, rules: [{ effect: 'DENY', capability: 'SHELL', match: 'kubectl delete *', note: 'Keep production safe' }] };
const view: PolicyView = { effective: parent, parent, inherited: true };

describe('permissions across Core and the editor', () => {
  it('reads origins and resets the selected level without copying inherited rules', async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    const client = new CoreClient({
      baseUrl: () => 'https://core.example', authorization: () => Promise.resolve(undefined), identity: { clientId: 'editor', clientName: () => 'VS Code' },
      fetch: (input, init) => { const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url; calls.push({ url, init: init! }); return Promise.resolve(Response.json(url.includes('/sessions/') ? { effective: parent, project: parent, inherited: true } : view)); },
    });
    expect(await client.policy('/api/v1/projects/p1/policy')).toEqual(view);
    expect(await client.policy('/api/v1/sessions/s1/policy')).toMatchObject(view);
    await client.setPolicy('/api/v1/projects/p1/policy', null);
    await client.setPolicy('/api/v1/backends/b1/policy', policy);
    expect(calls.map(({ url, init }) => [url, init.method, init.body])).toEqual([
      ['https://core.example/api/v1/projects/p1/policy/origin', 'GET', undefined],
      ['https://core.example/api/v1/sessions/s1/policy', 'GET', undefined],
      ['https://core.example/api/v1/projects/p1/policy', 'PUT', '{}'],
      ['https://core.example/api/v1/projects/p1/policy/origin', 'GET', undefined],
      ['https://core.example/api/v1/backends/b1/policy', 'PUT', JSON.stringify(policy)],
      ['https://core.example/api/v1/backends/b1/policy', 'GET', undefined],
    ]);
    for (const { init } of calls) expect((init.headers as Headers).get('X-Threavia-Channel')).toBe('vscode');
  });

  it('escapes labels and policy text before inserting them into a script', () => {
    const dangerous = '</script><script>attack()</script>';
    const html = permissionsPage('test-nonce', dangerous, { ...view, parent: { ...parent, supervision: dangerous } });
    expect(html).not.toContain(dangerous);
    expect(html).toContain('\\u003c/script>');
    expect(html).toContain("default-src 'none'");
    expect(html).toContain("script-src 'nonce-test-nonce'");
    expect(html).toContain('view.own ?? {...view.effective, rules:[]}');
  });
});
