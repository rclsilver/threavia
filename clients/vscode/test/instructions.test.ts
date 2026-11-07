import { JSDOM } from 'jsdom';
import { describe, expect, it } from 'vitest';

import { CoreClient } from '../src/api/client';
import type { InstructionsView } from '../src/api/types';
import { instructionsPage, type InstructionsScope } from '../src/instructions/page';

const dangerous = '</script><script>attack()</script><b>bold</b>';
const view: InstructionsView = { own: { agent: 'Use worktrees.', supervision: '' }, inherited: { agent: dangerous, supervision: 'Reading is ordinary.' } };

/** Runs the page as the webview would, recording what it sends to the extension. */
function open(scope: InstructionsScope, shown: InstructionsView = view) {
  const sent: unknown[] = [];
  const dom = new JSDOM(instructionsPage('test-nonce', 'Platform', shown, scope), {
    runScripts: 'dangerously',
    beforeParse(window) {
      Object.assign(window, { acquireVsCodeApi: () => ({ postMessage: (message: unknown) => sent.push(message), getState: () => undefined, setState: () => {} }) });
    },
  });
  const field = <T extends HTMLElement>(id: string) => dom.window.document.getElementById(id) as T;
  const receive = (data: unknown) => dom.window.dispatchEvent(new dom.window.MessageEvent('message', { data }));
  return { dom, sent, field, receive };
}

describe('layered instructions across Core and the editor', () => {
  it('reads and writes both texts of a level on its own path', async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    const client = new CoreClient({
      baseUrl: () => 'https://core.example', authorization: () => Promise.resolve(undefined), identity: { clientId: 'editor', clientName: () => 'VS Code' },
      fetch: (input, init) => { const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url; calls.push({ url, init: init! }); return Promise.resolve(Response.json(url.includes('/preview') ? { agent: 'A', supervision: 'S' } : view)); },
    });
    expect(await client.instructions('/api/v1/me/instructions')).toEqual(view);
    expect(await client.setInstructions('/api/v1/backends/b1/instructions', { agent: 'Be brief.', supervision: '' })).toEqual(view);
    expect(await client.instructionsPreview('p1', 'b 1')).toEqual({ agent: 'A', supervision: 'S' });
    expect(calls.map(({ url, init }) => [url, init.method, init.body])).toEqual([
      ['https://core.example/api/v1/me/instructions', 'GET', undefined],
      ['https://core.example/api/v1/backends/b1/instructions', 'PUT', JSON.stringify({ agent: 'Be brief.', supervision: '' })],
      ['https://core.example/api/v1/projects/p1/instructions/preview?backendId=b%201', 'GET', undefined],
    ]);
    for (const { init } of calls) expect((init.headers as Headers).get('X-Threavia-Channel')).toBe('vscode');
  });

  it('saves both texts from the page', () => {
    const { sent, field } = open('backend');
    expect(field<HTMLTextAreaElement>('agent').value).toBe('Use worktrees.');
    field<HTMLTextAreaElement>('supervision').value = 'Stop before a push.';
    field<HTMLButtonElement>('save').click();
    expect(sent).toEqual([{ type: 'save', instructions: { agent: 'Use worktrees.', supervision: 'Stop before a push.' } }]);
  });

  it('leaves out an unchanged project agent text, so a stale tab cannot revert it', () => {
    const { sent, field, receive } = open('project');
    field<HTMLTextAreaElement>('supervision').value = 'Stop before a push.';
    field<HTMLButtonElement>('save').click();
    expect(sent).toEqual([{ type: 'save', instructions: { supervision: 'Stop before a push.' } }]);
    receive({ type: 'saved', view: { ...view, own: { agent: 'Use worktrees.', supervision: 'Stop before a push.' } } });
    field<HTMLTextAreaElement>('agent').value = 'Use worktrees and rebase.';
    field<HTMLButtonElement>('save').click();
    expect(sent[1]).toEqual({ type: 'save', instructions: { agent: 'Use worktrees and rebase.', supervision: 'Stop before a push.' } });
  });

  it('bounds every text but the project agent text, which has never had a limit', () => {
    for (const scope of ['global', 'backend'] as const) {
      const { field } = open(scope);
      expect(field<HTMLTextAreaElement>('agent').maxLength).toBe(4000);
      expect(field<HTMLTextAreaElement>('supervision').maxLength).toBe(4000);
    }
    const { field } = open('project');
    expect(field('agent').hasAttribute('maxlength')).toBe(false);
    expect(field<HTMLTextAreaElement>('supervision').maxLength).toBe(4000);
  });

  it('shows the inherited text as text, never as markup', () => {
    const html = instructionsPage('test-nonce', dangerous, view, 'project');
    expect(html).not.toContain(dangerous);
    expect(html).toContain("default-src 'none'");
    expect(html).toContain("script-src 'nonce-test-nonce'");
    const { dom, field } = open('project');
    expect(field('inherited-agent').textContent).toBe(dangerous);
    expect(field('inherited-supervision').textContent).toBe('Reading is ordinary.');
    expect(dom.window.document.querySelector('b')).toBeNull();
    expect(dom.window.document.scripts).toHaveLength(1);
  });

  it('offers a preview only for a project and renders it as text', () => {
    expect(open('global').field('preview')).toBeNull();
    expect(open('backend').field('preview')).toBeNull();
    const { dom, sent, field, receive } = open('project');
    field<HTMLButtonElement>('preview').click();
    expect(sent).toEqual([{ type: 'preview' }]);
    receive({ type: 'preview', backend: 'Laptop', preview: { agent: dangerous, supervision: '' } });
    expect(field('preview-agent').textContent).toBe(dangerous);
    expect(field('preview-supervision').textContent).toBe('Nothing from the levels.');
    expect(field('preview-title').textContent).toContain('Laptop');
    expect(field('preview-panel').textContent).toContain("LOCAL_INSTRUCTIONS");
    expect(dom.window.document.querySelector('b')).toBeNull();
  });
});
