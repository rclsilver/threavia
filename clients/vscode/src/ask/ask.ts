import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { BackendInstance, KnownDirectory, Project, Session } from '../api/types';
import { currentProjectSetting } from '../config';
import { humanise } from '../conversation/format';
import { openSession } from '../sessions';
import { ago, isCurrentProject, orderProjects, orderSessions, titleOf } from '../tree/model';
import { askMessage, lineRange, type Excerpt } from './message';

/**
 * "Ask Threavia about this": the selection, or the whole file, sent to an
 * agent with what the person wants done with it.
 *
 * Every choice is asked in the editor's own pickers, each with the likely
 * answer first, so the usual path is Enter, Enter, Enter, type, Enter.
 */
export async function askAboutThis(client: CoreClient, uri?: vscode.Uri): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  const document = uri && editor?.document.uri.toString() !== uri.toString()
    ? await vscode.workspace.openTextDocument(uri)
    : editor?.document;
  if (!document) {
    void vscode.window.showInformationMessage('Open a file, or select code in one, to ask about it.');
    return;
  }
  const excerpt = excerptOf(document, editor?.document === document ? editor.selection : undefined);
  if (!excerpt.code.trim()) {
    void vscode.window.showInformationMessage('There is nothing in this file to ask about.');
    return;
  }

  try {
    const project = await pickProject(client);
    if (!project) return;
    const target = await pickSession(client, project);
    if (!target) return;

    let start: { backend: BackendInstance; directory: KnownDirectory | null } | undefined;
    if (target === 'new') {
      const backend = await pickBackend(client);
      if (!backend) return;
      const directory = await pickDirectory(client, project);
      if (directory === undefined) return;
      start = { backend, directory };
    }

    const instruction = await vscode.window.showInputBox({
      title: 'Ask Threavia',
      prompt: `About ${excerpt.wholeFile ? excerpt.path : `${excerpt.path}:${excerpt.startLine}-${excerpt.endLine}`}`,
      placeHolder: 'What should the agent do with this code?',
      ignoreFocusOut: true,
      validateInput: (value) => (value.trim() ? undefined : 'Say what you want done.'),
    });
    if (!instruction?.trim()) return;
    const message = askMessage(instruction, excerpt);

    let sessionId: string;
    if (start) {
      const created = await client.startSession({
        projectId: project.id,
        backendInstanceId: start.backend.id,
        workingDirectoryId: start.directory?.id ?? null,
        message,
      });
      sessionId = created.session.id;
    } else {
      sessionId = (target as Session).id;
      // Queued: if the Session is working, this waits its turn rather than
      // reaching into what it is doing.
      await client.postMessage(sessionId, message, 'QUEUE');
    }
    await openSession(sessionId);
  } catch (error) {
    const reason = error instanceof ApiError || error instanceof Error ? error.message : String(error);
    void vscode.window.showErrorMessage(`Not sent: ${reason}`);
  }
}

function excerptOf(document: vscode.TextDocument, selection: vscode.Selection | undefined): Excerpt {
  const path = vscode.workspace.asRelativePath(document.uri, false).replace(/\\/g, '/');
  if (selection && !selection.isEmpty) {
    const { startLine, endLine } = lineRange(selection.start, selection.end);
    const range = new vscode.Range(startLine - 1, 0, endLine - 1, document.lineAt(endLine - 1).text.length);
    return { path, startLine, endLine, languageId: document.languageId, code: document.getText(range) };
  }
  return {
    path,
    startLine: 1,
    endLine: document.lineCount,
    languageId: document.languageId,
    code: document.getText(),
    wholeFile: true,
  };
}

type Item<T> = vscode.QuickPickItem & { value: T };

async function pick<T>(title: string, placeHolder: string, items: Item<T>[]): Promise<T | undefined> {
  const picked = await vscode.window.showQuickPick(items, { title, placeHolder, ignoreFocusOut: true, matchOnDescription: true });
  return picked?.value;
}

async function pickProject(client: CoreClient): Promise<Project | undefined> {
  const setting = currentProjectSetting();
  const projects = orderProjects(await client.projects(), setting);
  if (projects.length === 0) {
    void vscode.window.showInformationMessage('There is no Project to ask in yet. Create one in Threavia first.');
    return undefined;
  }
  return pick(
    'Ask Threavia: Project',
    'Which Project is this about?',
    projects.map((project) => ({
      label: project.name,
      description: isCurrentProject(project, setting) ? 'this workspace' : undefined,
      value: project,
    })),
  );
}

async function pickSession(client: CoreClient, project: Project): Promise<Session | 'new' | undefined> {
  const sessions = orderSessions(await client.sessions(project.id), new Map()).slice(0, 15);
  return pick<Session | 'new'>('Ask Threavia: Session', 'Start a new session, or add to one', [
    { label: '$(add) New session', value: 'new' },
    ...(sessions.length > 0 ? [{ label: 'Recent sessions', kind: vscode.QuickPickItemKind.Separator, value: 'new' as const }] : []),
    ...sessions.map((session) => ({
      label: titleOf(session),
      description: session.activeJobStatus ? humanise(session.activeJobStatus) : undefined,
      detail: `Updated ${ago(session.updatedAt)}`,
      value: session,
    })),
  ]);
}

const READINESS: Record<string, number> = { READY: 0, DEGRADED: 1, UNAVAILABLE: 2, OFFLINE: 3 };

/** The backends that can take work, the ready ones first. */
async function pickBackend(client: CoreClient): Promise<BackendInstance | undefined> {
  const backends = (await client.backends())
    .filter((backend) => backend.ownershipStatus !== 'REVOKED')
    .sort((a, b) => (READINESS[a.operationalStatus] ?? 9) - (READINESS[b.operationalStatus] ?? 9));
  if (backends.length === 0) {
    void vscode.window.showInformationMessage('There is no backend to run the work. Connect one to Threavia first.');
    return undefined;
  }
  return pick(
    'Ask Threavia: Backend',
    'Which machine does the work?',
    backends.map((backend) => ({
      label: `$(server) ${backend.name}`,
      description: humanise(backend.operationalStatus),
      value: backend,
    })),
  );
}

/**
 * The working directory, asked only when the Project has some. The one named
 * like a folder of this workspace comes first: it is most likely the same
 * repository.
 */
async function pickDirectory(client: CoreClient, project: Project): Promise<KnownDirectory | null | undefined> {
  const directories = await client.directories(project.id);
  if (directories.length === 0) return null;
  const names = new Set((vscode.workspace.workspaceFolders ?? []).map((folder) => folder.name.toLowerCase()));
  const sorted = [...directories].sort(
    (a, b) => Number(names.has(b.name.toLowerCase())) - Number(names.has(a.name.toLowerCase())),
  );
  return pick<KnownDirectory | null>('Ask Threavia: Working directory', 'Where does the agent start?', [
    ...sorted.map((directory) => ({
      label: `$(folder) ${directory.name}`,
      description: names.has(directory.name.toLowerCase()) ? 'this workspace' : directory.description,
      value: directory,
    })),
    { label: 'None', description: 'the backend’s default', value: null },
  ]);
}
