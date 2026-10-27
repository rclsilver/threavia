import { gunzipSync } from 'node:zlib';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const host = vi.hoisted(() => ({
  pick: vi.fn(), input: vi.fn(), open: vi.fn(), info: vi.fn(), warn: vi.fn(),
  tree: new Map<string, Uint8Array | null>(),
}));
vi.mock('vscode', () => ({
  FileType: { File: 1, Directory: 2, SymbolicLink: 64 },
  ProgressLocation: { Notification: 15 },
  Uri: { joinPath: (base: { path: string }, ...parts: string[]) => ({ path: [base.path, ...parts].join('/') }) },
  window: {
    showQuickPick: host.pick, showInputBox: host.input, showOpenDialog: host.open,
    showInformationMessage: host.info, showWarningMessage: host.warn,
    withProgress: (_options: unknown, task: () => unknown) => task(),
  },
  workspace: {
    workspaceFolders: undefined,
    fs: {
      // A folder is every entry of `tree` below it; null marks a directory.
      readDirectory: ({ path }: { path: string }) => Promise.resolve([...host.tree].flatMap(([entry, content]) => {
        const rest = entry.startsWith(`${path}/`) ? entry.slice(path.length + 1) : '';
        if (!rest || rest.includes('/')) return [];
        return [[rest, content === null ? 2 : content.length === 0 ? 65 : 1]];
      })),
      readFile: ({ path }: { path: string }) => Promise.resolve(host.tree.get(path)!),
    },
  },
}));

import { CoreClient } from '../src/api/client';
import type { Project, Skill } from '../src/api/types';
import type { Core } from '../src/cores/core';
import type { VirtualDocuments } from '../src/diff/documents';
import { manageSkills } from '../src/skills/manage';
import { tarGz } from '../src/skills/pack';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoRequest } from '../../../web/ui/src/demo/server';

/** The regular files of a gzipped tar, read as Core's archive/tar reads them. */
function untar(archive: Uint8Array): Map<string, string> {
  const tar = gunzipSync(archive);
  const text = (from: number, to: number) => new TextDecoder().decode(tar.subarray(from, to)).replace(/\0.*$/s, '');
  const files = new Map<string, string>();
  for (let offset = 0; offset + 512 <= tar.length && tar[offset] !== 0;) {
    const block = tar.subarray(offset, offset + 512);
    const sum = block.reduce((total, byte, index) => total + (index >= 148 && index < 156 ? 0x20 : byte), 0);
    expect(parseInt(text(offset + 148, offset + 156), 8)).toBe(sum);
    const prefix = text(offset + 345, offset + 500);
    const name = (prefix ? `${prefix}/` : '') + text(offset, offset + 100);
    const size = parseInt(text(offset + 124, offset + 136), 8);
    if (text(offset + 156, offset + 157) === '0') files.set(name, new TextDecoder().decode(tar.subarray(offset + 512, offset + 512 + size)));
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return files;
}

function demoClient() {
  return new CoreClient({
    baseUrl: () => 'https://core.example', authorization: () => Promise.resolve(undefined),
    identity: { clientId: 'editor', clientName: () => 'VS Code' },
    fetch: async (input, init) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url);
      const result = await demoRequest(init?.method ?? 'GET', url.pathname + url.search, typeof init?.body === 'string' ? JSON.parse(init.body) : null);
      return result === undefined ? new Response(null, { status: 204 }) : Response.json(result);
    },
  });
}

describe('skills from VS Code', () => {
  it('turns a global skill off in one project only, and moves a project skill to every project', async () => {
    const client = demoClient();
    const shared = (await client.skills('')).find((skill) => skill.name === 'commit-messages')!;
    await client.setSkillActivation(IDS.homelab, shared.id, false);
    const off = (await client.skills('')).find((skill) => skill.id === shared.id)!;
    // The website had already turned it off; turning it off here must not turn it back on there.
    expect(off.disabledProjectIds).toEqual(expect.arrayContaining([IDS.homelab, IDS.website]));
    await client.setSkillActivation(IDS.homelab, shared.id, true);
    expect((await client.skills('')).find((skill) => skill.id === shared.id)!.disabledProjectIds).toEqual([IDS.website]);

    const local = (await client.skills(IDS.homelab)).find((skill) => skill.name === 'helm-pinning')!;
    expect((await client.skillFiles(local.id)).map((file) => file.path)).toContain('SKILL.md');
    const moved = await client.makeSkillGlobal(local.id);
    expect(moved).toMatchObject({ id: local.id, name: 'helm-pinning' });
    expect(moved.projectId).toBeUndefined();
    expect((await client.skills(IDS.homelab)).some((skill) => skill.id === local.id)).toBe(false);
    await client.uninstallSkill(local.id);
    expect((await client.skills('')).some((skill) => skill.id === local.id)).toBe(false);
  });

  it('sends an upload as raw bytes, which Core reads as an archive rather than a source description', async () => {
    const sent: { url: URL; init?: RequestInit }[] = [];
    const client = new CoreClient({
      baseUrl: () => 'https://core.example', authorization: () => Promise.resolve(undefined),
      identity: { clientId: 'editor', clientName: () => 'VS Code' },
      fetch: (input, init) => {
        sent.push({ url: new URL(input as string), init });
        return Promise.resolve(Response.json({ id: 's1', name: 'my-skill' }, { status: 201 }));
      },
    });
    const archive = new Uint8Array([0x1f, 0x8b, 1, 2]);
    await client.uploadSkill('', 'my-skill.tar.gz', archive, 'skills/mine');
    await client.uploadSkill('p1', 'other.zip', archive);
    expect(sent[0].url.pathname).toBe('/api/v1/me/skills');
    expect(Object.fromEntries(sent[0].url.searchParams)).toEqual({ filename: 'my-skill.tar.gz', path: 'skills/mine' });
    expect(new Headers(sent[0].init?.headers).get('Content-Type')).not.toMatch(/json/);
    expect(new Uint8Array(await (sent[0].init?.body as Blob).arrayBuffer())).toEqual(archive);
    expect(sent[1].url.pathname).toBe('/api/v1/projects/p1/skills');
  });

  it('packs paths too long for a ustar name through its prefix field', () => {
    const long = `${'deep/'.repeat(30)}SKILL.md`;
    const files = untar(tarGz([{ path: long, content: new TextEncoder().encode('# deep') }, { path: 'a/b.txt', content: new Uint8Array(513).fill(65) }]));
    expect(files.get(long)).toBe('# deep');
    expect(files.get('a/b.txt')).toBe('A'.repeat(513));
    expect(() => tarGz([{ path: 'x'.repeat(101), content: new Uint8Array() }])).toThrow(/too long/);
  });
});

describe('Manage Skills', () => {
  const global: Skill = { id: 'g1', ownerId: 'u', name: 'commit-messages', source: { type: 'GIT', url: 'https://git.example/skills' }, installedRevision: 'abcdef0123456789', installedAt: '', artifactId: 'a1', bundleSha256: '', createdAt: '', updatedAt: '', disabledProjectIds: [] };
  const local: Skill = { ...global, id: 'l1', projectId: 'p1', name: 'helm-pinning' };
  let client: Record<string, ReturnType<typeof vi.fn>>;
  let core: Core;
  const project = { id: 'p1', name: 'homelab' } as Project;

  beforeEach(() => {
    vi.resetAllMocks();
    host.tree.clear();
    client = {
      skills: vi.fn((projectId: string) => Promise.resolve(projectId ? [local] : [global])),
      projects: vi.fn().mockResolvedValue([project]),
      setSkillActivation: vi.fn(), makeSkillGlobal: vi.fn(), uninstallSkill: vi.fn(),
      uploadSkill: vi.fn().mockResolvedValue({ ...global, name: 'my-skill' }),
    };
    core = { id: 'c1', ready: true, client } as unknown as Core;
  });

  it('offers a project to turn a global skill off for itself, and a project skill to become global instead', async () => {
    const offered: string[][] = [];
    host.pick
      .mockResolvedValueOnce('Project')
      .mockImplementationOnce((items: { skill?: Skill }[]) => Promise.resolve(items.find((item) => item.skill?.id === 'g1')))
      .mockImplementationOnce((items: string[]) => { offered.push(items); return Promise.resolve('Turn off in this project'); })
      .mockImplementationOnce((items: { skill?: Skill }[]) => Promise.resolve(items.find((item) => item.skill?.id === 'l1')))
      .mockImplementationOnce((items: string[]) => { offered.push(items); return Promise.resolve(undefined); });
    await manageSkills(core, {} as VirtualDocuments, project);
    expect(client.setSkillActivation).toHaveBeenCalledExactlyOnceWith('p1', 'g1', false);
    expect(offered[0]).toEqual(['View files', 'Turn off in this project', 'Uninstall']);
    expect(offered[1]).toEqual(['View files', 'Make global — all my projects', 'Uninstall']);
  });

  it('installs a folder from a project into the global skills, without its .git and under its own name', async () => {
    const text = (value: string) => new TextEncoder().encode(value);
    host.tree.set('/work/my-skill/SKILL.md', text('---\ndescription: Mine\n---\n'));
    host.tree.set('/work/my-skill/scripts', null);
    host.tree.set('/work/my-skill/scripts/run.sh', text('echo run'));
    host.tree.set('/work/my-skill/.git', null);
    host.tree.set('/work/my-skill/.git/HEAD', text('ref: refs/heads/main'));
    host.tree.set('/work/my-skill/link', new Uint8Array());
    host.pick
      .mockResolvedValueOnce('Project')
      .mockImplementationOnce((items: { skill?: Skill }[]) => Promise.resolve(items[0]))
      .mockResolvedValueOnce('Global — all my projects')
      .mockResolvedValueOnce('Local folder')
      .mockResolvedValueOnce(undefined);
    host.open.mockResolvedValueOnce([{ path: '/work/my-skill' }]);
    await manageSkills(core, {} as VirtualDocuments, project);
    expect(client.uploadSkill).toHaveBeenCalledOnce();
    const [projectId, filename, archive] = client.uploadSkill.mock.calls[0] as [string, string, Uint8Array];
    expect(projectId).toBe('');
    expect(filename).toBe('my-skill.tar.gz');
    expect([...untar(archive).keys()].sort()).toEqual(['my-skill/SKILL.md', 'my-skill/scripts/run.sh']);
    expect(host.info).toHaveBeenCalledWith(expect.stringContaining('next Job'));
  });
});
