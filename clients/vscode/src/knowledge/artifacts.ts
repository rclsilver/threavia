import { mediaType } from '../../../shared/media';
import * as vscode from 'vscode';

import type { Artifact } from '../api/types';
import type { Core } from '../cores/core';
import type { Cores } from '../cores/registry';
import { artifactKind, bytes } from '../conversation/format';
import type { CurrentProject } from './current';
import { ProjectView, type MessageNode } from './view';

export type ArtifactNode = { type: 'artifact'; coreId: string; artifact: Artifact };

export class ArtifactsView extends ProjectView<Artifact[], ArtifactNode> {
  constructor(current: CurrentProject, cores: Cores) {
    super('threavia.artifacts', current, cores, 'artifacts');
  }

  protected fetch(core: Core, projectId: string) { return core.client.artifacts(projectId); }
  protected loaded() {}
  protected children(data: Artifact[], node?: ArtifactNode): (ArtifactNode | MessageNode)[] {
    if (node) return [];
    if (!data.length) return [{ type: 'message', text: 'No artifacts yet. Attach a file or let an agent publish one.' }];
    return data.map((artifact) => ({ type: 'artifact', coreId: this.current.core!.id, artifact }));
  }
  protected item(node: ArtifactNode) {
    const artifact = node.artifact;
    const item = new vscode.TreeItem(artifact.filename, vscode.TreeItemCollapsibleState.None);
    item.id = `${node.coreId}/${artifact.id}`;
    item.contextValue = 'artifact';
    item.description = bytes(artifact.size);
    item.tooltip = `${artifact.filename}\n${artifact.mimeType} · ${bytes(artifact.size)}\n${new Date(artifact.createdAt).toLocaleString()}`;
    const kind = artifactKind(artifact.mimeType, artifact.filename);
    item.iconPath = new vscode.ThemeIcon(kind === 'image' || kind === 'audio' || kind === 'video' ? 'file-media' : kind === 'html' ? 'file-code' : 'file');
    item.command = { command: 'threavia.openArtifact', title: 'Open Artifact', arguments: [node] };
    return item;
  }
  async open(node: ArtifactNode) {
    const core = this.cores.get(node.coreId);
    if (core) await core.artifacts.open(ref(node.artifact), vscode.ViewColumn.Active);
  }
  async save(node: ArtifactNode) {
    const core = this.cores.get(node.coreId);
    if (core) await core.artifacts.save(ref(node.artifact));
  }
  async remove(node: ArtifactNode) {
    const answer = await vscode.window.showWarningMessage(`Delete ${node.artifact.filename}? Conversation previews that use it will no longer be available.`, { modal: true }, 'Delete');
    if (answer !== 'Delete') return;
    await this.act('Not deleted', () => this.clientOf(node.coreId).deleteArtifact(node.artifact.id));
  }
  async upload() {
    const { core, project } = this.current;
    if (!core || !project) return;
    const files = await vscode.window.showOpenDialog({ canSelectMany: true, canSelectFolders: false, openLabel: 'Upload artifacts' });
    for (const file of files ?? []) {
      await this.act('Not uploaded', async () => core.client.uploadArtifact(project.id, file.path.split('/').pop() ?? 'file', mediaType('', file.path), await vscode.workspace.fs.readFile(file)));
    }
  }
}

function ref(artifact: Artifact) { return { artifactId: artifact.id, filename: artifact.filename, mimeType: artifact.mimeType }; }
