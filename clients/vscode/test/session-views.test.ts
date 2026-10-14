import { beforeEach, describe, expect, it, vi } from 'vitest';

const closeTabs = vi.hoisted(() => vi.fn().mockResolvedValue(true));

vi.mock('vscode', () => {
  class TabInputText { constructor(readonly uri: { scheme: string; path: string; toString(): string }) {} }
  class TabInputTextDiff { constructor(readonly original: TabInputText['uri'], readonly modified: TabInputText['uri']) {} }
  return {
    TabInputText, TabInputTextDiff,
    window: { tabGroups: { all: [], close: closeTabs }, showWarningMessage: vi.fn() },
  };
});

import * as vscode from 'vscode';
import {
  closeSessionViews, registerSessionPanel, sessionArchived, sessionRestored,
} from '../src/session-views';

const ref = { coreId: 'core-a', sessionId: 's1' };
const otherCore = { coreId: 'core-b', sessionId: 's1' };
const otherSession = { coreId: 'core-a', sessionId: 's2' };
const uri = (scheme: string, path: string) => ({ scheme, path, toString: () => `${scheme}:${path}` }) as vscode.Uri;
const tab = (input: unknown) => ({ input }) as vscode.Tab;

function panel() {
  const listeners = new Set<() => void>();
  const view = {
    onDidDispose: (listener: () => void) => { listeners.add(listener); return { dispose: () => listeners.delete(listener) }; },
    dispose: vi.fn(() => { for (const listener of listeners) listener(); }),
  };
  return view;
}

const register = (owner: typeof ref, view: ReturnType<typeof panel>) =>
  registerSessionPanel(owner, view as unknown as vscode.WebviewPanel);

beforeEach(() => {
  sessionRestored(ref);
  sessionRestored(otherCore);
  sessionRestored(otherSession);
  Object.assign(vscode.window.tabGroups, { all: [] });
  vi.clearAllMocks();
});

describe('archiving a session closes its editor views', () => {
  it('closes every registered panel, including hidden views, while keeping other sessions and Cores open', async () => {
    const conversation = panel();
    const permissions = panel();
    const artifact = panel();
    const elsewhere = panel();
    const another = panel();
    for (const view of [conversation, permissions, artifact]) register(ref, view);
    register(otherCore, elsewhere);
    register(otherSession, another);

    await closeSessionViews(ref);

    for (const view of [conversation, permissions, artifact]) expect(view.dispose).toHaveBeenCalledOnce();
    expect(elsewhere.dispose).not.toHaveBeenCalled();
    expect(another.dispose).not.toHaveBeenCalled();
    elsewhere.dispose();
    another.dispose();
  });

  it('finds restored diffs, raw diffs and artifacts in all groups without closing local files or other sessions', async () => {
    const before = uri('threavia-diff', '/core-a/s1/12/before/file.ts');
    const after = uri('threavia-diff', '/core-a/s1/12/after/file.ts');
    const diff = tab(new vscode.TabInputTextDiff(before, after));
    const raw = tab(new vscode.TabInputText(uri('threavia-diff', '/core-a/s1/13/raw/file.diff')));
    const artifactUri = uri('threavia-artifact', '/core-a/sessions/s1/artifacts/artifact/report.md');
    const artifact = tab(new vscode.TabInputText(artifactUri));
    const duplicate = tab(new vscode.TabInputText(artifactUri));
    const others = [
      tab(new vscode.TabInputText(uri('file', '/workspace/file.ts'))),
      tab(new vscode.TabInputText(uri('threavia-diff', '/core-b/s1/12/raw/file.diff'))),
      tab(new vscode.TabInputText(uri('threavia-diff', '/core-a/s10/12/raw/file.diff'))),
      tab({ viewType: 'another-extension' }),
    ];
    Object.assign(vscode.window.tabGroups, { all: [{ tabs: [diff, artifact, ...others] }, { tabs: [raw, duplicate] }] });

    await closeSessionViews(ref);

    expect(closeTabs).toHaveBeenCalledExactlyOnceWith([diff, artifact, raw, duplicate], true);
  });

  it('prevents pending asynchronous opens from leaving views behind, and allows reopening after Undo', async () => {
    await closeSessionViews(ref);
    const pending = panel();
    expect(register(ref, pending)).toBe(false);
    expect(pending.dispose).toHaveBeenCalledOnce();
    expect(sessionArchived(ref)).toBe(true);

    sessionRestored(ref);
    const reopened = panel();
    expect(register(ref, reopened)).toBe(true);
    expect(reopened.dispose).not.toHaveBeenCalled();
    reopened.dispose();
  });

  it('forgets disposed panels and permits repeated archive notifications', async () => {
    const closed = panel();
    register(ref, closed);
    closed.dispose();
    await closeSessionViews(ref);
    await closeSessionViews(ref);
    expect(closed.dispose).toHaveBeenCalledOnce();
    expect(closeTabs).not.toHaveBeenCalled();
  });
});
