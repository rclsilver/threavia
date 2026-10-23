import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plug } from 'lucide-react';
import { api } from '@/api/client';
import { useBackends, useProjects } from '@/api/queries';
import { rememberMCPAuth, useMCPSignIn } from '@/auth/mcp';
import { mcpConnectionKey, rememberMCPBackend, savedMCPBackend, useMCPConnections } from '@/auth/mcp-connections';
import type { MCPBinding, MCPImport, MCPProbe, MCPRestriction, MCPServer } from '@/api/mcp';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { ActionError } from '@/components/ui/action-error';
import { RecordList, RecordRow, ConfirmLine, NewButton } from '@/components/record-list';
import { MenuItem } from '@/components/ui/menu';
import { EmptyState } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { MCPSignInDialog } from '@/components/mcp-sign-in';

const initial: MCPServer = { name: '', transport: 'http', url: '', tools: [], readTools: [], resources: false, prompts: false, enabled: false, oauth: false };
const control = 'border-border bg-surface text-text min-h-11 rounded-md border px-3 py-2 text-sm';
export function MCPPane() {
  const client = useQueryClient();
  const servers = useQuery({ queryKey: ['mcp', 'servers'], queryFn: () => api.get<{ items: MCPServer[] }>('/api/v1/me/mcp') });
  const projects = useProjects(); const backends = useBackends();
  const signIn = useMCPSignIn();
  const [signInOpen, setSignInOpen] = useState(!!signIn.flow);
  const authTarget = signIn.flow?.pending;
  const authServer = servers.data?.items.find(s => s.id === authTarget?.serverId);
  const authBackend = backends.data?.find(b => b.id === authTarget?.backend);
  const [project, setProject] = useState(''); const [backend, setBackend] = useState(() => authTarget?.backend || savedMCPBackend());
  const bindings = useQuery({ queryKey: ['mcp', 'bindings', project], queryFn: () => api.get<{ items: MCPBinding[] }>(`/api/v1/projects/${project}/mcp`), enabled: !!project });
  const [editing, setEditing] = useState<string | null>(null); const [draft, setDraft] = useState('');
  const [probe, setProbe] = useState<MCPProbe | null>(null); const [preview, setPreview] = useState<MCPImport | null>(null);
  const [deleting, setDeleting] = useState('');
  const [disconnecting, setDisconnecting] = useState('');
  const discoveryRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!probe) return;
    discoveryRef.current?.focus({ preventScroll: true });
    discoveryRef.current?.scrollIntoView?.({ block: 'nearest' });
  }, [probe]);
  const mutation = useMutation({ mutationFn: (action: () => Promise<unknown>) => action(), onSuccess: () => { void client.invalidateQueries({ queryKey: ['mcp'] }); } });
  const run = (action: () => Promise<unknown>) => mutation.mutate(action);
  const selectedBackend = backends.data?.some(b => b.id === backend) ? backend : backends.data?.[0]?.id || '';
  const selectedMachine = backends.data?.find(b => b.id === selectedBackend);
  const backendAvailable = !!selectedMachine && selectedMachine.operationalStatus !== 'OFFLINE' && selectedMachine.operationalStatus !== 'UNAVAILABLE' && selectedMachine.ownershipStatus !== 'REVOKED';
  const connections = useMCPConnections(servers.data?.items || [], selectedBackend, backendAvailable && !signIn.flow);
  useEffect(() => { if (selectedBackend) rememberMCPBackend(selectedBackend); }, [selectedBackend]);
  async function checkConnection(server: MCPServer) {
    const connection = connections.get(server.id || '');
    const result = connection ? (await connection.refetch({ throwOnError: true })).data : await operation<MCPProbe>('probe', server.id);
    if (result) setProbe(result);
  }
  function edit(server: MCPServer) { setEditing(server.id || 'new'); setDraft(JSON.stringify(server, null, 2)); }
  async function operation<T>(operation: string, serverId = '', extra: Record<string, unknown> = {}) {
    if (!selectedBackend) throw new Error('Choose a connected backend first');
    return api.post<T>(`/api/v1/backends/${selectedBackend}/mcp`, { operation, serverId, ...extra });
  }
  function bind(serverId: string, enabled: boolean, required: boolean) { run(() => api.put(`/api/v1/projects/${project}/mcp`, { serverId, enabled, required })); }
  async function startAuth(serverId: string, targetBackend = selectedBackend) {
    if (!targetBackend) throw new Error('Choose a connected backend first');
    const result = await api.post<{ state: string; authorizationUrl: string }>(`/api/v1/backends/${targetBackend}/mcp`, { operation: 'auth_start', serverId, redirectUri: window.location.origin + '/settings/mcp' });
    const parsed = new URL(result.authorizationUrl); if (parsed.protocol !== 'https:' && !(parsed.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(parsed.hostname))) throw new Error('Unsafe sign-in URL refused');
    signIn.reset(); setSignInOpen(false);
    rememberMCPAuth(targetBackend, serverId, result.state);
    window.location.assign(result.authorizationUrl);
  }
  return <section data-tour="mcp" className="space-y-4 [&_label]:min-h-11">
    <p className="text-muted text-sm">Connect existing MCP servers. Credentials stay on each backend. Changes apply to the next Job. Adding a declaration never installs or starts software.</p>
    <div className="flex flex-wrap gap-3">
      <label className="flex flex-col gap-1 text-sm">Execution backend<select className={control} value={selectedBackend} onChange={e => { setBackend(e.target.value); setProbe(null); }}><option value="">Choose backend</option>{backends.data?.map(b => <option key={b.id} value={b.id}>{b.name} · {b.operationalStatus}</option>)}</select></label>
      <label className="flex flex-col gap-1 text-sm">Project activation<select className={control} value={project} onChange={e => setProject(e.target.value)}><option value="">Account registry</option>{projects.data?.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label>
    </div>
    <div className="flex flex-wrap gap-2"><NewButton label="Add MCP server" onClick={() => edit(initial)} /><Button size="lg" disabled={mutation.isPending || !selectedBackend} onClick={() => run(async () => { setPreview(await operation<MCPImport>('import', '', { source: 'codex' })); })}>Import from Codex…</Button><Button size="lg" disabled={mutation.isPending || !selectedBackend} onClick={() => run(async () => { setPreview(await operation<MCPImport>('import', '', { source: 'claude' })); })}>Import from Claude…</Button></div>
    <MCPSignInDialog signIn={signIn} serverName={authServer?.name || 'MCP server'} backendName={authBackend?.name || 'the sign-in backend'} open={signInOpen} onOpenChange={setSignInOpen}
      starting={mutation.isPending}
      actionError={mutation.error}
      onConnect={authServer && authTarget ? () => run(() => startAuth(authTarget.serverId, authTarget.backend)) : undefined}
      onReview={() => { if (authTarget && signIn.discovery) { setBackend(authTarget.backend); setProbe(signIn.discovery); setSignInOpen(false); } }} />
    <ActionError error={servers.error || bindings.error || mutation.error} outcome="MCP operation could not be completed" />
    {servers.isPending && <p role="status">Loading MCP servers…</p>}
    {preview && <div className="border-border space-y-3 rounded-md border p-3"><h3 className="font-medium">Import preview — nothing added yet</h3>{preview.servers.length === 0 && <p>No MCP declarations found.</p>}{preview.servers.map((p, index) => <div key={index}><p>{p.declaration.name}</p><ul className="text-muted text-sm">{p.notes.map(n => <li key={n}>{n}</li>)}</ul><Button size="lg" onClick={() => { edit(p.declaration); setPreview(null); }}>Review declaration</Button></div>)}<Button size="lg" onClick={() => setPreview(null)}>Close preview</Button></div>}
    {editing !== null && <form className="border-border space-y-3 rounded-md border p-3" onSubmit={e => { e.preventDefault(); run(async () => { const value = JSON.parse(draft) as MCPServer; if (editing === 'new') await api.post('/api/v1/me/mcp', value); else await api.put(`/api/v1/mcp/${editing}`, value); setEditing(null); }); }}>
      <label className="block space-y-2"><span className="font-medium">{editing === 'new' ? 'New server declaration' : 'Edit server declaration'}</span><textarea aria-label="MCP server declaration JSON" className={control + ' min-h-80 w-full font-mono'} value={draft} onChange={e => setDraft(e.target.value)} spellCheck={false} /></label>
      <p className="text-muted text-sm">Use stdio with command/args, or http with url. env and headers map names to backend environment variable names, never values. Never put secrets in arguments or URLs. tools selects exact tool names; readTools explicitly identifies selected read-only tools. Unknown tools require approval.</p>
      <div className="flex gap-2"><Button size="lg" type="submit" disabled={mutation.isPending}>Save declaration</Button><Button size="lg" type="button" onClick={() => setEditing(null)}>Cancel</Button></div>
    </form>}
    {!servers.isPending && servers.data?.items.length === 0 && <EmptyState>No MCP servers. Add an existing server or preview declarations from a backend.</EmptyState>}
    {!!servers.data?.items.length && <RecordList>{servers.data.items.map(server => {
      const id = server.id || ''; const binding = bindings.data?.items.find(b => b.serverId === id);
      const thisSignIn = !!signIn.flow && authTarget?.serverId === id && authTarget.backend === selectedBackend;
      const authBusy = thisSignIn && (signIn.finishing || signIn.checking);
      const connection = connections.get(id);
      const checking = authBusy || !!connection?.isFetching;
      const discovered = backendAvailable && !connection?.isError ? connection?.data || (thisSignIn ? signIn.discovery : undefined) : undefined;
      const connected = !!discovered && !checking;
      const accountConnected = backendAvailable && thisSignIn && signIn.connected && !connection?.isError;
      const state = checking ? 'Checking connection…' : !selectedBackend ? 'Choose a backend' : !backendAvailable ? 'Backend unavailable' : connected ? 'Connected' : connection?.isError ? 'Connection unverified' : accountConnected ? 'Account connected' : thisSignIn ? 'Sign-in needs attention' : 'Connection not checked';
      const activation = project ? (binding?.enabled ? (binding.required ? 'Required in this project' : 'Optional in this project') : 'Not active in this project') : 'Account declaration';
      return <RecordRow key={id} menuLabel={`Actions for ${server.name}`} icon={Plug} title={server.name} body={`${server.transport} · ${server.enabled ? 'Enabled' : 'Disabled'} · ${server.tools.length} selected tools`} meta={<>{activation}{server.oauth && <span className="mt-1 block">{selectedMachine ? `Connection on ${selectedMachine.name}` : 'Choose an execution backend to check this account'}{connected && ` · ${discovered.tools.length} tools available`}</span>}</>}
        badges={server.oauth && <Badge role="status" tone={connected ? 'ok' : connection?.isError ? 'warn' : 'neutral'}>{state}</Badge>}
        action={server.oauth ? <Button variant={connected ? 'secondary' : 'primary'} size="lg" disabled={mutation.isPending || !backendAvailable || checking} onClick={() => connected ? setProbe(discovered) : run(() => startAuth(id))}>{checking ? 'Checking…' : connected ? 'View tools' : 'Connect account…'}</Button> : <Button size="lg" disabled={mutation.isPending || !selectedBackend} onClick={() => run(() => checkConnection(server))}>Test connection</Button>}
        menu={<>{thisSignIn && <MenuItem onSelect={() => setSignInOpen(true)}>Connection status</MenuItem>}{server.oauth && <><MenuItem disabled={checking || !backendAvailable} onSelect={() => run(() => checkConnection(server))}>Check connection again</MenuItem><MenuItem disabled={checking || !backendAvailable} onSelect={() => run(() => startAuth(id))}>{connected || accountConnected ? 'Reconnect account…' : 'Connect account…'}</MenuItem></>}<MenuItem onSelect={() => edit(server)}>Edit declaration</MenuItem>{server.oauth && <MenuItem disabled={checking || !backendAvailable} onSelect={() => setDisconnecting(id)}>Disconnect account on this backend…</MenuItem>}<MenuItem onSelect={() => setDeleting(id)}>Remove declaration…</MenuItem></>}
        below={<>{server.oauth && backendAvailable && connection?.isError && <div className="space-y-2"><ActionError error={connection.error} outcome={`Connection could not be verified on ${selectedMachine.name}`} recovery="Check again, or connect your account if sign-in is needed." /><Button size="lg" disabled={mutation.isPending || checking} onClick={() => run(() => checkConnection(server))}>Check again</Button></div>}{disconnecting === id && <ConfirmLine pending={mutation.isPending} question="Disconnect this account now on the selected backend? Active Jobs may lose access; they will not switch to another account." confirm="Disconnect account" onConfirm={() => run(async () => { await operation('logout', id); setDisconnecting(''); setProbe(null); if (thisSignIn) { signIn.reset(); setSignInOpen(false); } await client.resetQueries({ queryKey: mcpConnectionKey(selectedBackend, server), exact: true }); })} onCancel={() => setDisconnecting('')} />}{project && <div className="flex flex-wrap gap-4"><CheckboxField disabled={mutation.isPending} checked={binding?.enabled ?? false} onCheckedChange={enabled => bind(id, enabled, binding?.required ?? false)} children="Active in this project" /><CheckboxField disabled={mutation.isPending} checked={binding?.required ?? false} onCheckedChange={required => bind(id, binding?.enabled ?? false, required)} children="Required for startup" /></div>}{deleting === id && <ConfirmLine pending={mutation.isPending} question={`Remove ${server.name} and its activations in ${(server.projectIds || []).map(id => projects.data?.find(p => p.id === id)?.name || id).join(", ") || "no projects"}? Existing Jobs keep their snapshot; software and credentials stay on backends.`} confirm="Remove declaration" onConfirm={() => run(async () => { await api.delete(`/api/v1/mcp/${id}`); setDeleting(''); })} onCancel={() => setDeleting('')} />}</>} />;
    })}</RecordList>}
    {probe && <div ref={discoveryRef} tabIndex={-1} role="region" aria-label="Discovered MCP tools" className="border-border space-y-3 rounded-md border p-3"><h3 className="font-medium">Connected on selected backend</h3><p className="text-muted text-sm">Live discovery only. Newly discovered tools are not enabled automatically. Mark read-only only when you trust the tool’s behavior.</p>{probe.tools.map(t => { const server = servers.data?.items.find(s => s.id === probe.serverId); return <div key={t.name}><p className="font-mono text-sm">{t.name}</p><p className="text-muted text-sm">{t.description}</p>{server && <div className="flex flex-wrap gap-4"><CheckboxField disabled={mutation.isPending} children="Expose this tool" checked={server.tools.includes(t.name)} onCheckedChange={selected => run(() => api.put(`/api/v1/mcp/${server.id}`, { ...server, tools: selected ? [...server.tools, t.name] : server.tools.filter(n => n !== t.name), readTools: selected ? server.readTools : server.readTools.filter(n => n !== t.name) }))} /><CheckboxField children="Explicitly classify as read-only" checked={server.readTools.includes(t.name)} disabled={mutation.isPending || !server.tools.includes(t.name)} onCheckedChange={read => run(() => api.put(`/api/v1/mcp/${server.id}`, { ...server, readTools: read ? [...server.readTools, t.name] : server.readTools.filter(n => n !== t.name) }))} /></div>}</div>})}<Button size="lg" onClick={() => setProbe(null)}>Close discovery</Button></div>}
  </section>;
}

export function SessionMCPPanel({ sessionId, projectId }: { sessionId: string; projectId?: string }) {
  const client = useQueryClient();
  const servers = useQuery({ queryKey: ['mcp', 'servers'], queryFn: () => api.get<{ items: MCPServer[] }>('/api/v1/me/mcp') });
  const bindings = useQuery({ queryKey: ['mcp', 'bindings', projectId], queryFn: () => api.get<{ items: MCPBinding[] }>(`/api/v1/projects/${projectId}/mcp`), enabled: !!projectId });
  const restriction = useQuery({ queryKey: ['mcp', 'session', sessionId], queryFn: () => api.get<MCPRestriction>(`/api/v1/sessions/${sessionId}/mcp`) });
  const update = useMutation({ mutationFn: (serverIds: string[] | null) => api.put(`/api/v1/sessions/${sessionId}/mcp`, { serverIds }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['mcp', 'session', sessionId] }); } });
  const enabled = (bindings.data?.items ?? []).filter(b => b.enabled).map(b => b.serverId);
  const selected = restriction.data?.restricted ? restriction.data.serverIds || [] : enabled;
  return <section className="space-y-3"><p className="text-muted text-sm">Restrict this session to project-enabled servers. Changes apply to the next Job.</p><ActionError error={servers.error || bindings.error || restriction.error || update.error} />{servers.data?.items.filter(s => s.id && enabled.includes(s.id)).map(s => <CheckboxField key={s.id} children={s.name} checked={selected.includes(s.id || '')} disabled={update.isPending} onCheckedChange={checked => update.mutate(checked ? [...selected, s.id || ''] : selected.filter(id => id !== s.id))} />)}{enabled.length === 0 && <p>No MCP servers are active in this project.</p>}<Button size="lg" disabled={update.isPending} onClick={() => update.mutate(null)}>Use project selection</Button></section>;
}
