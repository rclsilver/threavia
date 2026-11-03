// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { markdownArtifactPage } from '../src/conversation/markdown-artifact';

let saved: { fullWidth?: boolean } | undefined;
const setState = vi.fn((state: { fullWidth: boolean }) => { saved = state; });

beforeEach(() => {
  saved = undefined;
  setState.mockClear();
  vi.stubGlobal('acquireVsCodeApi', () => ({ getState: () => saved, setState }));
});

afterEach(() => {
  window.dispatchEvent(new Event('pagehide'));
  document.body.replaceChildren();
  document.documentElement.className = '';
  vi.unstubAllGlobals();
});

/** Loads the page as VS Code does: markup first, then its script, run afresh. */
async function open(text: string) {
  const page = markdownArtifactPage({ title: 'Report', text, cspSource: 'https://extension.test', scriptUri: '/artifact.js', styleUri: '/artifact.css', nonce: 'test' });
  document.body.innerHTML = new DOMParser().parseFromString(page, 'text/html').body.innerHTML;
  vi.resetModules();
  await import('../src/webview/artifact');
}

it('opens the rendered document, toggles the unchanged source, isolates diagrams and follows the theme', async () => {
  Object.defineProperty(SVGElement.prototype, 'getBBox', { configurable: true, value: () => ({ x: 0, y: 0, width: 160, height: 24 }) });
  Object.defineProperty(SVGElement.prototype, 'getComputedTextLength', { configurable: true, value: () => 160 });
  const text = '\n# Readable report\n\n```mermaid\nflowchart LR\nA --> B\n```\n\n```mermaid\ninvalid diagram syntax\n```';
  await open(text);
  const source = document.querySelector<HTMLElement>('#source')!;
  const preview = document.querySelector<HTMLElement>('#preview')!;
  expect(preview.hidden).toBe(false);
  expect(source.hidden).toBe(true);
  expect(source.textContent).toBe(text);
  // Rendering lazy-loads Mermaid; a cold import can exceed one second on CI.
  await vi.waitFor(() => expect(preview.querySelector('iframe')).not.toBeNull(), { timeout: 5_000 });
  const iframe = preview.querySelector('iframe')!;
  expect(iframe.getAttribute('sandbox')).toBe('');
  expect(iframe.srcdoc).toContain("default-src 'none'");
  await vi.waitFor(() => expect(preview.textContent).toContain('This diagram could not be rendered.'));
  // Widening the page is not a change of theme: the diagrams already drawn stay.
  document.querySelector<HTMLButtonElement>('#full-width')!.click();
  await new Promise((resolve) => setTimeout(resolve));
  expect(preview.querySelector('iframe')).toBe(iframe);
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

it('lets the reader take the whole panel for a wide document, and keeps that when the panel comes back', async () => {
  await open('| A | B |\n|---|---|\n| 1 | 2 |');
  const root = document.documentElement;
  const toggle = () => document.querySelector<HTMLButtonElement>('#full-width')!;
  // A document opens at the reading width, as the conversation does.
  expect(root.classList.contains('full-width')).toBe(false);
  expect(toggle().getAttribute('aria-pressed')).toBe('false');

  toggle().click();
  expect(root.classList.contains('full-width')).toBe(true);
  expect(toggle().getAttribute('aria-pressed')).toBe('true');

  // A hidden panel loses its page; VS Code reloads it with the state it saved.
  window.dispatchEvent(new Event('pagehide'));
  root.className = '';
  await open('| A | B |\n|---|---|\n| 1 | 2 |');
  expect(root.classList.contains('full-width')).toBe(true);
  expect(toggle().getAttribute('aria-pressed')).toBe('true');

  toggle().click();
  expect(root.classList.contains('full-width')).toBe(false);
  expect(setState).toHaveBeenLastCalledWith({ fullWidth: false });
});
