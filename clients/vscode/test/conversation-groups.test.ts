import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

interface Group {
  viewColumn: number;
  tabs: { panel?: Panel; label?: string }[];
  isActive: boolean;
  locked: boolean;
}

const host = vi.hoisted(() => ({
  groups: [] as Group[],
  panels: [] as Panel[],
  command: vi.fn<(name: string) => Promise<unknown>>(),
  create: vi.fn(),
  clipboard: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('vscode', () => ({
  Uri: { joinPath: (base: { path: string }, ...parts: string[]) => ({ toString: () => [base.path, ...parts].join('/') }) },
  commands: { executeCommand: host.command },
  env: { clipboard: { writeText: host.clipboard } },
  window: {
    createWebviewPanel: host.create,
    registerWebviewPanelSerializer: () => ({ dispose() {} }),
    state: { focused: true, active: true },
    onDidChangeWindowState: () => ({ dispose() {} }),
    tabGroups: {
      get all() { return host.groups; },
      get activeTabGroup() { return host.groups.find((group) => group.isActive)!; },
    },
  },
}));

import * as vscode from 'vscode';
import { EventBus } from '../src/api/events';
import type { Event } from '../src/api/types';
import type { Core } from '../src/cores/core';
import { ConversationPanels, type Services } from '../src/conversation/panel';
import type { WebviewMessage } from '../src/conversation/protocol';
import { isSessionInFront } from '../src/sessions';

class Panel {
  active = true;
  visible = true;
  title = '';
  disposed = false;
  private readonly closed = new Set<() => void>();
  private readonly changed = new Set<() => void>();
  receive: (message: WebviewMessage) => void = () => {};
  webview = {
    html: '', options: {}, cspSource: 'webview:',
    asWebviewUri: (uri: vscode.Uri) => uri,
    postMessage: vi.fn().mockResolvedValue(true),
    onDidReceiveMessage: (receive: typeof this.receive) => {
      this.receive = receive;
      return { dispose() {} };
    },
  };
  constructor(public group: Group) {
    group.tabs.push({ panel: this });
    host.panels.push(this);
  }
  get viewColumn() { return this.group.viewColumn; }
  reveal = vi.fn((column?: number) => {
    if (column !== undefined && column !== this.viewColumn) {
      this.group.tabs = this.group.tabs.filter((tab) => tab.panel !== this);
      this.group = host.groups.find((group) => group.viewColumn === column)!;
      this.group.tabs.push({ panel: this });
    }
    activate(this.group);
    this.active = true;
    for (const changed of this.changed) changed();
  });
  // The editor tells a panel when it loses focus too, as another one takes it.
  deactivate() {
    if (!this.active) return;
    this.active = false;
    for (const changed of this.changed) changed();
  }
  onDidDispose(listener: () => void) { this.closed.add(listener); return { dispose: () => this.closed.delete(listener) }; }
  onDidChangeViewState(listener: () => void) { this.changed.add(listener); return { dispose: () => this.changed.delete(listener) }; }
  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.group.tabs = this.group.tabs.filter((tab) => tab.panel !== this);
    for (const closed of this.closed) closed();
  }
}

function activate(target: Group) {
  for (const group of host.groups) group.isActive = group === target;
  for (const panel of host.panels) panel.deactivate();
}

function group(tabs: Group['tabs'] = []): Group {
  const next = { viewColumn: host.groups.length + 1, tabs, isActive: false, locked: false };
  host.groups.push(next);
  activate(next);
  return next;
}

describe('conversation editor groups', () => {
  let conversations: ConversationPanels;
  let services: Services;
  let openPath: ReturnType<typeof vi.fn>;
  let openArtifact: ReturnType<typeof vi.fn>;
  let history: Event[];
  let bus: EventBus;
  let markSessionRead: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    host.groups = [];
    host.panels = [];
    vi.clearAllMocks();
    group();
    host.command.mockImplementation((name) => {
      if (name === 'workbench.action.newGroupRight') group();
      if (name === 'workbench.action.lockEditorGroup') host.groups.find((entry) => entry.isActive)!.locked = true;
      return Promise.resolve(undefined);
    });
    host.create.mockImplementation((_type: string, _title: string, column: number) => {
      let target = host.groups.find((entry) => entry.viewColumn === column)!;
      // VS Code resolves a sole empty group through its default routing, which
      // skips a locked group. Locking before creation would misplace the panel.
      if (host.groups.length === 1 && !target.tabs.length && target.locked) target = group();
      return new Panel(target);
    });
    openPath = vi.fn().mockResolvedValue(true);
    openArtifact = vi.fn().mockResolvedValue(undefined);
    history = [];
    bus = new EventBus();
    markSessionRead = vi.fn().mockResolvedValue(undefined);
    const core = {
      id: 'group-core', bus,
      client: {
        snapshot: vi.fn().mockResolvedValue({ session: { id: 's1', projectId: 'p1', status: 'ACTIVE', readSequence: 0 }, runs: [], jobs: [], events: history, attention: {}, cursor: 0 }),
        markSessionRead,
      },
      attention: { ready: true, current: { validations: [], userInputs: [] }, onChange: () => ({ dispose() {} }) },
      repositories: { watch: () => ({ dispose() {} }) }, artifacts: { open: openArtifact },
    } as unknown as Core;
    services = {
      extensionUri: { path: '/extension' },
      cores: { ids: [core.id], started: Promise.resolve(), get: () => core, onDidRemove: () => ({ dispose() {} }) },
      paths: { open: openPath }, documents: {},
    } as unknown as Services;
    conversations = new ConversationPanels(services);
  });
  afterEach(() => conversations.dispose());

  it('gives simultaneous session opens separate locked groups without adding tabs beside existing files', async () => {
    host.groups[0].tabs.push({ label: 'file.ts' });
    await Promise.all([
      conversations.open({ coreId: 'group-core', sessionId: 's1' }),
      conversations.open({ coreId: 'group-core', sessionId: 's2' }),
    ]);
    expect(host.groups.map((entry) => [entry.tabs.length, entry.locked])).toEqual([[1, false], [1, true], [1, true]]);
    expect(host.panels[0].group).not.toBe(host.panels[1].group);
  });

  it('uses an empty group and reveals the same session again without duplicating its panel or group', async () => {
    const ref = { coreId: 'group-core', sessionId: 's1' };
    await Promise.all([conversations.open(ref), conversations.open(ref)]);
    expect(host.groups).toHaveLength(1);
    expect(host.groups[0].locked).toBe(true);
    expect(host.panels).toHaveLength(1);
    expect(host.panels[0].reveal).toHaveBeenCalledOnce();
  });

  it('opens a draft in its own locked group and reuses it until its first message', async () => {
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    const start = { coreId: 'group-core', projectId: 'p1', projectName: 'Project', backendInstanceId: 'b1', backendName: 'Agent', workingDirectoryId: null };
    await conversations.openDraft(start);
    await conversations.openDraft(start);
    expect(host.groups).toHaveLength(2);
    expect(host.groups.every((entry) => entry.locked && entry.tabs.length === 1)).toBe(true);
    expect(host.panels[1].reveal).toHaveBeenCalledOnce();
  });

  it('moves a restored conversation out of a shared group and locks its new group', async () => {
    const previous = host.groups[0];
    previous.tabs.push({ label: 'file.ts' });
    const restored = new Panel(previous);
    await conversations.deserializeWebviewPanel(restored as unknown as vscode.WebviewPanel, { coreId: 'group-core', sessionId: 's1' });
    await vi.waitFor(() => expect(restored.group).not.toBe(previous));
    expect(previous.tabs).toEqual([{ label: 'file.ts' }]);
    expect(restored.group.locked).toBe(true);
    expect(restored.group.tabs).toHaveLength(1);
  });

  it('opens files and artifact previews outside every session group', async () => {
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    await conversations.open({ coreId: 'group-core', sessionId: 's2' });
    host.panels[1].receive({ type: 'openPath', path: 'src/file.ts' });
    await vi.waitFor(() => expect(openPath).toHaveBeenCalledWith('src/file.ts', expect.any(Function)));
    const fileColumn = openPath.mock.calls[0][1] as () => Promise<vscode.ViewColumn>;
    expect(await fileColumn()).toBe(3);
    host.panels[1].receive({ type: 'openArtifact', artifactId: 'a1', filename: 'report.html', mimeType: 'text/html' });
    await vi.waitFor(() => expect(openArtifact).toHaveBeenCalledWith(expect.objectContaining({ artifactId: 'a1' }), expect.any(Function), 's2'));
    const artifactColumn = openArtifact.mock.calls[0][1] as () => Promise<vscode.ViewColumn>;
    expect(await artifactColumn()).toBe(3);
    expect(host.groups).toHaveLength(3);
    expect(host.groups[2].locked).toBe(false);
  });

  it('locks a conversation again when revealed without toggling it off', async () => {
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    const panel = host.panels[0];
    panel.group.locked = false;
    panel.reveal();
    await vi.waitFor(() => expect(panel.group.locked).toBe(true));
    expect(host.command).not.toHaveBeenCalledWith('workbench.action.toggleEditorGroupLock');
  });

  it('reads a session only while the person is in its conversation, not beside it nor in the terminal below it', async () => {
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    await conversations.open({ coreId: 'group-core', sessionId: 's2' });
    const [beside, inFront] = host.panels;
    await vi.waitFor(() => expect(host.panels.every((panel) => panel.title)).toBe(true));
    const s1 = { coreId: 'group-core', sessionId: 's1' };
    const reply = (sequence: number) => bus.emit('event', { id: `reply-${sequence}`, sequence, sessionId: 's1', type: 'agent.message', timestamp: '2026-10-14T12:00:00Z', payload: { text: 'Done' } });
    inFront.receive({ type: 'focus', focused: true });
    expect([beside.visible, beside.active, inFront.active]).toEqual([true, false, true]);
    reply(2);
    // Showing is not reading: on every client, the reply still waits for them.
    expect(markSessionRead).not.toHaveBeenCalled();
    expect(isSessionInFront(s1)).toBe(false);
    beside.reveal();
    beside.receive({ type: 'focus', focused: true });
    expect(markSessionRead).toHaveBeenCalledExactlyOnceWith('s1', 2);
    expect(isSessionInFront(s1)).toBe(true);
    // Off to the terminal: the conversation is still the active editor, but
    // nobody is in it any more.
    beside.receive({ type: 'focus', focused: false });
    reply(3);
    expect(beside.active).toBe(true);
    expect(markSessionRead).toHaveBeenCalledOnce();
    expect(isSessionInFront(s1)).toBe(false);
    beside.receive({ type: 'focus', focused: true });
    expect(markSessionRead).toHaveBeenLastCalledWith('s1', 3);
  });

  it('copies the agent’s complete original Markdown, keeping whitespace, links, tables and fenced code', async () => {
    const markdown = '  ## Résultat\r\n\r\n- **Fait** : [rapport](https://example.com/a?x=1&y=2)\r\n\r\n| A | B |\r\n| - | - |\r\n| 1 | 2 |\r\n\r\n```ts\r\nconst html = "<strong>texte</strong>";\r\n```\r\n\r\n';
    history.push({ id: 'response', sequence: 1, type: 'agent.message', timestamp: '2026-10-14T12:00:00Z', payload: { text: markdown } });
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    host.panels[0].receive({ type: 'copyResponse', sequence: 1 });
    await vi.waitFor(() => expect(host.panels[0].webview.postMessage).toHaveBeenCalledWith({ type: 'copiedResponse', sequence: 1, ok: true }));
    expect(host.clipboard).toHaveBeenCalledExactlyOnceWith(markdown);
  });

  it('leaves the clipboard untouched when a copy names a user message or missing history', async () => {
    history.push({ id: 'user', sequence: 1, type: 'user.message', timestamp: '2026-10-14T12:00:00Z', payload: { text: 'My prompt' } });
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    for (const sequence of [1, 99]) {
      host.panels[0].receive({ type: 'copyResponse', sequence });
      expect(host.panels[0].webview.postMessage).toHaveBeenCalledWith(expect.objectContaining({ type: 'copiedResponse', sequence, ok: false }));
    }
    expect(host.clipboard).not.toHaveBeenCalled();
  });

  it('reports a clipboard failure and allows the same response to be copied again', async () => {
    history.push({ id: 'response', sequence: 1, type: 'agent.message', timestamp: '2026-10-14T12:00:00Z', payload: { text: '**Answer**' } });
    host.clipboard.mockRejectedValueOnce(new Error('Clipboard unavailable'));
    await conversations.open({ coreId: 'group-core', sessionId: 's1' });
    const panel = host.panels[0];
    panel.receive({ type: 'copyResponse', sequence: 1 });
    await vi.waitFor(() => expect(panel.webview.postMessage).toHaveBeenCalledWith({ type: 'copiedResponse', sequence: 1, ok: false, error: 'Response not copied: Clipboard unavailable' }));
    panel.receive({ type: 'copyResponse', sequence: 1 });
    await vi.waitFor(() => expect(panel.webview.postMessage).toHaveBeenCalledWith({ type: 'copiedResponse', sequence: 1, ok: true }));
    expect(host.clipboard).toHaveBeenCalledTimes(2);
  });
});
