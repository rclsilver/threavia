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

it('keeps selections focused during activity, resets incompatible effort, and restores a refused save', () => {
  const changed = vi.fn();
  const controls = createModelSettings(changed);
  document.body.append(controls.element);
  controls.update(settings, false, true);
  const [model, effort] = controls.element.querySelectorAll('select');
  model.focus();
  controls.update({ ...settings }, false, true);
  expect(document.activeElement).toBe(model);
  model.value = 'second'; model.dispatchEvent(new Event('change'));
  expect(changed).toHaveBeenLastCalledWith('second', '');
  controls.update({ ...settings, error: 'Save refused' }, false, true);
  expect(model.value).toBe('first');
  expect(effort.value).toBe('high');
  expect(controls.element.textContent).toContain('Save refused');
  controls.update({ ...settings, model: 'second', effort: '' }, false, false);
  expect([...effort.options].map(option => option.value)).toEqual(['', 'low']);
  controls.update({ ...settings, model: 'plain', effort: '' }, false, false);
  expect(effort.disabled).toBe(true);
  controls.update({ ...settings, supported: false }, false, false);
  expect(model.disabled).toBe(true);
});

it('uses inline selects, gates sending until saved, and keeps the draft after a failed send', async () => {
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
  const model = document.querySelector<HTMLSelectElement>('.composer select[aria-label="Model"]')!;
  const field = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]')!;
  field.value = 'Keep this message'; field.dispatchEvent(new Event('input'));
  model.value = 'second'; model.dispatchEvent(new Event('change'));
  expect(postMessage).toHaveBeenLastCalledWith({ type: 'setModelSettings', model: 'second', effort: '' });
  field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
  expect(field.value).toBe('Keep this message');
  expect(postMessage.mock.calls.filter(([message]) => message.type === 'send')).toHaveLength(0);
  model.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  expect(postMessage.mock.calls.filter(([message]) => message.type === 'stop')).toHaveLength(0);
  receive({ type: 'session', view: { ...view, modelSettings: { ...settings, model: 'second', effort: '' } } });
  expect(model.value).toBe('second');
  field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
  expect(postMessage).toHaveBeenLastCalledWith(expect.objectContaining({ type: 'send', text: 'Keep this message' }));
  expect(model.disabled).toBe(true);
  receive({ type: 'sendFailed', text: 'Keep this message', error: 'Offline' });
  expect(field.value).toBe('Keep this message');
  expect(model.disabled).toBe(false);
});
