import * as vscode from 'vscode';

import { ApiError, CoreClient } from './api/client';
import { EventBus } from './api/events';
import { Answers } from './attention/answer';
import { Notifier, WaitingStatus } from './attention/notifier';
import { AttentionStore } from './attention/store';
import { PROVIDER_ID, ThreaviaAuth } from './auth/provider';
import { coreUrl, promptCoreUrl } from './config';
import { Connection } from './connection';
import { createIdentity } from './identity';
import { Presence } from './presence';
import { askAboutThis } from './ask/ask';
import type { BackendInstance } from './api/types';
import { Artifacts } from './conversation/artifacts';
import { ConversationPanels } from './conversation/panel';
import { VirtualDocuments } from './diff/documents';
import { PathResolver } from './workspace/resolve';
import { openInBrowser, runOpenSession, sessionIdOf, setSessionOpener } from './sessions';
import { UNTITLED, titleOf } from './tree/model';
import { SidebarProvider, type RequestNode, type SessionNode } from './tree/provider';

/**
 * What the extension exposes to the code that builds on it: the conversation
 * view and the editor integrations read Core and the stream through these
 * rather than opening a second connection.
 */
export interface Threavia {
  client: CoreClient;
  bus: EventBus;
  attention: AttentionStore;
}

export function activate(context: vscode.ExtensionContext): Threavia {
  const identity = createIdentity(context);
  const bus = new EventBus();

  // The client and the auth layer need each other: requests carry the
  // credential, and a basic sign-in is checked with a request. The auth layer
  // reaches the client lazily, only once a sign-in runs.
  const auth: ThreaviaAuth = new ThreaviaAuth(context.secrets, () => client, coreUrl);
  const client = new CoreClient({
    baseUrl: coreUrl,
    identity,
    authorization: () => auth.authorization(),
    onUnauthorized: () => auth.recover(),
  });

  const attention = new AttentionStore(client, bus);
  const answers = new Answers(client, attention);
  const sidebar = new SidebarProvider(client, attention, bus);
  const tree = vscode.window.createTreeView('threavia.sidebar', {
    treeDataProvider: sidebar,
    showCollapseAll: true,
  });
  const presence = new Presence(client);

  // The conversation: one webview per Session, with what it opens — diffs,
  // artifacts, files — in the editor's own documents.
  const documents = new VirtualDocuments('threavia-diff');
  const artifactDocuments = new VirtualDocuments('threavia-artifact');
  const paths = new PathResolver();
  let backends: { at: number; list: Promise<BackendInstance[]> } | undefined;
  const panels = new ConversationPanels({
    extensionUri: context.extensionUri,
    client,
    bus,
    attention,
    answers,
    documents,
    paths,
    artifacts: new Artifacts(client, artifactDocuments),
    // Shared by every panel and kept a minute: a backend's name and features
    // change rarely, and ten panels restored at start ask once.
    backends: () => {
      if (!backends || Date.now() - backends.at > 60_000) {
        const list = client.backends();
        list.catch(() => (backends = undefined));
        backends = { at: Date.now(), list };
      }
      return backends.list;
    },
  });
  setSessionOpener((sessionId) => {
    panels.open(sessionId);
    return Promise.resolve();
  });
  const connection = new Connection(context.globalState, client, auth, bus, attention, sidebar, presence);

  /** A Session's title from what is already known, asking Core only as a last resort. */
  const sessionTitle = async (sessionId: string): Promise<string> => {
    const known = sidebar.findSession(sessionId);
    if (known) return titleOf(known);
    const asking = [...attention.current.validations, ...attention.current.userInputs].find(
      (request) => request.scope.sessionId === sessionId,
    );
    if (asking?.context?.sessionTitle) return asking.context.sessionTitle;
    try {
      return titleOf((await client.snapshot(sessionId, 1)).session);
    } catch {
      return UNTITLED;
    }
  };

  context.subscriptions.push(
    auth,
    panels,
    paths,
    documents,
    artifactDocuments,
    vscode.authentication.registerAuthenticationProvider(PROVIDER_ID, 'Threavia', auth, {
      supportsMultipleAccounts: false,
    }),
    vscode.window.registerUriHandler(auth),
    sidebar,
    tree,
    tree.onDidChangeSelection((event) => sidebar.select(event.selection[0])),
    presence,
    connection,
    { dispose: () => attention.dispose() },
    new WaitingStatus(attention),
    new Notifier(context.globalState, attention, answers, bus, sessionTitle),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration('threavia.coreUrl')) void connection.start();
    }),
  );

  const command = (name: string, run: (...args: never[]) => unknown) =>
    context.subscriptions.push(vscode.commands.registerCommand(name, run));

  /** Asks for the Core URL on first use, rather than at every start. */
  const ensureCore = async (): Promise<boolean> => {
    if (coreUrl()) return true;
    // Saving the setting restarts the connection through the listener above.
    return (await promptCoreUrl()) !== undefined;
  };

  command('threavia.setCoreUrl', async () => {
    const before = coreUrl();
    const url = await promptCoreUrl();
    // The same URL saved again changes no setting, so nothing restarts by itself.
    if (url !== undefined && url === before) await connection.start();
  });

  command('threavia.signIn', async () => {
    if (!(await ensureCore())) return;
    if (connection.status === 'unconfigured' || connection.status === 'unreachable') await connection.start();
    if (connection.status === 'unreachable') {
      void vscode.window.showErrorMessage(`Core does not answer at ${coreUrl()}.`, 'Set Core URL').then(
        (choice) => choice && vscode.commands.executeCommand('threavia.setCoreUrl'),
      );
      return;
    }
    await auth.signIn();
  });

  command('threavia.signOut', async () => {
    await auth.signOut();
    void vscode.window.showInformationMessage('Signed out of Threavia.');
  });

  command('threavia.refresh', async () => {
    if (!(await ensureCore())) return;
    if (connection.status !== 'ready') return connection.start();
    sidebar.reload();
    await attention.refresh();
  });

  command('threavia.showWaiting', async () => {
    await vscode.commands.executeCommand('threavia.sidebar.focus');
    if (connection.status === 'ready') {
      await tree.reveal(sidebar.waitingNode, { expand: true, select: true, focus: true });
    }
  });

  command('threavia.openSession', (target: unknown) => runOpenSession(target));

  command('threavia.ask', async (uri?: vscode.Uri) => {
    if (!(await ensureCore())) return;
    if (connection.status !== 'ready') {
      void vscode.window.showInformationMessage('Sign in to Threavia first.', 'Sign In').then(
        (choice) => choice && vscode.commands.executeCommand('threavia.signIn'),
      );
      return;
    }
    await askAboutThis(client, uri instanceof vscode.Uri ? uri : undefined);
  });

  // From a sidebar row, or from the title bar of the conversation in front,
  // which hands the command nothing that names the Session.
  const targetSession = (target: unknown) => sessionIdOf(target) ?? panels.activeSessionId;

  command('threavia.openInBrowser', (target: unknown) => {
    const sessionId = targetSession(target);
    if (sessionId) return openInBrowser(sessionId);
  });

  command('threavia.pinSession', (target: unknown) => pin(targetSession(target), true));
  command('threavia.unpinSession', (target: unknown) => pin(targetSession(target), false));

  const pin = async (sessionId: string | undefined, pinned: boolean) => {
    if (!sessionId) return;
    try {
      await client.pinSession(sessionId, pinned);
      // The stream says so too; doing it here is what makes the click count
      // when this window's stream is down.
      sidebar.invalidateSessions();
      panels.refresh(sessionId);
    } catch (error) {
      failed(error, pinned ? 'Not pinned' : 'Not unpinned');
    }
  };

  command('threavia.renameSession', async (node: SessionNode) => {
    const title = await vscode.window.showInputBox({
      title: 'Rename the session',
      value: node.session.title,
      validateInput: (value) => (value.trim() ? undefined : 'A session needs a title.'),
    });
    if (title === undefined || title.trim() === node.session.title) return;
    try {
      await client.renameSession(node.session.id, title.trim());
      sidebar.invalidateSessions();
    } catch (error) {
      failed(error, 'Not renamed');
    }
  });

  // Archiving is not deleting, so it asks for no confirmation: the message
  // that says it happened carries the way back.
  command('threavia.archiveSession', async (node: SessionNode) => {
    try {
      await client.archiveSession(node.session.id);
      sidebar.invalidateSessions();
    } catch (error) {
      return failed(error, 'Not archived');
    }
    const choice = await vscode.window.showInformationMessage(`Archived “${titleOf(node.session)}”.`, 'Undo');
    if (!choice) return;
    try {
      await client.restoreSession(node.session.id);
      sidebar.invalidateSessions();
    } catch (error) {
      failed(error, 'Not restored');
    }
  });

  command('threavia.refreshRepository', async (node: SessionNode) => {
    try {
      const repo = await vscode.window.withProgress(
        { location: { viewId: 'threavia.sidebar' }, title: 'Asking origin…' },
        () => sidebar.refreshRepository(node.session.id),
      );
      if (repo?.fetchError) void vscode.window.showWarningMessage(`Origin did not answer: ${repo.fetchError}`);
    } catch (error) {
      failed(error, 'Not refreshed');
    }
  });

  command('threavia.approve', async (node: RequestNode) => {
    const request = node.item.kind === 'validation' ? attention.findValidation(node.item.id) : undefined;
    if (request) await answers.decide(request, true);
  });
  command('threavia.deny', async (node: RequestNode) => {
    const request = node.item.kind === 'validation' ? attention.findValidation(node.item.id) : undefined;
    if (request) await answers.decide(request, false);
  });
  command('threavia.answer', async (node: RequestNode) => {
    const request = node.item.kind === 'input' ? attention.findUserInput(node.item.id) : undefined;
    if (request) await answers.ask(request);
  });

  void connection.start();
  return { client, bus, attention };
}

export function deactivate() {
  // Everything is in context.subscriptions, disposed by the editor.
}

function failed(error: unknown, outcome: string) {
  const reason = error instanceof ApiError || error instanceof Error ? error.message : String(error);
  void vscode.window.showErrorMessage(`${outcome}: ${reason}`);
}
