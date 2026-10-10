import * as vscode from 'vscode';

import type { CoreClient } from '../api/client';
import type { BackendInstance, KnownDirectory, Project } from '../api/types';
import { humanise } from '../conversation/format';
import { backendIcon, backendTooltip } from '../conversation/backend';
import { isCurrentProject, orderProjects } from '../tree/model';

/**
 * The choices a new Session starts with — Project, backend, working
 * directory — asked in the editor's own pickers, each with the likely answer
 * first, so the usual path is Enter, Enter, Enter. "Ask Threavia about this"
 * and "New Session" ask them the same way; `title` says which is asking.
 */

export type Item<T> = vscode.QuickPickItem & { value: T };

export async function pick<T>(title: string, placeHolder: string, items: Item<T>[]): Promise<T | undefined> {
  const picked = await vscode.window.showQuickPick(items, { title, placeHolder, ignoreFocusOut: true, matchOnDescription: true });
  return picked?.value;
}

/** The Projects of a Core, the one its setting names first. */
export async function pickProject(
  client: CoreClient,
  setting: string,
  title: string,
  placeHolder: string,
): Promise<Project | undefined> {
  const projects = orderProjects(await client.projects(), setting);
  if (projects.length === 0) {
    void vscode.window.showInformationMessage('There is no Project yet. Create one in Threavia first.');
    return undefined;
  }
  return pick(
    `${title}: Project`,
    placeHolder,
    projects.map((project) => ({
      label: project.name,
      description: isCurrentProject(project, setting) ? 'default' : undefined,
      value: project,
    })),
  );
}

const READINESS: Record<string, number> = { READY: 0, DEGRADED: 1, UNAVAILABLE: 2, OFFLINE: 3 };

/** The backends that can take work, the ready ones first. */
export async function pickBackend(client: CoreClient, title: string): Promise<BackendInstance | undefined> {
  const backends = (await client.backends())
    .filter((backend) => backend.ownershipStatus !== 'REVOKED')
    .sort((a, b) => (READINESS[a.operationalStatus] ?? 9) - (READINESS[b.operationalStatus] ?? 9));
  if (backends.length === 0) {
    void vscode.window.showInformationMessage('There is no backend to run the work. Connect one to Threavia first.');
    return undefined;
  }
  return pick(
    `${title}: Backend`,
    'Which machine does the work?',
    backends.map((backend) => ({
      label: `$(${backendIcon(backend.backend)}) ${backend.name}`,
      description: `${backend.backend || 'Unknown provider'} · ${humanise(backend.operationalStatus)}`,
      detail: backendTooltip(backend),
      value: backend,
    })),
  );
}

/**
 * The working directory, asked only when the Project has some: null for none,
 * undefined when the picker was dismissed. The one named like a folder of this
 * workspace comes first: it is most likely the same repository.
 */
export async function pickDirectory(
  client: CoreClient,
  project: Project,
  title: string,
): Promise<KnownDirectory | null | undefined> {
  const directories = await client.directories(project.id);
  if (directories.length === 0) return null;
  const names = new Set((vscode.workspace.workspaceFolders ?? []).map((folder) => folder.name.toLowerCase()));
  const sorted = [...directories].sort(
    (a, b) => Number(names.has(b.name.toLowerCase())) - Number(names.has(a.name.toLowerCase())),
  );
  return pick<KnownDirectory | null>(`${title}: Working directory`, 'Where does the agent start?', [
    ...sorted.map((directory) => ({
      label: `$(folder) ${directory.name}`,
      description: names.has(directory.name.toLowerCase()) ? 'this workspace' : directory.description,
      value: directory,
    })),
    { label: 'None', description: 'the backend’s default', value: null },
  ]);
}
