import * as vscode from 'vscode';

import type { Project, Skill } from '../api/types';
import { bytes } from '../conversation/format';
import type { Core } from '../cores/core';
import type { VirtualDocuments } from '../diff/documents';
import { pickProject } from '../start/pickers';
import { readFolder, tarGz } from './pack';

/**
 * Native pickers let Skills be installed, read and turned on or off without
 * leaving the editor. Seen from a Project, the global Skills are listed too,
 * with whether this Project uses them.
 */
export async function manageSkills(core: Core, documents: VirtualDocuments, project?: Project) {
  const scope = await vscode.window.showQuickPick(['Project', 'Global'], { title: 'Skills', placeHolder: 'A project’s skills, or those of all your projects?', ignoreFocusOut: true });
  if (!scope) return;
  if (scope === 'Project' && !project) project = await pickProject(core.client, core.projectSetting, 'Skills', 'Which Project?', core.id);
  if (scope === 'Project' && !project) return;
  const projectId = scope === 'Project' ? project!.id : '';
  const title = scope === 'Project' ? `Skills: ${project!.name}` : 'Global skills';
  while (core.ready) {
    const [local, global, projects] = await Promise.all([
      projectId ? core.client.skills(projectId) : [],
      core.client.skills(''),
      projectId ? [] : core.client.projects(),
    ]);
    const stateOf = (skill: Skill) => {
      const off = skill.disabledProjectIds ?? [];
      if (projectId) {
        if (local.some((other) => other.name === skill.name)) return 'replaced by this project’s skill';
        return off.includes(projectId) ? 'off in this project' : 'active';
      }
      const names = projects.filter((other) => off.includes(other.id)).map((other) => other.name);
      return names.length ? `off in ${names.join(', ')}` : 'active in every project';
    };
    const selected = await vscode.window.showQuickPick([
      { label: '$(add) Install skill…', skill: undefined },
      ...local.map((skill) => ({ label: `$(sparkle) ${skill.name}`, description: 'project', detail: skill.description, skill })),
      ...global.map((skill) => ({ label: `$(sparkle) ${skill.name}`, description: `global · ${stateOf(skill)}`, detail: skill.description, skill })),
    ], { title, ignoreFocusOut: true, matchOnDescription: true });
    if (!selected) return;
    const skill = selected.skill;
    if (!skill) {
      await install(core, projectId);
      continue;
    }
    const isGlobal = !skill.projectId;
    const off = isGlobal && (skill.disabledProjectIds ?? []).includes(projectId);
    const action = await vscode.window.showQuickPick([
      'View files',
      ...(isGlobal ? [] : ['Make global — all my projects']),
      ...(isGlobal && projectId ? [off ? 'Turn on in this project' : 'Turn off in this project'] : []),
      'Uninstall',
    ], { title: `${skill.name} (${isGlobal ? 'global' : 'project'})`, placeHolder: `${skill.installedRevision.slice(0, 12)} · ${skill.source.url || 'uploaded'}`, ignoreFocusOut: true });
    if (action === 'View files') {
      await viewFiles(core, documents, skill);
    } else if (action === 'Make global — all my projects') {
      await core.client.makeSkillGlobal(skill.id);
    } else if (action === 'Turn on in this project' || action === 'Turn off in this project') {
      await core.client.setSkillActivation(projectId, skill.id, off);
    } else if (action === 'Uninstall') {
      const answer = await vscode.window.showWarningMessage(isGlobal ? `Uninstall “${skill.name}” from all your projects?` : `Uninstall “${skill.name}”?`, { modal: true, detail: isGlobal ? 'Every project stops using it from its next Job.' : 'This project stops using it from its next Job.' }, 'Uninstall');
      if (answer === 'Uninstall') await core.client.uninstallSkill(skill.id);
    }
  }
}

const SOURCES = ['Git repository', 'Archive URL', 'Local folder', 'Local archive'];

/** Where a Skill comes from, then where in it. Core fetches and repacks it; nothing in it is run. */
async function install(core: Core, projectId: string) {
  let target = projectId;
  if (projectId) {
    const choice = await vscode.window.showQuickPick(['This project', 'Global — all my projects'], { title: 'Install the skill in', ignoreFocusOut: true });
    if (!choice) return;
    if (choice.startsWith('Global')) target = '';
  }
  const source = await vscode.window.showQuickPick(SOURCES, { title: 'Install the skill from', ignoreFocusOut: true });
  if (!source) return;
  const installing = (run: () => Promise<Skill>) => vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: 'Installing the skill…' }, run);
  let skill: Skill;
  if (source === 'Git repository' || source === 'Archive URL') {
    const git = source === 'Git repository';
    const url = await vscode.window.showInputBox({ title: git ? 'Repository URL' : 'Archive URL', placeHolder: git ? 'https://github.com/owner/repo' : 'https://example.com/skill.tar.gz', ignoreFocusOut: true, validateInput: (value) => value.trim() ? undefined : 'Enter a URL.' });
    if (url === undefined) return;
    const path = await askPath();
    if (path === undefined) return;
    const revision = git ? await vscode.window.showInputBox({ title: 'Branch, tag or commit (optional)', placeHolder: 'The default branch', ignoreFocusOut: true }) : '';
    if (revision === undefined) return;
    skill = await installing(() => core.client.installSkill(target, { type: git ? 'GIT' : 'ARCHIVE', url: url.trim(), path, revision: revision.trim() }));
  } else if (source === 'Local folder') {
    const [folder] = await vscode.window.showOpenDialog({ canSelectFolders: true, canSelectFiles: false, defaultUri: vscode.workspace.workspaceFolders?.[0]?.uri, title: 'The folder holding SKILL.md', openLabel: 'Install' }) ?? [];
    if (!folder) return;
    skill = await installing(async () => core.client.uploadSkill(target, `${folder.path.split('/').pop() || 'skill'}.tar.gz`, tarGz(await readFolder(folder))));
  } else {
    const [file] = await vscode.window.showOpenDialog({ canSelectFiles: true, filters: { 'Skill archive': ['zip', 'tgz', 'gz'] }, title: 'A .zip or .tar.gz holding SKILL.md', openLabel: 'Install' }) ?? [];
    if (!file) return;
    const path = await askPath();
    if (path === undefined) return;
    skill = await installing(async () => core.client.uploadSkill(target, file.path.split('/').pop() ?? '', await vscode.workspace.fs.readFile(file), path));
  }
  void vscode.window.showInformationMessage(`Installed ${skill.name} at ${skill.installedRevision.slice(0, 12)}. It applies to the next Job.`);
}

async function askPath() {
  const path = await vscode.window.showInputBox({ title: 'Path of the skill inside it (optional)', placeHolder: 'Where SKILL.md is, if not at the top', ignoreFocusOut: true });
  return path?.trim();
}

/** The files Core stored, which are what an agent is given; SKILL.md first. */
async function viewFiles(core: Core, documents: VirtualDocuments, skill: Skill) {
  const files = await core.client.skillFiles(skill.id);
  files.sort((a, b) => Number(b.path === 'SKILL.md') - Number(a.path === 'SKILL.md'));
  const picked = await vscode.window.showQuickPick(files.map((file) => ({ label: file.path, description: bytes(file.size), detail: file.text === undefined ? 'Binary or too large to show' : undefined, file })), { title: `Files of ${skill.name}`, ignoreFocusOut: true });
  if (!picked) return;
  if (picked.file.text === undefined) {
    void vscode.window.showInformationMessage(`${picked.file.path} is binary or too large to show.`);
    return;
  }
  // The Core first, as every made-up document; the revision tells versions apart.
  const uri = documents.put(`/${core.id}/${skill.name}@${skill.installedRevision.slice(0, 12)}/${picked.file.path}`, picked.file.text);
  await vscode.window.showTextDocument(uri, { preview: true });
}
