// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import type { HostMessage, SessionView, WebviewMessage } from '../src/conversation/protocol';
import { learningPrompt } from '../../shared/learnings';

vi.mock('../src/webview/markdown', () => ({ renderMarkdown: () => document.createElement('div') }));
afterEach(() => { document.body.replaceChildren(); vi.unstubAllGlobals(); });

it('offers capture for a completed result, preserves the composer and displays host errors', async () => {
  const postMessage = vi.fn<(message: WebviewMessage) => void>();
  document.body.dataset.sessionId = 'session';
  vi.stubGlobal('acquireVsCodeApi', () => ({ postMessage, getState: () => ({ sessionId: 'session' }), setState: vi.fn() }));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  await import('../src/webview/main');
  const view: SessionView = { title: 'Resolution', status: { label: 'Idle', tone: 'idle' },
    modelSettings: { model: '', effort: '', supported: false }, pending: {}, deliveries: [], validations: [], questions: [], more: false,
    learnings: { sourceJobId: 'source', pending: false } };
  const receive = (message: HostMessage) => window.dispatchEvent(new MessageEvent('message', { data: message }));
  receive({ type: 'session', view });
  receive({ type: 'events', reset: true, events: [{ id: 'request', sequence: 1, timestamp: '2026-10-27T10:00:00Z', type: 'user.message', payload: { text: learningPrompt('source') } }] });
  expect(document.querySelector('.message-user details summary')?.textContent).toBe('Capture learnings from this resolution');
  expect(document.querySelector('.message-user details')?.hasAttribute('open')).toBe(false);
  expect(document.querySelector('.message-user .message-text')?.textContent).toBe(learningPrompt('source'));
  const field = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]')!;
  field.value = 'Keep my draft'; field.dispatchEvent(new Event('input'));
  const button = [...document.querySelectorAll('button')].find(b => b.textContent?.includes('Capture learnings'))!;
  expect(button.hidden).toBe(false);
  button.click(); button.click();
  expect(postMessage.mock.calls.filter(([message]) => message.type === 'captureLearnings')).toHaveLength(1);
  expect(field.value).toBe('Keep my draft');
  receive({ type: 'session', view: { ...view, learnings: { pending: false, sourceJobId: 'source', error: 'Request not confirmed; check the conversation.' } } });
  expect(button.disabled).toBe(false);
  expect(document.querySelector('.learning-actions [role="alert"]')?.textContent).toContain('Request not confirmed');
  receive({ type: 'session', view: { ...view, learnings: { pending: false } } });
  expect(button.hidden).toBe(true);
  expect(field.value).toBe('Keep my draft');
});
