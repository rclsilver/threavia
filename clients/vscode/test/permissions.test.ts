import { JSDOM } from 'jsdom';
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

  it('asks what the reviewer should know only for a session, the levels above setting it with the instructions', () => {
    const supervised: PolicyView = { effective: { ...policy, mode: 'SUPERVISED' }, parent: policy, inherited: false, own: { ...policy, mode: 'SUPERVISED', supervision: 'Reading is ordinary.' } };
    const open = (session: boolean) => {
      const sent: { type: string; policy: ExecutionPolicy }[] = [];
      const dom = new JSDOM(permissionsPage('test-nonce', 'Level', supervised, session), {
        runScripts: 'dangerously',
        beforeParse(window) { Object.assign(window, { acquireVsCodeApi: () => ({ postMessage: (message: { type: string; policy: ExecutionPolicy }) => sent.push(message), getState: () => undefined, setState: () => {} }) }); },
      });
      const document = dom.window.document;
      (document.getElementById('save') as HTMLButtonElement).click();
      return { hidden: document.getElementById('supervision-label')!.hidden, sent };
    };
    // Instance, backend and project pages alike: Core refuses supervision there.
    const above = open(false);
    expect(above.hidden).toBe(true);
    expect(above.sent[0].policy).not.toHaveProperty('supervision');
    const session = open(true);
    expect(session.hidden).toBe(false);
    expect(session.sent[0].policy.supervision).toBe('Reading is ordinary.');
  });
});
