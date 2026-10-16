// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { markdownArtifactPage } from '../src/conversation/markdown-artifact';

afterEach(() => {
  window.dispatchEvent(new Event('pagehide'));
  document.body.replaceChildren();
});

it('opens the rendered document, toggles the unchanged source, isolates diagrams and follows the theme', async () => {
  Object.defineProperty(SVGElement.prototype, 'getBBox', { configurable: true, value: () => ({ x: 0, y: 0, width: 160, height: 24 }) });
  Object.defineProperty(SVGElement.prototype, 'getComputedTextLength', { configurable: true, value: () => 160 });
  const text = '\n# Readable report\n\n```mermaid\nflowchart LR\nA --> B\n```\n\n```mermaid\ninvalid diagram syntax\n```';
  const page = markdownArtifactPage({ title: 'Report', text, cspSource: 'https://extension.test', scriptUri: '/artifact.js', styleUri: '/artifact.css', nonce: 'test' });
  const parsed = new DOMParser().parseFromString(page, 'text/html');
  document.body.innerHTML = parsed.body.innerHTML;
  await import('../src/webview/artifact');
  const source = document.querySelector<HTMLElement>('#source')!;
  const preview = document.querySelector<HTMLElement>('#preview')!;
  expect(preview.hidden).toBe(false);
  expect(source.hidden).toBe(true);
  expect(source.textContent).toBe(text);
  await vi.waitFor(() => expect(preview.querySelector('iframe')).not.toBeNull());
  const iframe = preview.querySelector('iframe')!;
  expect(iframe.getAttribute('sandbox')).toBe('');
  expect(iframe.srcdoc).toContain("default-src 'none'");
  await vi.waitFor(() => expect(preview.textContent).toContain('This diagram could not be rendered.'));
  expect(preview.textContent).toContain('invalid diagram syntax');
  document.querySelector<HTMLButtonElement>('[data-view=source]')!.click();
  expect(preview.hidden).toBe(true);
  expect(source.hidden).toBe(false);
  expect(source.textContent).toBe(text);
  document.querySelector<HTMLButtonElement>('[data-view=preview]')!.click();
  expect(preview.hidden).toBe(false);
  document.body.className = 'vscode-dark';
  await vi.waitFor(() => expect(preview.querySelector('iframe')?.srcdoc).toContain('color-scheme:dark'));
  expect(source.textContent).toBe(text);
}, 10_000);
