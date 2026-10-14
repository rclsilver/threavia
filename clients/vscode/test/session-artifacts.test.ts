import { beforeEach, describe, expect, it, vi } from 'vitest';

const host = vi.hoisted(() => ({
  close: vi.fn().mockResolvedValue(true),
  showTextDocument: vi.fn().mockResolvedValue(undefined),
  createWebviewPanel: vi.fn(),
}));

vi.mock('vscode', () => {
  class TabInputText { constructor(readonly uri: unknown) {} }
  class TabInputTextDiff {}
  return {
    TabInputText, TabInputTextDiff, ProgressLocation: { Window: 10 },
    window: {
      ...host, tabGroups: { all: [], close: host.close },
      withProgress: (_options: unknown, task: () => unknown) => task(),
    },
  };
});

import * as vscode from 'vscode';
import type { CoreClient } from '../src/api/client';
import { Artifacts } from '../src/conversation/artifacts';
import type { VirtualDocuments } from '../src/diff/documents';
import { closeSessionViews, sessionRestored } from '../src/session-views';

const ref = { coreId: 'artifact-core', sessionId: 'artifact-session' };
const artifact = { artifactId: 'a1', filename: 'report.md', mimeType: 'text/markdown' };
const content = new TextEncoder().encode('Report');

function setup(read: () => Promise<Uint8Array>) {
  const put = vi.fn((path: string) => ({ scheme: 'threavia-artifact', path, toString: () => `threavia-artifact:${path}` }));
  const artifacts = new Artifacts(
    { artifactContent: read } as unknown as CoreClient,
    { put } as unknown as VirtualDocuments,
    ref.coreId,
  );
  return { artifacts, put };
}

beforeEach(() => {
  sessionRestored(ref);
  Object.assign(vscode.window.tabGroups, { all: [] });
  vi.clearAllMocks();
});

describe('artifact previews while a session is being archived', () => {
  it('chooses its group after the download, allowing another session to move the groups meanwhile', async () => {
    let finish!: (bytes: Uint8Array) => void;
    let destination = 2;
    const column = vi.fn(() => Promise.resolve(destination));
    const { artifacts } = setup(() => new Promise((resolve) => { finish = resolve; }));
    const opening = artifacts.open(artifact, column, ref.sessionId);
    expect(column).not.toHaveBeenCalled();
    destination = 3;
    finish(content);
    await opening;
    expect(host.showTextDocument).toHaveBeenCalledWith(expect.anything(), { viewColumn: 3, preview: true });
  });

  it('does not open a preview whose download finishes after the archive', async () => {
    let finish!: (bytes: Uint8Array) => void;
    const { artifacts, put } = setup(() => new Promise((resolve) => { finish = resolve; }));
    const opening = artifacts.open(artifact, 1, ref.sessionId);
    await closeSessionViews(ref);
    finish(content);
    await opening;
    expect(put).not.toHaveBeenCalled();
    expect(host.showTextDocument).not.toHaveBeenCalled();
    expect(host.createWebviewPanel).not.toHaveBeenCalled();
  });

  it('closes a native tab that finishes opening after the archive', async () => {
    let finish!: () => void;
    const nativeOpen = new Promise<void>((resolve) => { finish = resolve; });
    host.showTextDocument.mockReturnValueOnce(nativeOpen);
    const { artifacts, put } = setup(() => Promise.resolve(content));
    const opening = artifacts.open(artifact, 1, ref.sessionId);
    await vi.waitFor(() => expect(host.showTextDocument).toHaveBeenCalledOnce());
    await closeSessionViews(ref);
    const uri = put.mock.results[0].value as vscode.Uri;
    const tab = { input: new vscode.TabInputText(uri) };
    Object.assign(vscode.window.tabGroups, { all: [{ tabs: [tab] }] });
    finish();
    await opening;
    expect(host.close).toHaveBeenCalledExactlyOnceWith([tab], true);
  });
});
