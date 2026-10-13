import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import type { CoreClient } from '../src/api/client';
import type { UserInputRequest } from '../src/api/types';

const host = vi.hoisted(() => {
  function create() {
    const accepted = new Set<() => void>();
    const hidden = new Set<() => void>();
    const widget = {
      value: '', items: [] as { label: string }[], selectedItems: [] as { label: string }[],
      shown: false, disposed: false, validationMessage: undefined as string | undefined,
      onDidAccept: (callback: () => void) => { accepted.add(callback); return { dispose: () => { accepted.delete(callback); } }; },
      onDidHide: (callback: () => void) => { hidden.add(callback); return { dispose: () => { hidden.delete(callback); } }; },
      show: () => { widget.shown = true; },
      dispose: () => { widget.disposed = true; },
      accept: () => { for (const callback of accepted) callback(); },
      hide: () => { for (const callback of hidden) callback(); },
    };
    return widget;
  }
  const widgets: ReturnType<typeof create>[] = [];
  return { widgets, create: () => { const widget = create(); widgets.push(widget); return widget; } };
});
vi.mock('vscode', () => ({ window: {
  createInputBox: host.create, createQuickPick: host.create,
  setStatusBarMessage: vi.fn(), showInformationMessage: vi.fn(), showErrorMessage: vi.fn(),
} }));

import { Answers } from '../src/attention/answer';
import { AttentionStore } from '../src/attention/store';
import { EventBus } from '../src/api/events';

const request: UserInputRequest = { id: 'question', scope: { projectId: 'project', sessionId: 'session', runId: 'run', jobId: 'job' }, createdAt: '2026-10-13T11:59:00Z', prompt: 'Environment?', freeText: true, notify: true, status: 'PENDING' };
const stores: AttentionStore[] = [];
beforeEach(() => { host.widgets.length = 0; });
afterEach(() => { for (const store of stores.splice(0)) store.dispose(); });
async function setup(choices?: string[], loaded = true) {
  const question = { ...request, choices };
  const client = { attention: () => Promise.resolve({ userInputs: [question], validations: [] }), resolveUserInput: vi.fn().mockResolvedValue(question) };
  const bus = new EventBus();
  const attention = new AttentionStore(client as unknown as CoreClient, bus);
  stores.push(attention);
  if (loaded) await attention.refresh();
  return { question, client, bus, attention, answers: new Answers(client as unknown as CoreClient, attention) };
}

it.each([undefined, ['staging', 'production']])('closes an open input/picker when its turn is interrupted (%s)', async (choices) => {
  const { question, answers, client, bus } = await setup(choices);
  const pending = answers.ask(question);
  expect(host.widgets[0].shown).toBe(true);
  bus.publish({ id: 'e', sequence: 1, type: 'user.message', timestamp: '2026-10-13T12:00:00Z', jobId: 'job', sessionId: 'session', payload: { text: 'Change course', delivery: 'NOW' } });
  await pending;
  expect(host.widgets[0].disposed).toBe(true);
  expect(client.resolveUserInput).not.toHaveBeenCalled();
});

it('closes an open field when the question is answered elsewhere', async () => {
  const { question, answers, attention, client } = await setup();
  const pending = answers.ask(question);
  attention.drop(undefined, question.id);
  await pending;
  expect(host.widgets[0].disposed).toBe(true);
  expect(client.resolveUserInput).not.toHaveBeenCalled();
});

it('closes a snapshot’s input even before the global attention list first loads', async () => {
  const { question, answers, client, bus } = await setup(undefined, false);
  const pending = answers.ask(question);
  bus.publish({ id: 'e', sequence: 1, type: 'user.message', timestamp: '2026-10-13T12:00:00Z', jobId: 'job', sessionId: 'session', payload: { text: 'Stop asking', delivery: 'NOW' } });
  await pending;
  expect(host.widgets[0].disposed).toBe(true);
  expect(client.resolveUserInput).not.toHaveBeenCalled();
});

it('keeps an empty answer open, and submits a valid answer once', async () => {
  const { question, answers, client } = await setup();
  const pending = answers.ask(question);
  const input = host.widgets[0];
  input.value = '   ';
  input.accept();
  expect(input.disposed).toBe(false);
  expect(input.validationMessage).toBe('Write an answer.');
  input.value = ' staging ';
  input.accept();
  await pending;
  expect(input.disposed).toBe(true);
  expect(client.resolveUserInput).toHaveBeenCalledExactlyOnceWith(question.id, 'staging');
});

it('keeps choice and free-text answers working, and a dismissed field sends nothing', async () => {
  const { question, answers, client } = await setup(['staging']);
  const pending = answers.ask(question);
  host.widgets[0].selectedItems = [{ label: '$(edit) Something else…' }];
  host.widgets[0].accept();
  await vi.waitFor(() => expect(host.widgets).toHaveLength(2));
  host.widgets[1].value = 'preview';
  host.widgets[1].accept();
  await pending;
  expect(client.resolveUserInput).toHaveBeenCalledExactlyOnceWith(question.id, 'preview');
  const second = await setup(['production']);
  const dismissed = second.answers.ask(second.question);
  host.widgets.at(-1)!.hide();
  await dismissed;
  expect(second.client.resolveUserInput).not.toHaveBeenCalled();
});
