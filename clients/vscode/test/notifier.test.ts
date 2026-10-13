import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import type { CoreClient } from '../src/api/client';
import type { UserInputRequest } from '../src/api/types';
import type { Core } from '../src/cores/core';
import type { Cores } from '../src/cores/registry';

interface Notice { title: string; messages: string[]; closed: boolean; cancel: () => void }
const mocks = vi.hoisted(() => ({ notices: [] as Notice[], commands: new Map<string, (args: unknown) => Promise<void>>() }));
vi.mock('vscode', () => ({
  ProgressLocation: { Notification: 15 },
  commands: { registerCommand: (name: string, callback: (args: unknown) => Promise<void>) => { mocks.commands.set(name, callback); return { dispose: () => { mocks.commands.delete(name); } }; } },
  window: {
    withProgress: (options: { title: string }, task: (progress: { report: (value: { message: string }) => void }, token: { onCancellationRequested: (callback: () => void) => { dispose: () => void } }) => Promise<void>) => {
      const notice: Notice = { title: options.title, messages: [], closed: false, cancel: () => {} };
      mocks.notices.push(notice);
      return task({ report: ({ message }) => { notice.messages.push(message); } }, { onCancellationRequested: (callback) => { notice.cancel = callback; return { dispose() {} }; } }).then(() => { notice.closed = true; });
    },
    setStatusBarMessage: vi.fn(), showInformationMessage: vi.fn(), showErrorMessage: vi.fn(),
  },
}));
vi.mock('../src/config', () => ({ notifyAttention: () => true, notifyJobEnded: () => false }));
vi.mock('../src/sessions', () => ({ isSessionOpen: () => false, openSession: vi.fn() }));

import { EventBus } from '../src/api/events';
import { Answers } from '../src/attention/answer';
import { AttentionLedgers } from '../src/attention/ledger';
import { Notifier } from '../src/attention/notifier';
import { AttentionStore } from '../src/attention/store';

const prompt: UserInputRequest = { id: 'question', scope: { sessionId: 'session', jobId: 'job', projectId: 'project', runId: 'run' }, status: 'PENDING', prompt: 'Environment?', choices: ['staging', 'production'], freeText: true, notify: true, createdAt: '2026-10-13T11:59:00Z' };
const cleanup: { dispose: () => void }[] = [];
beforeEach(() => { mocks.notices.length = 0; mocks.commands.clear(); vi.clearAllMocks(); });
afterEach(() => { for (const item of cleanup.splice(0).reverse()) item.dispose(); });

async function setup(coreIds = ['core'], request = prompt) {
  const listeners = new Set<(core: Core) => void>();
  const runtimes = new Map<string, Core>();
  const clients = new Map<string, { resolveUserInput: ReturnType<typeof vi.fn>; resolveValidation: ReturnType<typeof vi.fn> }>();
  const registry = {
    get: (id: string) => runtimes.get(id),
    ledgers: new AttentionLedgers(), saveLedgers: () => Promise.resolve(),
    onAttention: (listener: (core: Core) => void) => { listeners.add(listener); return { dispose: () => { listeners.delete(listener); } }; },
    onDidRemove: () => ({ dispose() {} }), on: () => ({ dispose() {} }),
  } as unknown as Cores;
  for (const id of coreIds) {
    const client = { attention: () => Promise.resolve({ validations: [], userInputs: [request] }), resolveUserInput: vi.fn().mockResolvedValue(request), resolveValidation: vi.fn() };
    const bus = new EventBus();
    const attention = new AttentionStore(client as unknown as CoreClient, bus);
    cleanup.push(attention);
    const core = { id, bus, attention, answers: new Answers(client as unknown as CoreClient, attention), label: (text: string) => `${id} · ${text}` } as unknown as Core;
    attention.onChange(() => { for (const listener of listeners) listener(core); });
    runtimes.set(id, core);
    clients.set(id, client);
  }
  const notifier = new Notifier(registry, () => Promise.resolve('Session'));
  cleanup.push(notifier);
  for (const core of runtimes.values()) await core.attention.refresh();
  return { core: runtimes.get(coreIds[0])!, clients, runtimes };
}

it('closes the matching notification when a question is answered in the conversation', async () => {
  const { core } = await setup(['first', 'second']);
  expect(mocks.notices).toHaveLength(2);
  await core.answers.reply(prompt, 'staging');
  await vi.waitFor(() => expect(mocks.notices[0].closed).toBe(true));
  expect(mocks.notices[1].closed).toBe(false);
});

it('closes obsolete notifications on interrupt and on answers from another device', async () => {
  const { core } = await setup();
  core.bus.publish({ id: 'e', sequence: 1, timestamp: '2026-10-13T12:00:00Z', type: 'user.message', sessionId: 'session', jobId: 'job', payload: { text: 'Change course', delivery: 'NOW' } });
  await vi.waitFor(() => expect(mocks.notices[0].closed).toBe(true));
  expect(core.attention.current.userInputs).toEqual([]);
});

it('keeps a failed answer visible and closes on remote resolution', async () => {
  const { core, clients } = await setup();
  clients.get('core')!.resolveUserInput.mockRejectedValueOnce(new Error('Offline'));
  expect(await core.answers.reply(prompt, 'staging')).toBe(false);
  expect(mocks.notices[0].closed).toBe(false);
  core.bus.publish({ id: 'e', sequence: 1, timestamp: '2026-10-13T12:00:00Z', type: 'user_input.resolved', sessionId: 'session', jobId: 'job', payload: { requestId: prompt.id, value: 'production' } });
  await vi.waitFor(() => expect(mocks.notices[0].closed).toBe(true));
});

it('answers a short choice through its notification link and ignores stale links', async () => {
  const { clients } = await setup();
  const message = mocks.notices[0].messages[0];
  const match = message.match(/\[staging\]\(command:threavia.notificationAction\?([^)]*)\)/)!;
  const args = JSON.parse(decodeURIComponent(match[1])) as unknown[];
  const action = mocks.commands.get('threavia.notificationAction')!;
  await action(args[0]);
  await vi.waitFor(() => expect(mocks.notices[0].closed).toBe(true));
  await action(args[0]);
  expect(clients.get('core')!.resolveUserInput).toHaveBeenCalledTimes(1);
  expect(clients.get('core')!.resolveUserInput).toHaveBeenCalledWith(prompt.id, 'staging');
});

it('dismisses a notification without answering or cancelling the job', async () => {
  const { core, clients } = await setup();
  mocks.notices[0].cancel();
  await vi.waitFor(() => expect(mocks.notices[0].closed).toBe(true));
  expect(core.attention.findUserInput(prompt.id)).toBeDefined();
  expect(clients.get('core')!.resolveUserInput).not.toHaveBeenCalled();
});

it('encodes choices containing parentheses and escapes prompt text in notifications', async () => {
  const request = { ...prompt, prompt: '[Untrusted](command:other.action)', choices: ['staging (test)'] };
  const { clients } = await setup(['core'], request);
  expect(mocks.notices[0].title).toContain('\\[Untrusted\\]\\(command:other.action\\)');
  const encoded = mocks.notices[0].messages[0].match(/command:threavia.notificationAction\?([^)]*)/)!;
  const args = JSON.parse(decodeURIComponent(encoded[1])) as unknown[];
  await mocks.commands.get('threavia.notificationAction')!(args[0]);
  expect(clients.get('core')!.resolveUserInput).toHaveBeenCalledWith(request.id, 'staging (test)');
});
