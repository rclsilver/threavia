import * as vscode from 'vscode';

import { ApiError } from './api/client';
import { askAboutThis } from './ask/ask';
import { Notifier, WaitingStatus } from './attention/notifier';
import { promptName, promptUrl } from './config';
import { ConversationPanels } from './conversation/panel';
import type { Core } from './cores/core';
import { coreIdOf, sessionRefOf, type SessionRef } from './cores/refs';
import { Cores } from './cores/registry';
import { hostOf, newCoreId } from './cores/settings';
import { VirtualDocuments } from './diff/documents';
import { createIdentity } from './identity';
import { CurrentProject } from './knowledge/current';
import { MemoryView } from './knowledge/memory';
import { TasksView } from './knowledge/tasks';
import { openInBrowser, runOpenSession, setSessionOpener } from './sessions';
import { chooseNewSession } from './start/new';
import { UNTITLED, titleOf } from './tree/model';
import { SidebarProvider, type Node, type RequestNode, type SessionNode } from './tree/provider';
import { PathResolver } from './workspace/resolve';

/**
 * What the extension exposes to the code that builds on it: every Core, with
 * its client, stream and what waits there, rather than a second connection.
 */
export interface Threavia {
  cores: Cores;
}

export function activate(context: vscode.ExtensionContext): Threavia {
  const identity = createIdentity(context);

  // Documents the extension makes up are shared by every Core: their paths
  // start with the Core's id.
  const documents = new VirtualDocuments('threavia-diff');
  const artifactDocuments = new VirtualDocuments('threavia-artifact');
  const cores = new Cores(context, identity, artifactDocuments);

  const sidebar = new SidebarProvider(cores);
  const tree = vscode.window.createTreeView('threavia.sidebar', {
    treeDataProvider: sidebar,
    showCollapseAll: true,
  });

  // The conversation: one webview per Session, with what it opens — diffs,
  // artifacts, files — in the editor's own documents.
  const paths = new PathResolver();
  const panels = new ConversationPanels({ extensionUri: context.extensionUri, cores, documents, paths });
  setSessionOpener(
    (ref) => {
      panels.open(ref);
      return Promise.resolve();
    },
    () => cores.ids,
  );

  // Tasks and Memory show one Project, the one the person last turned to.
  const currentProject = new CurrentProject(cores, context.workspaceState);
  const tasks = new TasksView(currentProject, cores);
  const memory = new MemoryView(currentProject, cores);
  panels.onActiveProject = (coreId, projectId) => currentProject.follow(coreId, projectId);

  /** A Session's title from what is already known, asking Core only as a last resort. */
  const sessionTitle = async (core: Core, sessionId: string): Promise<string> => {
    const known = sidebar.findSession(core.id, sessionId);
    if (known) return titleOf(known);
    const asking = [...core.attention.current.validations, ...core.attention.current.userInputs].find(
      (request) => request.scope.sessionId === sessionId,
    );
    if (asking?.context?.sessionTitle) return asking.context.sessionTitle;
    try {
      return titleOf((await core.client.snapshot(sessionId, 1)).session);
    } catch {
      return UNTITLED;
    }
  };

  context.subscriptions.push(
    cores,
    panels,
    paths,
    documents,
    artifactDocuments,
    sidebar,
    tree,
    tree.onDidChangeSelection((event) => {
      const node = event.selection[0];
      sidebar.select(node);
      const project = projectOf(node);
      if (project) currentProject.follow(project.coreId, project.projectId);
    }),
    currentProject,
    tasks,
    memory,
    new WaitingStatus(cores),
    new Notifier(cores, sessionTitle),
  );

  const command = (name: string, run: (...args: never[]) => unknown) =>
    context.subscriptions.push(vscode.commands.registerCommand(name, run));

  // ------------------------------------------------------------- the Cores

  /**
   * The Core a command is about: the one its argument names (a Core's row, a
   * welcome link), the only one, or the one the person picks among
   * `candidates`.
   */
  const coreFor = async (target: unknown, title: string, candidates: Core[] = cores.list()): Promise<Core | undefined> => {
    const named = cores.get(coreIdOf(target));
    if (named) return named;
    return cores.pick(title, 'Which Core?', candidates);
  };

  /** Signs in to a Core once it answered, saying so when it does not. */
  const signIn = async (core: Core): Promise<void> => {
    if (core.state === 'connecting') await core.connection.settled;
    if (core.state === 'unreachable') await core.connection.start();
    if (core.state === 'unreachable') {
      void vscode.window
        .showErrorMessage(`${core.name} does not answer at ${core.url}.`, 'Set Core URL')
        .then((choice) => choice && vscode.commands.executeCommand('threavia.setCoreUrl', core.id));
      return;
    }
    if (core.state === 'signedOut') await core.auth.signIn();
  };

  command('threavia.addCore', async () => {
    const url = await promptUrl('Add a Threavia Core');
    if (!url) return;
    const existing = cores.list().find((core) => core.url === url);
    if (existing) {
      void vscode.window.showInformationMessage(`${existing.name} is already at ${url}.`);
      return;
    }
    const name = await promptName('Add a Threavia Core', url);
    if (!name) return;
    const core = await cores.add({ id: newCoreId(), name, url });
    if (core) await signIn(core);
  });

  command('threavia.removeCore', async (target?: unknown) => {
    const core = await coreFor(target, 'Remove a Core');
    if (!core) return;
    const choice = await vscode.window.showWarningMessage(
      `Remove ${core.name}?`,
      {
        modal: true,
        detail: `Its sign-in is forgotten and its conversations close. Nothing changes on the Core itself (${core.url}).`,
      },
      'Remove',
    );
    if (choice === 'Remove') await cores.remove(core.id);
  });

  command('threavia.renameCore', async (target?: unknown) => {
    const core = await coreFor(target, 'Rename a Core');
    if (!core) return;
    const name = await promptName(`Rename ${core.name}`, core.url, core.name);
    if (name && name !== core.name) await cores.update(core.id, { name });
  });

  // Kept from the time there was one Core: with none, it adds one; otherwise
  // it changes the address of the one picked.
  command('threavia.setCoreUrl', async (target?: unknown) => {
    if (cores.list().length === 0) return vscode.commands.executeCommand('threavia.addCore');
    const core = await coreFor(target, 'Set Core URL');
    if (!core) return;
    const url = await promptUrl(`The URL of ${core.name}`, core.url);
    if (url === undefined) return;
    // The same URL saved again changes nothing, so it is taken as "try again".
    if (url === core.url) return core.connection.start();
    // A name that was only the host follows the new host.
    const name = core.name === hostOf(core.url) ? hostOf(url) : core.name;
    await cores.update(core.id, { url, name });
    await core.connection.settled;
    if (core.state === 'signedOut') await core.auth.signIn();
  });

  command('threavia.signIn', async (target?: unknown) => {
    if (cores.list().length === 0) return vscode.commands.executeCommand('threavia.addCore');
    // Picked among the Cores that still need it, when nothing names one.
    const waiting = cores.list().filter((core) => !core.ready);
    const core = await coreFor(target, 'Sign in to Threavia', waiting.length > 0 ? waiting : cores.list());
    if (core) await signIn(core);
  });

  command('threavia.signOut', async (target?: unknown) => {
    const core = await coreFor(
      target,
      'Sign out of Threavia',
      cores.list().filter((candidate) => candidate.auth.signedIn),
    );
    if (!core) return;
    await core.auth.signOut();
    void vscode.window.showInformationMessage(cores.several ? `Signed out of ${core.name}.` : 'Signed out of Threavia.');
  });

  command('threavia.refresh', async (target?: unknown) => {
    if (cores.list().length === 0) return vscode.commands.executeCommand('threavia.addCore');
    const named = cores.get(coreIdOf(target));
    for (const core of named ? [named] : cores.list()) {
      if (!core.ready) {
        void core.connection.start();
        continue;
      }
      sidebar.reload(core.id);
      void core.attention.refresh();
    }
    currentProject.reload();
    tasks.reload();
    memory.reload();
  });

  command('threavia.switchProject', async () => {
    if (await ensureReady()) await currentProject.choose();
  });
  tasks.register(command, context.subscriptions);
  memory.register(command, context.subscriptions);

  command('threavia.showWaiting', async () => {
    await vscode.commands.executeCommand('threavia.sidebar.focus');
    // Every Core with something waiting has its Waiting node opened; the
    // first one is where the focus goes.
    const ready = cores.ready();
    const waiting = ready.filter(
      (core) => core.attention.current.validations.length + core.attention.current.userInputs.length > 0,
    );
    let focus = true;
    for (const core of waiting.length > 0 ? waiting : ready.slice(0, 1)) {
      await tree.reveal(sidebar.waitingNode(core.id), { expand: true, select: focus, focus });
      focus = false;
    }
  });

  command('threavia.openSession', (target: unknown, second?: unknown) => runOpenSession(target, second));

  /** Whether a Core can be asked now, offering the sign-in when none can. */
  const ensureReady = async (): Promise<boolean> => {
    if (cores.list().length === 0) {
      await vscode.commands.executeCommand('threavia.addCore');
      return cores.ready().length > 0;
    }
    if (cores.ready().length > 0) return true;
    void vscode.window
      .showInformationMessage('Sign in to Threavia first.', 'Sign In')
      .then((choice) => choice && vscode.commands.executeCommand('threavia.signIn'));
    return false;
  };

  /** The Core a new Session or a question goes to: picked among those that can be asked. */
  const readyCore = async (title: string, target?: unknown): Promise<Core | undefined> => {
    if (!(await ensureReady())) return undefined;
    return coreFor(target, title, cores.ready());
  };

  command('threavia.ask', async (uri?: vscode.Uri) => {
    const core = await readyCore('Ask Threavia');
    if (core) await askAboutThis(core, cores.several, uri instanceof vscode.Uri ? uri : undefined);
  });

  // From a Project's row the Core and the Project are known; from the view's
  // title bar or the command palette they are asked first.
  command('threavia.newSession', async (node?: Node) => {
    const core = await readyCore('New Session', node);
    if (!core) return;
    const start = await chooseNewSession(core, cores.several, node?.type === 'project' ? node.project : undefined);
    if (start) panels.openDraft(start);
  });

  /** The Project of a row of the Sessions view, so Tasks and Memory follow the selection. */
  const projectOf = (node: Node | undefined): { coreId: string; projectId: string } | undefined => {
    if (node?.type === 'project') return { coreId: node.coreId, projectId: node.project.id };
    if (node?.type === 'session') return { coreId: node.coreId, projectId: node.session.projectId };
    if (node?.type === 'request') {
      const projectId = sidebar.findSession(node.coreId, node.item.sessionId)?.projectId;
      return projectId ? { coreId: node.coreId, projectId } : undefined;
    }
    return undefined;
  };

  // From a sidebar row, or from the title bar of the conversation in front,
  // which hands the command nothing that names the Session.
  const targetSession = (target: unknown): SessionRef | undefined =>
    (target ? sessionRefOf(target, cores.ids) : undefined) ?? panels.activeSession;

  command('threavia.openInBrowser', (target: unknown) => {
    const ref = targetSession(target);
    const core = cores.get(ref?.coreId);
    if (ref && core) return openInBrowser(core.url, ref.sessionId);
  });

  command('threavia.pinSession', (target: unknown) => pin(targetSession(target), true));
  command('threavia.unpinSession', (target: unknown) => pin(targetSession(target), false));

  const pin = async (ref: SessionRef | undefined, pinned: boolean) => {
    const core = cores.get(ref?.coreId);
    if (!ref || !core) return;
    try {
      await core.client.pinSession(ref.sessionId, pinned);
      // The stream says so too; doing it here is what makes the click count
      // when this window's stream is down.
      sidebar.invalidateSessions(core.id);
      panels.refresh(ref);
    } catch (error) {
      failed(error, pinned ? 'Not pinned' : 'Not unpinned');
    }
  };

  /** The Core of a row a context menu was opened on. */
  const coreOf = (node: { coreId: string }): Core | undefined => cores.get(node.coreId);

  command('threavia.renameSession', async (node: SessionNode) => {
    const core = coreOf(node);
    if (!core) return;
    const title = await vscode.window.showInputBox({
      title: 'Rename the session',
      value: node.session.title,
      validateInput: (value) => (value.trim() ? undefined : 'A session needs a title.'),
    });
    if (title === undefined || title.trim() === node.session.title) return;
    try {
      await core.client.renameSession(node.session.id, title.trim());
      sidebar.invalidateSessions(core.id);
    } catch (error) {
      failed(error, 'Not renamed');
    }
  });

  // Archiving is not deleting, so it asks for no confirmation: the message
  // that says it happened carries the way back.
  command('threavia.archiveSession', async (node: SessionNode) => {
    const core = coreOf(node);
    if (!core) return;
    try {
      await core.client.archiveSession(node.session.id);
      sidebar.invalidateSessions(core.id);
    } catch (error) {
      return failed(error, 'Not archived');
    }
    const choice = await vscode.window.showInformationMessage(`Archived “${titleOf(node.session)}”.`, 'Undo');
    if (!choice) return;
    try {
      await core.client.restoreSession(node.session.id);
      sidebar.invalidateSessions(core.id);
    } catch (error) {
      failed(error, 'Not restored');
    }
  });

  command('threavia.refreshRepository', async (node: SessionNode) => {
    const core = coreOf(node);
    if (!core) return;
    try {
      const repo = await vscode.window.withProgress(
        { location: { viewId: 'threavia.sidebar' }, title: 'Asking origin…' },
        () => sidebar.refreshRepository(core.id, node.session.id),
      );
      if (repo?.fetchError) void vscode.window.showWarningMessage(`Origin did not answer: ${repo.fetchError}`);
    } catch (error) {
      failed(error, 'Not refreshed');
    }
  });

  command('threavia.approve', async (node: RequestNode) => {
    const core = coreOf(node);
    const request = node.item.kind === 'validation' ? core?.attention.findValidation(node.item.id) : undefined;
    if (core && request) await core.answers.decide(request, true);
  });
  command('threavia.deny', async (node: RequestNode) => {
    const core = coreOf(node);
    const request = node.item.kind === 'validation' ? core?.attention.findValidation(node.item.id) : undefined;
    if (core && request) await core.answers.decide(request, false);
  });
  command('threavia.answer', async (node: RequestNode) => {
    const core = coreOf(node);
    const request = node.item.kind === 'input' ? core?.attention.findUserInput(node.item.id) : undefined;
    if (core && request) await core.answers.ask(request);
  });

  void cores.start();
  return { cores };
}

export function deactivate() {
  // Everything is in context.subscriptions, disposed by the editor.
}

function failed(error: unknown, outcome: string) {
  const reason = error instanceof ApiError || error instanceof Error ? error.message : String(error);
  void vscode.window.showErrorMessage(`${outcome}: ${reason}`);
}
