// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import type { HostMessage, SessionView, WebviewMessage } from '../src/conversation/protocol';
import { createModelSettings } from '../src/webview/model-settings';

vi.mock('../src/webview/markdown', () => ({ renderMarkdown: () => document.createElement('div') }));
const settings: SessionView['modelSettings'] = {
  model: 'first', effort: 'high', supported: true,
  catalog: { defaultModel: 'first', defaultEffort: 'medium', models: [
    { id: 'first', name: 'First', efforts: ['low', 'medium', 'high'], defaultEffort: 'medium' },
    { id: 'second', name: 'Second', efforts: ['low'], defaultEffort: 'low' },
    { id: 'plain', name: 'Plain', efforts: [] },
  ] },
};
afterEach(() => { document.body.replaceChildren(); vi.unstubAllGlobals(); });

it('says the next job\'s model and effort in one pill, and asks for the Quick Pick on click', () => {
  const pick = vi.fn();
  const controls = createModelSettings(pick);
  document.body.append(controls.element, controls.error);
  const pill = controls.element.querySelector<HTMLButtonElement>('button')!;
  controls.update(settings, false);
  expect(pill.textContent).toContain('First');
  expect(pill.textContent).toContain('· high');
  pill.click();
  expect(pick).toHaveBeenCalledOnce();
  controls.update({ ...settings, model: '', effort: '' }, false);
  expect(pill.textContent).toContain('Default · First');
  expect(pill.textContent).toContain('· medium');
  controls.update({ ...settings, model: 'plain', effort: '' }, false);
  expect(pill.textContent).not.toContain('·');
  controls.update({ ...settings, model: 'gone', effort: '' }, false);
  expect(pill.textContent).toContain('gone (unavailable)');
  controls.update({ ...settings, error: 'Save refused' }, false);
  expect(controls.error.hidden).toBe(false);
  expect(controls.error.textContent).toContain('Save refused');
  controls.update({ ...settings, supported: false }, false);
  expect(pill.disabled).toBe(true);
  controls.update({ ...settings, saving: true }, true);
  expect(pill.disabled).toBe(true);
  expect(pill.querySelector('.codicon-loading')).not.toBeNull();
});

it('puts the pill in the one control line, gates sending while saving, and keeps the draft after a failed send', async () => {
  const postMessage = vi.fn<(message: WebviewMessage) => void>();
  document.body.dataset.sessionId = 'session';
  vi.stubGlobal('acquireVsCodeApi', () => ({ postMessage, getState: () => ({ sessionId: 'session' }), setState: vi.fn() }));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  await import('../src/webview/main');
  const view: SessionView = { title: 'Session', status: { label: 'Running', tone: 'running' }, modelSettings: settings,
    pending: {}, active: { id: 'job', status: 'RUNNING', model: 'first', effort: 'high' }, deliveries: ['NEXT'], validations: [], questions: [], more: false };
  const receive = (message: HostMessage) => window.dispatchEvent(new MessageEvent('message', { data: message }));
  receive({ type: 'session', view });
  expect(document.querySelector('header')?.textContent).not.toMatch(/Model:|Effort:|Current job:/);
  const toolbar = document.querySelector('.composer .toolbar')!;
  const pill = toolbar.querySelector<HTMLButtonElement>('[data-model-pill]')!;
  expect(toolbar.querySelector('button[aria-label="Attach files"]')).not.toBeNull();
  expect(toolbar.querySelector('[role="radiogroup"]')).not.toBeNull();
  expect(toolbar.textContent).not.toContain('Applies to new jobs');
  const field = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]')!;
  field.value = 'Keep this message'; field.dispatchEvent(new Event('input'));
  pill.click();
  expect(postMessage).toHaveBeenLastCalledWith({ type: 'pickModel' });
  receive({ type: 'session', view: { ...view, modelSettings: { ...settings, saving: true } } });
  field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
  expect(field.value).toBe('Keep this message');
  expect(postMessage.mock.calls.filter(([message]) => message.type === 'send')).toHaveLength(0);
  receive({ type: 'session', view: { ...view, modelSettings: { ...settings, model: 'second', effort: '' } } });
  expect(pill.textContent).toContain('Second');
  field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
  expect(postMessage).toHaveBeenLastCalledWith(expect.objectContaining({ type: 'send', text: 'Keep this message' }));
  expect(pill.disabled).toBe(true);
  receive({ type: 'sendFailed', text: 'Keep this message', error: 'Offline' });
  expect(field.value).toBe('Keep this message');
  expect(pill.disabled).toBe(false);
  // Idle: the line keeps the paperclip and the pill, without delivery or Stop.
  receive({ type: 'session', view: { ...view, active: undefined, deliveries: [] } });
  expect(toolbar.querySelector('[data-model-pill]')).toBe(pill);
  expect(toolbar.querySelector('[role="radiogroup"]')).toBeNull();
});
