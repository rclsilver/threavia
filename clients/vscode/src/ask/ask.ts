import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { BackendInstance, KnownDirectory, Project, Session } from '../api/types';
import { humanise } from '../conversation/format';
import { openSession } from '../sessions';
import { pick, pickBackend, pickDirectory, pickProject } from '../start/pickers';
import { ago, orderSessions, titleOf } from '../tree/model';
import { askMessage, lineRange, type Excerpt } from './message';

const TITLE = 'Ask Threavia';

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
    const project = await pickProject(client, TITLE, 'Which Project is this about?');
    if (!project) return;
    const target = await pickSession(client, project);
    if (!target) return;

    let start: { backend: BackendInstance; directory: KnownDirectory | null } | undefined;
    if (target === 'new') {
      const backend = await pickBackend(client, TITLE);
      if (!backend) return;
      const directory = await pickDirectory(client, project, TITLE);
      if (directory === undefined) return;
      start = { backend, directory };
    }

    const instruction = await vscode.window.showInputBox({
      title: TITLE,
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

async function pickSession(client: CoreClient, project: Project): Promise<Session | 'new' | undefined> {
  const sessions = orderSessions(await client.sessions(project.id), new Map()).slice(0, 15);
  return pick<Session | 'new'>(`${TITLE}: Session`, 'Start a new session, or add to one', [
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
