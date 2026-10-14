import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const host = vi.hoisted(() => ({ pick: vi.fn(), input: vi.fn(), status: vi.fn() }));
vi.mock('vscode', () => ({
  EventEmitter: class { event = () => ({ dispose() {} }); fire() {} dispose() {} },
  workspace: { registerTextDocumentContentProvider: () => ({ dispose() {} }) },
  window: {
    createTreeView: () => ({ description: undefined, dispose() {} }),
    showQuickPick: host.pick, showInputBox: host.input, setStatusBarMessage: host.status,
  },
}));

import type * as vscode from 'vscode';
import type { Decision, Project } from '../src/api/types';
import type { Effect } from '../src/api/events';
import type { Core } from '../src/cores/core';
import type { Cores } from '../src/cores/registry';
import type { CurrentProject } from '../src/knowledge/current';
import { MemoryView } from '../src/knowledge/memory';

const decision = (id: string, projectId?: string): Decision => ({ id, projectId, title: id, importance: 'NORMAL', status: 'ACTIVE', createdAt: '2026-10-14T12:00:00Z', updatedAt: '2026-10-14T12:00:00Z' });

describe('Memory view scope', () => {
  let view: MemoryView;
  let coreA: Core;
  let coreB: Core;
  let clientA: { decisions: ReturnType<typeof vi.fn>; createDecision: ReturnType<typeof vi.fn> };
  let clientB: typeof clientA;
  let commands: Map<string, (...args: never[]) => unknown>;
  let subscriptions: vscode.Disposable[];
  let current: { core?: Core; project?: Project; settled: boolean; onChange: () => { dispose(): void } };
  let effect: (core: Core, value: Effect) => void;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    clientA = { decisions: vi.fn().mockResolvedValue([decision('local', 'p1')]), createDecision: vi.fn().mockResolvedValue({}) };
    clientB = { decisions: vi.fn().mockResolvedValue([decision('global-b')]), createDecision: vi.fn().mockResolvedValue({}) };
    coreA = { id: 'a', name: 'Core A', ready: true, client: clientA } as unknown as Core;
    coreB = { id: 'b', name: 'Core B', ready: true, client: clientB } as unknown as Core;
    current = { core: coreA, project: { id: 'p1', name: 'Project' } as Project, settled: true, onChange: () => ({ dispose() {} }) };
    const cores = {
      several: true, ready: () => [coreA, coreB], get: (id: string) => id === 'a' ? coreA : id === 'b' ? coreB : undefined,
      on: (topic: string, listener: unknown) => { if (topic === 'effect') effect = listener as typeof effect; return { dispose() {} }; },
      onDidChange: () => ({ dispose() {} }),
    } as unknown as Cores;
    view = new MemoryView(current as unknown as CurrentProject, cores);
    commands = new Map(); subscriptions = [];
    view.register((name, run) => { commands.set(name, run); }, subscriptions);
  });
  afterEach(() => { view.dispose(); for (const disposable of subscriptions) disposable.dispose(); vi.useRealTimers(); });

  it('keeps the current project as the default and switches to the explicitly chosen Core’s globals', async () => {
    await view.getChildren();
    expect(clientA.decisions).toHaveBeenCalledWith('p1', true);
    host.pick.mockResolvedValueOnce({ coreId: 'b' });
    await commands.get('threavia.switchMemoryScope')!();
    const groups = await view.getChildren();
    const rows = await view.getChildren(groups[0]);
    expect(clientB.decisions).toHaveBeenCalledExactlyOnceWith('', true);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ type: 'decision', coreId: 'b', decision: { id: 'global-b' } });
    expect(view.view.description).toBe('Global memory · Core B');

    effect(coreB, { kind: 'decisions', projectId: 'p2' });
    await vi.advanceTimersByTimeAsync(200);
    expect(clientB.decisions).toHaveBeenCalledTimes(1);
    effect(coreB, { kind: 'decisions' });
    await vi.advanceTimersByTimeAsync(200);
    expect(clientB.decisions).toHaveBeenCalledTimes(2);
  });

  it('records global memory without requiring any project and uses the selected Core', async () => {
    current.core = undefined; current.project = undefined;
    host.pick.mockResolvedValueOnce({ coreId: 'b' }).mockResolvedValueOnce({ value: 'IMPORTANT' });
    host.input.mockResolvedValueOnce('Shared preference').mockResolvedValueOnce('All my projects');
    await commands.get('threavia.recordDecision')!();
    expect(clientB.createDecision).toHaveBeenCalledExactlyOnceWith('', { title: 'Shared preference', content: 'All my projects', importance: 'IMPORTANT' });
    expect(clientA.createDecision).not.toHaveBeenCalled();
  });

  it('does not let a late project read replace global memory after switching scope', async () => {
    let finish!: (decisions: Decision[]) => void;
    clientA.decisions.mockReturnValueOnce(new Promise<Decision[]>((resolve) => { finish = resolve; }));
    const pending = view.getChildren();
    host.pick.mockResolvedValueOnce({ coreId: 'b' });
    await commands.get('threavia.switchMemoryScope')!();
    await view.getChildren();
    finish([decision('late-local', 'p1')]);
    await pending;
    const groups = await view.getChildren();
    const rows = await view.getChildren(groups[0]);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ coreId: 'b', decision: { id: 'global-b' } });
  });
});
