import { describe, expect, it, vi } from 'vitest';

vi.mock('vscode', () => ({ window: { showQuickPick: vi.fn((items: unknown[]) => Promise.resolve(items[0])) }, Uri: { file: (path: string) => path }, ThemeIcon: class { constructor(public id: string) {} } }));
import * as vscode from 'vscode';

import { CoreClient } from '../src/api/client';
import type { BackendInstance } from '../src/api/types';
import { pickBackend } from '../src/start/pickers';

describe('default backend', () => {
  it('sets and clears the preference on the selected Core', async () => {
    const calls: { url: string; body: unknown }[] = [];
    const client = new CoreClient({ baseUrl: () => 'https://second.example', authorization: () => Promise.resolve(undefined), identity: { clientId: 'editor', clientName: () => 'VS Code' }, fetch: (input, init) => { calls.push({ url: typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, body: init?.body }); return Promise.resolve(new Response(null, { status: 204 })); } });
    await client.setDefaultBackend('b2'); await client.setDefaultBackend(null);
    expect(calls).toEqual([
      { url: 'https://second.example/api/v1/me/default-backend', body: JSON.stringify({ backendInstanceId: 'b2' }) },
      { url: 'https://second.example/api/v1/me/default-backend', body: JSON.stringify({ backendInstanceId: null }) },
    ]);
  });

  it('proposes an offline default first, labels it and keeps revoked backends out', async () => {
    const backend = (patch: Partial<BackendInstance>): BackendInstance => ({ id: 'b1', name: 'Backend', operationalStatus: 'READY', ownershipStatus: 'CLAIMED', providerAuthState: 'AUTHENTICATED', capacity: { activeRuns: 0, maxConcurrentRuns: 1 }, capabilities: ['CODE'], protocolVersion: 1, createdAt: '2026-10-14T00:00:00Z', updatedAt: '2026-10-14T00:00:00Z', ...patch });
    const backends = [
      backend({ id: 'ready', name: 'Ready' }),
      backend({ id: 'preferred', name: 'Preferred', isDefault: true, operationalStatus: 'OFFLINE' }),
      backend({ id: 'revoked', name: 'Revoked', isDefault: true, ownershipStatus: 'REVOKED' }),
    ];
    const client = { backends: () => Promise.resolve(backends) } as CoreClient;
    expect((await pickBackend(client, 'New session'))?.id).toBe('preferred');
    const items = vi.mocked(vscode.window.showQuickPick).mock.calls.at(-1)![0] as vscode.QuickPickItem[];
    expect(items).toHaveLength(2);
    expect(items[0].description).toContain('default');
  });
});
