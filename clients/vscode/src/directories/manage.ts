import * as vscode from 'vscode';

import type { KnownDirectory, Project } from '../api/types';
import type { Core } from '../cores/core';
import { pickBackend, pickProject } from '../start/pickers';

async function fields(directory?: KnownDirectory) {
  const name = await vscode.window.showInputBox({ title: 'Directory name', value: directory?.name, ignoreFocusOut: true, validateInput: (value) => value.trim() ? undefined : 'Enter a directory name.' });
  if (name === undefined) return;
  const description = await vscode.window.showInputBox({ title: 'Directory description (optional)', value: directory?.description ?? '', ignoreFocusOut: true });
  if (description === undefined) return;
  const gitRemote = await vscode.window.showInputBox({ title: 'Git remote (optional)', value: directory?.gitRemote ?? '', ignoreFocusOut: true });
  if (gitRemote === undefined) return;
  return { name: name.trim(), description: description.trim(), gitRemote: gitRemote.trim() };
}

/** Native pickers let a directory be managed without leaving the editor. */
export async function manageDirectories(core: Core, project?: Project) {
  const scope = await vscode.window.showQuickPick(['Project', 'Global'], { title: 'Known directories', placeHolder: 'Project (default) or shared across all your projects?', ignoreFocusOut: true });
  if (!scope) return;
  if (scope === 'Project' && !project) project = await pickProject(core.client, core.projectSetting, 'Known directories', 'Which Project?', core.id);
  if (scope === 'Project' && !project) return;
  const projectId = scope === 'Project' ? project!.id : '';
  const title = scope === 'Project' ? `Directories: ${project!.name}` : 'Global directories';
  while (core.ready) {
    const directories = await core.client.directories(projectId);
    const selected = await vscode.window.showQuickPick([
      { label: '$(add) New directory…', directory: undefined },
      ...directories.map((directory) => ({ label: `$(folder) ${directory.name}`, description: directory.projectId ? 'project' : 'global', detail: directory.description, directory })),
    ], { title, ignoreFocusOut: true, matchOnDescription: true });
    if (!selected) return;
    const directory = selected.directory;
    if (!directory) {
      let targetProjectId = projectId;
      if (projectId) {
        const target = await vscode.window.showQuickPick(['This project', 'Global — all my projects'], { title: 'New directory scope', ignoreFocusOut: true });
        if (!target) continue;
        if (target.startsWith('Global')) targetProjectId = '';
      }
      const input = await fields();
      if (input) await core.client.createDirectory(targetProjectId, input);
      continue;
    }
    const action = await vscode.window.showQuickPick(['Edit metadata', ...(directory.projectId ? ['Make global — all my projects'] : []), 'Manage paths on backends', 'Forget directory'], { title: `${directory.name} (${directory.projectId ? 'project' : 'global'})`, ignoreFocusOut: true });
    if (action === 'Edit metadata') {
      const input = await fields(directory);
      if (input) {
        const patch = { ...(input.name !== directory.name && { name: input.name }), ...(input.description !== (directory.description ?? '') && { description: input.description }), ...(input.gitRemote !== (directory.gitRemote ?? '') && { gitRemote: input.gitRemote }) };
        if (Object.keys(patch).length) await core.client.updateDirectory(directory.id, patch);
      }
    } else if (action === 'Make global — all my projects') {
      await core.client.makeDirectoryGlobal(directory.id);
    } else if (action === 'Manage paths on backends') {
      await managePaths(core, directory);
    } else if (action === 'Forget directory') {
      const answer = await vscode.window.showWarningMessage(`Forget “${directory.name}” and all its paths?`, { modal: true, detail: 'Files stay on every machine. This is refused while a session uses it as its working directory.' }, 'Forget directory');
      if (answer === 'Forget directory') await core.client.deleteDirectory(directory.id);
    }
  }
}

async function managePaths(core: Core, directory: KnownDirectory) {
  const bindings = await core.client.bindings(directory.id);
  const backends = await core.client.backends();
  const choice = await vscode.window.showQuickPick([
    { label: '$(add) Add path on a backend…', binding: undefined },
    ...bindings.map((binding) => ({ label: backends.find((backend) => backend.id === binding.backendInstanceId)?.name ?? binding.backendInstanceId, description: binding.path, binding })),
  ], { title: `Paths: ${directory.name}`, ignoreFocusOut: true });
  if (!choice) return;
  const binding = choice.binding;
  if (binding) {
    const action = await vscode.window.showQuickPick(['Edit path', 'Forget path'], { title: choice.label, placeHolder: binding.path, ignoreFocusOut: true });
    if (!action) return;
    if (action === 'Forget path') {
      const answer = await vscode.window.showWarningMessage(`Forget the path ${binding.path}?`, { modal: true, detail: 'Files stay on the backend.' }, 'Forget path');
      if (answer === 'Forget path') await core.client.unbindDirectory(directory.id, binding.backendInstanceId);
      return;
    }
  }
  const backendId = binding?.backendInstanceId ?? (await pickBackend(core.client, 'Directory path'))?.id;
  if (!backendId) return;
  const path = await vscode.window.showInputBox({ title: 'Absolute path on this backend', value: binding?.path, ignoreFocusOut: true, validateInput: (value) => value.trim().startsWith('/') ? undefined : 'Enter an absolute path starting with /.' });
  if (path !== undefined) await core.client.bindDirectory(directory.id, backendId, path.trim());
}
