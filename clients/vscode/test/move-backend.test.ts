import { beforeEach, expect, it, vi } from 'vitest';

const window = vi.hoisted(() => ({ showQuickPick: vi.fn(), showInformationMessage: vi.fn(), showWarningMessage: vi.fn() }));
vi.mock('vscode', () => ({
  ThemeIcon: class { constructor(public id: string) {} },
  Uri: { file: (path: string) => ({ path }) },
  window,
}));

import type { CoreClient } from '../src/api/client';
import type { BackendInstance, HandoffPreview, Snapshot } from '../src/api/types';
import { moveSessionToBackend } from '../src/handoff';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

/** A client answered by the demo Core, the one the Web demo runs on. */
const client = {
  snapshot: (id: string) => demoRequest('GET', `/api/v1/sessions/${id}`, null),
  backends: async () => ((await demoRequest('GET', '/api/v1/backends', null)) as { items: BackendInstance[] }).items,
  handoffPreview: (id: string, backend: string) => demoRequest('GET', `/api/v1/sessions/${id}/handoff?backendInstanceId=${backend}`, null),
  moveSessionToBackend: (id: string, backend: string, run: string) => demoRequest('PATCH', `/api/v1/sessions/${id}`, { backendInstanceId: backend, expectedRunId: run }),
} as unknown as CoreClient;

/** The quick pick answered with the NAS. */
const pickNas = (items: { target: { id: string } }[]) => Promise.resolve(items.find((item) => item.target.id === IDS.nas));

async function idleSession(model: string) {
  const created = await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'Prepare the move', model }) as { session: { id: string } };
  return created.session.id;
}

beforeEach(() => {
  for (const mock of Object.values(window)) mock.mockReset();
});

it('offers the other backends, confirms with what the move changes, and moves', async () => {
  const sessionId = await idleSession('opus');
  window.showQuickPick.mockImplementation(pickNas);
  window.showInformationMessage.mockResolvedValue('Move');

  expect(await moveSessionToBackend(client, sessionId)).toBe(true);

  const offered = window.showQuickPick.mock.calls[0][0] as { target: { id: string } }[];
  expect(offered.map((item) => item.target.id)).not.toContain(IDS.laptop);
  const [question, options] = window.showInformationMessage.mock.calls[0] as [string, { modal: boolean; detail: string }];
  expect(question).toMatch(/^Move to /);
  expect(options.modal).toBe(true);
  // Opus is the laptop's: on the NAS the session takes its default model.
  expect(options.detail).toContain('"opus"');
  const moved = await demoRequest('GET', `/api/v1/sessions/${sessionId}`, null) as Snapshot;
  expect(moved.runs.at(-1)?.backendInstanceId).toBe(IDS.nas);
  expect(moved.session.model).toBe('');
});

it('leaves the session where it is when the move is not confirmed', async () => {
  const sessionId = await idleSession('');
  window.showQuickPick.mockImplementation(pickNas);
  window.showInformationMessage.mockResolvedValue(undefined);

  expect(await moveSessionToBackend(client, sessionId)).toBe(false);
  const snapshot = await demoRequest('GET', `/api/v1/sessions/${sessionId}`, null) as Snapshot;
  expect(snapshot.runs).toHaveLength(1);
});

it('says why a destination is refused instead of asking to confirm', async () => {
  const sessionId = await idleSession('');
  const preview = await demoRequest('GET', `/api/v1/sessions/${sessionId}/handoff?backendInstanceId=${IDS.nas}`, null) as HandoffPreview;
  const refusing = { ...client, handoffPreview: () => Promise.resolve({ ...preview, canMove: false, reasons: ['The backend is full.'] }) } as unknown as CoreClient;
  window.showQuickPick.mockImplementation(pickNas);

  expect(await moveSessionToBackend(refusing, sessionId)).toBe(false);
  expect(window.showInformationMessage).not.toHaveBeenCalled();
  expect(window.showWarningMessage.mock.calls[0][0]).toContain('The backend is full.');
});
