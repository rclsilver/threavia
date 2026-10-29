// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import type { WebviewMessage } from '../src/conversation/protocol';

vi.mock('../src/webview/markdown', () => ({ renderMarkdown: () => document.createElement('div') }));

it('tells the extension where the keyboard focus is, since a conversation is read only while it is in there', async () => {
  const postMessage = vi.fn<(message: WebviewMessage) => void>();
  document.body.dataset.sessionId = 'session';
  vi.stubGlobal('acquireVsCodeApi', () => ({ postMessage, getState: () => ({ sessionId: 'session' }), setState: vi.fn() }));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  await import('../src/webview/main');
  // Said at once: the editor may have given the page the focus before it ran.
  expect(postMessage).toHaveBeenCalledWith({ type: 'focus', focused: document.hasFocus() });
  window.dispatchEvent(new FocusEvent('blur'));
  expect(postMessage).toHaveBeenLastCalledWith({ type: 'focus', focused: false });
  window.dispatchEvent(new FocusEvent('focus'));
  expect(postMessage).toHaveBeenLastCalledWith({ type: 'focus', focused: true });
});
