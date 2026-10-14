import { Folder, Globe, Pencil, Trash2 } from 'lucide-react';
import { useCallback, useState } from 'react';

import { useBackends, useBindDirectory, useBindings, useCreateDirectory, useDeleteDirectory, useDirectories, useMakeDirectoryGlobal, useUnbindDirectory, useUpdateDirectory } from '@/api/queries';
import type { KnownDirectory } from '@/api/types';
import { ConfirmLine, NewButton, RecordList, RecordRow } from '@/components/record-list';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/card';
import { Input, Label, Textarea } from '@/components/ui/input';
import { MenuItem } from '@/components/ui/menu';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useShortcut } from '@/use-shortcut';

export function DirectoriesPane({ projectId = '' }: { projectId?: string }) {
  const directories = useDirectories(projectId);
  const [creating, setCreating] = useState(false);
  useShortcut('n', useCallback(() => setCreating(true), []));
  return <div className="space-y-4" data-tour="known-directories">
    <p className="text-muted text-sm">Remember directories and their paths on each backend. Global directories are available to all your projects.</p>
    {!creating && <NewButton label="New directory" onClick={() => setCreating(true)} />}
    {creating && <section className="border-border space-y-3 rounded-lg border p-4"><div className="flex items-center justify-between"><h3 className="font-medium">New known directory</h3><Button variant="ghost" size="lg" onClick={() => setCreating(false)}>Cancel</Button></div><DirectoryCreate projectId={projectId} done={() => setCreating(false)} /></section>}
    <ActionError error={directories.error} outcome="Directories could not be read" recovery="Reload the page." />
    {directories.isPending ? <p className="text-muted text-sm">Reading directories…</p> : !directories.data?.length ? <EmptyState>No known directories yet.</EmptyState> : <RecordList>{directories.data.map((directory) => <DirectoryRow key={directory.id} directory={directory} />)}</RecordList>}
  </div>;
}

function DirectoryCreate({ projectId, done }: { projectId: string; done: () => void }) {
  const [scope, setScope] = useState(projectId ? 'project' : 'global');
  const create = useCreateDirectory(scope === 'global' ? '' : projectId);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [remote, setRemote] = useState('');
  return <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); if (!name.trim() || create.isPending) return; create.mutate({ name: name.trim(), description, gitRemote: remote.trim() || null }, { onSuccess: done }); }}>
    {projectId && <div className="space-y-1.5"><Label htmlFor="directory-scope">Scope</Label><Select value={scope} onValueChange={setScope}><SelectTrigger id="directory-scope"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="project">This project</SelectItem><SelectItem value="global">Global — all my projects</SelectItem></SelectContent></Select></div>}
    <DirectoryFields name={name} onName={setName} description={description} onDescription={setDescription} remote={remote} onRemote={setRemote} prefix="new-directory" />
    <ActionError error={create.error} outcome="Not created" recovery="Your changes are still here; try again." />
    <Button type="submit" variant="primary" disabled={create.isPending || !name.trim()}>Create directory</Button>
  </form>;
}

function DirectoryFields({ name, onName, description, onDescription, remote, onRemote, prefix }: { name: string; onName: (value: string) => void; description: string; onDescription: (value: string) => void; remote: string; onRemote: (value: string) => void; prefix: string }) {
  return <>
    <div className="space-y-1.5"><Label htmlFor={`${prefix}-name`}>Name</Label><Input id={`${prefix}-name`} value={name} onChange={(event) => onName(event.target.value)} required /></div>
    <div className="space-y-1.5"><Label htmlFor={`${prefix}-description`}>Description</Label><Textarea id={`${prefix}-description`} rows={3} value={description} onChange={(event) => onDescription(event.target.value)} /></div>
    <div className="space-y-1.5"><Label htmlFor={`${prefix}-remote`}>Git remote (optional)</Label><Input id={`${prefix}-remote`} value={remote} onChange={(event) => onRemote(event.target.value)} /></div>
  </>;
}

function DirectoryRow({ directory }: { directory: KnownDirectory }) {
  const remove = useDeleteDirectory(directory.id);
  const makeGlobal = useMakeDirectoryGlobal(directory.id);
  const [editing, setEditing] = useState(false);
  const [asking, setAsking] = useState(false);
  return <RecordRow icon={Folder} title={directory.name} body={directory.description} meta={directory.gitRemote} badges={<Badge>{directory.projectId ? 'Project' : 'Global'}</Badge>}
    action={<Button variant="ghost" size="lg" aria-expanded={editing} onClick={() => setEditing(!editing)}>Manage</Button>}
    menuLabel={`Actions for ${directory.name}`} menu={<><MenuItem onSelect={() => setEditing(true)}><Pencil />Edit metadata and paths</MenuItem>{directory.projectId && <MenuItem disabled={makeGlobal.isPending} onSelect={() => makeGlobal.mutate()}><Globe />Make global — all my projects</MenuItem>}<MenuItem className="text-danger" onSelect={() => setAsking(true)}><Trash2 />Forget directory</MenuItem></>}
    below={(editing || asking || Boolean(remove.error) || Boolean(makeGlobal.error)) && <div className="space-y-3">{asking && <ConfirmLine question="Forget this directory and all its paths? Files stay on their machines. Sessions using it must choose another directory first." confirm="Forget" pending={remove.isPending} onCancel={() => setAsking(false)} onConfirm={() => remove.mutate()} />}<ActionError error={remove.error} outcome="Directory not forgotten" recovery="If a session uses it, change its working directory first." /><ActionError error={makeGlobal.error} outcome="Directory not made global" recovery="If a global directory has this name, rename one and try again." />{editing && <DirectoryEditor directory={directory} done={() => setEditing(false)} />}</div>}
  />;
}

function DirectoryEditor({ directory, done }: { directory: KnownDirectory; done: () => void }) {
  const update = useUpdateDirectory(directory.id);
  const [original, setOriginal] = useState(directory);
  const [name, setName] = useState(directory.name);
  const [description, setDescription] = useState(directory.description ?? '');
  const [remote, setRemote] = useState(directory.gitRemote ?? '');
  return <div className="space-y-5">
    <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); if (update.isPending) return; const patch = { ...(name.trim() !== original.name && { name: name.trim() }), ...(description !== (original.description ?? '') && { description }), ...(remote.trim() !== (original.gitRemote ?? '') && { gitRemote: remote.trim() }) }; update.mutate(patch, { onSuccess: (updated) => { setOriginal(updated); setName(updated.name); setDescription(updated.description ?? ''); setRemote(updated.gitRemote ?? ''); } }); }}>
      <fieldset disabled={update.isPending} className="space-y-3"><DirectoryFields name={name} onName={setName} description={description} onDescription={setDescription} remote={remote} onRemote={setRemote} prefix={`directory-${directory.id}`} /></fieldset>
      <ActionError error={update.error} outcome="Not saved" recovery="Your changes are still here; try again." />
      <div className="flex gap-2"><Button type="submit" variant="primary" disabled={update.isPending || !name.trim()}>Save metadata</Button><Button type="button" variant="ghost" onClick={done}>Close</Button></div>
      {update.isSuccess && <p role="status" className="text-ok text-sm">Metadata saved.</p>}
    </form>
    <DirectoryPaths directoryId={directory.id} />
  </div>;
}

function DirectoryPaths({ directoryId }: { directoryId: string }) {
  const bindings = useBindings(directoryId);
  const backends = useBackends();
  const bind = useBindDirectory(directoryId);
  const unbind = useUnbindDirectory(directoryId);
  const [backendId, setBackendId] = useState('');
  const [path, setPath] = useState('');
  const [forget, setForget] = useState('');
  const available = (backends.data ?? []).filter((backend) => backend.ownershipStatus !== 'REVOKED');
  return <div className="space-y-3"><h3 className="text-sm font-medium">Paths on backends</h3>
    <ActionError error={bindings.error ?? backends.error} outcome="Paths could not be read" />
    {(bindings.data ?? []).map((binding) => <div key={binding.backendInstanceId} className="space-y-2"><div className="flex flex-wrap items-center gap-2 text-sm"><span className="min-w-0 flex-1 break-words">{backends.data?.find((backend) => backend.id === binding.backendInstanceId)?.name ?? binding.backendInstanceId} · <code>{binding.path}</code></span><Button size="lg" variant="ghost" onClick={() => { setBackendId(binding.backendInstanceId); setPath(binding.path); }}>Edit path</Button><Button size="lg" variant="ghost" onClick={() => setForget(binding.backendInstanceId)}>Forget path</Button></div>{forget === binding.backendInstanceId && <ConfirmLine question="Forget this path? Files stay on the backend." confirm="Forget path" pending={unbind.isPending} onCancel={() => setForget('')} onConfirm={() => unbind.mutate(binding.backendInstanceId, { onSuccess: () => setForget('') })} />}</div>)}
    <ActionError error={unbind.error} outcome="Path not forgotten" />
    <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); if (!backendId || !path.startsWith('/') || bind.isPending) return; bind.mutate({ backendInstanceId: backendId, path }, { onSuccess: () => setPath('') }); }}>
      <div className="space-y-1.5"><Label htmlFor={`binding-${directoryId}-backend`}>Backend</Label><Select value={backendId} onValueChange={setBackendId}><SelectTrigger id={`binding-${directoryId}-backend`}><SelectValue placeholder="Choose a backend" /></SelectTrigger><SelectContent>{available.map((backend) => <SelectItem key={backend.id} value={backend.id}>{backend.name}</SelectItem>)}</SelectContent></Select></div>
      <div className="space-y-1.5"><Label htmlFor={`binding-${directoryId}-path`}>Absolute path on this backend</Label><Input id={`binding-${directoryId}-path`} value={path} onChange={(event) => setPath(event.target.value)} placeholder="/home/alex/work/docs" required pattern="/.*" /></div>
      <ActionError error={bind.error} outcome="Path not saved" recovery="Check the backend and its absolute path, then try again." />
      <Button type="submit" variant="primary" disabled={bind.isPending || !backendId || !path.startsWith('/')}>Save path</Button>
      {bind.isSuccess && <p role="status" className="text-ok text-sm">Path saved.</p>}
    </form>
  </div>;
}
