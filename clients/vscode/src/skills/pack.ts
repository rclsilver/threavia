import * as vscode from 'vscode';
import { gzipSync } from 'node:zlib';

/** One file of a Skill folder, its path relative to the folder with `/` separators. */
export interface SkillFolderFile {
  path: string;
  content: Uint8Array;
}

/**
 * The regular files under a folder, without `.git`: what installing the same
 * folder from git would hold. Links are left out, as Core would leave them.
 * Paths start with the folder's own name, as in a tar of the folder, so that
 * Core names a skill whose SKILL.md gives no name after the folder.
 */
export async function readFolder(root: vscode.Uri): Promise<SkillFolderFile[]> {
  const top = root.path.split('/').pop() || 'skill';
  const files: SkillFolderFile[] = [];
  const walk = async (relative: string) => {
    for (const [name, type] of await vscode.workspace.fs.readDirectory(relative ? vscode.Uri.joinPath(root, relative) : root)) {
      const path = relative ? `${relative}/${name}` : name;
      if (type === vscode.FileType.Directory && name !== '.git') await walk(path);
      else if (type === vscode.FileType.File) files.push({ path: `${top}/${path}`, content: await vscode.workspace.fs.readFile(vscode.Uri.joinPath(root, path)) });
    }
  };
  await walk('');
  return files;
}

/**
 * Files as the gzipped tar Core installs an upload from. Core keeps the
 * regular files only and repacks them itself, so nothing else is written: no
 * directory entries, owners or times.
 */
export function tarGz(files: SkillFolderFile[]): Uint8Array {
  const blocks: Uint8Array[] = [];
  for (const file of files) {
    blocks.push(header(file.path, file.content.length), file.content, new Uint8Array((512 - (file.content.length % 512)) % 512));
  }
  // Two empty blocks end the archive.
  blocks.push(new Uint8Array(1024));
  return gzipSync(Buffer.concat(blocks));
}

const encoder = new TextEncoder();

/** A ustar header: a long path is split at a `/` between its prefix and name fields. */
function header(path: string, size: number): Uint8Array {
  const block = new Uint8Array(512);
  const write = (offset: number, value: string) => block.set(encoder.encode(value), offset);
  const octal = (offset: number, length: number, value: number) => write(offset, value.toString(8).padStart(length - 1, '0'));
  const [prefix, name] = split(path);
  write(0, name);
  octal(100, 8, 0o644);
  octal(108, 8, 0);
  octal(116, 8, 0);
  octal(124, 12, size);
  octal(136, 12, 0);
  write(156, '0');
  write(257, 'ustar\0');
  write(263, '00');
  write(345, prefix);
  // The checksum is summed with its own field as spaces.
  block.fill(0x20, 148, 156);
  write(148, `${block.reduce((sum, byte) => sum + byte, 0).toString(8).padStart(6, '0')}\0 `);
  return block;
}

function split(path: string): [string, string] {
  const length = (value: string) => encoder.encode(value).length;
  if (length(path) <= 100) return ['', path];
  for (let slash = path.indexOf('/'); slash !== -1; slash = path.indexOf('/', slash + 1)) {
    const prefix = path.slice(0, slash);
    const name = path.slice(slash + 1);
    if (length(prefix) <= 155 && name && length(name) <= 100) return [prefix, name];
  }
  throw new Error(`${path}: the path is too long to install from a folder.`);
}
