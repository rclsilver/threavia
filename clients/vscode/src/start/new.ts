import * as vscode from 'vscode';

import { ApiError, type CoreClient } from '../api/client';
import type { Project } from '../api/types';
import type { DraftStart } from '../conversation/draft';
import { pickBackend, pickDirectory, pickProject } from './pickers';

const TITLE = 'New Session';

/**
 * "New Session": the Project (unless it was started from one in the
 * sidebar), the backend and the working directory, as "Ask Threavia about
 * this" asks them. Nothing is created here: the choice opens a draft, and the
 * first message sent from it starts the Session.
 */
export async function chooseNewSession(client: CoreClient, from?: Project): Promise<DraftStart | undefined> {
  try {
    const project = from ?? (await pickProject(client, TITLE, 'Which Project does the session work in?'));
    if (!project) return undefined;
    const backend = await pickBackend(client, TITLE);
    if (!backend) return undefined;
    const directory = await pickDirectory(client, project, TITLE);
    if (directory === undefined) return undefined;
    return {
      projectId: project.id,
      projectName: project.name,
      backendInstanceId: backend.id,
      backendName: backend.name,
      workingDirectoryId: directory?.id ?? null,
      directoryName: directory?.name,
    };
  } catch (error) {
    const reason = error instanceof ApiError || error instanceof Error ? error.message : String(error);
    void vscode.window.showErrorMessage(`No new session: ${reason}`);
    return undefined;
  }
}
