import { useCallback, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound } from 'lucide-react';
import { api } from '@/api/client';
import type { components } from '@/api/schema';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { CheckboxField } from '@/components/ui/checkbox';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { EmptyState } from '@/components/ui/card';
import { ConfirmLine, NewButton, RecordList, RecordRow } from '@/components/record-list';
import { MenuItem } from '@/components/ui/menu';
import { useShortcut } from '@/use-shortcut';

type Access = components['schemas']['ControlAccess'];
type Scope = Access['scopes'][number];
const scopes: { value: Scope; label: string }[] = [
  { value: 'read', label: 'Read projects, sessions, events and configuration' },
  { value: 'manage', label: 'Create, change and delete resources; send messages and cancel work' },
  { value: 'answer', label: 'Answer agents’ questions' },
  { value: 'approve', label: 'Accept or refuse permissions on my behalf' },
];

export function ControlAccessPane() {
  const client = useQueryClient();
  const accesses = useQuery({ queryKey: ['control-accesses'], queryFn: () => api.get<{ items: Access[] }>('/api/v1/me/control-accesses') });
  const [creating, setCreating] = useState(false);
  const [label, setLabel] = useState('');
  const [granted, setGranted] = useState<Scope[]>(['read']);
  const [days, setDays] = useState('30');
  const [token, setToken] = useState('');
  const [showToken, setShowToken] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<Error>();
  const [revoking, setRevoking] = useState('');
  useShortcut('n', useCallback(() => { if (!creating && !token) setCreating(true); }, [creating, token]));
  const mutation = useMutation({
    mutationFn: async (action: () => Promise<void>) => { await action(); },
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['control-accesses'] }); },
  });
  function create() {
    mutation.mutate(async () => {
      const expiresAt = days ? new Date(Date.now() + Number(days) * 86400000).toISOString() : undefined;
      const result = await api.post<{ access: Access; token: string }>('/api/v1/me/control-accesses', { label, scopes: granted, ...(expiresAt ? { expiresAt } : {}) });
      // Only local component state, never the query or mutation result cache.
      setToken(result.token); setShowToken(false); setCopied(false); setCopyError(undefined); setCreating(false); setLabel(''); setGranted(['read']);
    });
  }
  return <section data-tour="control-accesses" className="space-y-4 [&_label]:min-h-11">
    <p className="text-muted text-sm">Let another Threavia or MCP client act on your account on this Core. Each connection has its own rights and can be revoked. The MCP endpoint is <code>/mcp</code>.</p>
    <div className="flex justify-end">{!creating && !token && <NewButton label="Create MCP access" onClick={() => setCreating(true)} />}</div>
    <ActionError error={accesses.error || mutation.error || copyError} outcome="MCP access operation could not be completed" />
    {token && <div className="border-border space-y-3 rounded-md border p-4" role="region" aria-label="New MCP access token">
      <h2 className="font-medium">Save this token now</h2>
      <p className="text-muted text-sm">It is shown once. Store it privately on the calling backend, and reference its environment variable in the MCP declaration’s Authorization header.</p>
      <label className="block space-y-2"><span className="text-sm">Access token</span><Input className="min-h-11 font-mono" type={showToken ? 'text' : 'password'} autoComplete="off" readOnly value={token} /></label>
      <div className="flex flex-wrap gap-2">
        <Button size="lg" onClick={() => { if (!navigator.clipboard) { setCopyError(new Error('Clipboard is unavailable. Show the token and copy it manually.')); return; } void navigator.clipboard.writeText(token).then(() => { setCopied(true); setCopyError(undefined); }, () => setCopyError(new Error('Token could not be copied. Show it and copy it manually.'))); }}>{copied ? 'Copied' : 'Copy token'}</Button>
        <Button size="lg" onClick={() => setShowToken(!showToken)}>{showToken ? 'Hide token' : 'Show token'}</Button>
        <Button size="lg" onClick={() => { setToken(''); setShowToken(false); setCopyError(undefined); }}>I saved the token</Button>
      </div>
    </div>}
    {creating && <form className="border-border space-y-4 rounded-md border p-4" onSubmit={e => { e.preventDefault(); create(); }}>
      <label className="block space-y-2"><span className="text-sm font-medium">Connection name</span><Input autoFocus className="min-h-11" required maxLength={120} value={label} onChange={e => setLabel(e.target.value)} placeholder="Main Threavia → development" /></label>
      <fieldset className="space-y-2"><legend className="mb-2 text-sm font-medium">Rights on this Core</legend>{scopes.map(scope => <CheckboxField key={scope.value} checked={granted.includes(scope.value)} onCheckedChange={checked => setGranted(checked ? [...granted, scope.value] : granted.filter(v => v !== scope.value))}>{scope.label}</CheckboxField>)}</fieldset>
      <label className="flex flex-col gap-2 text-sm">Expires after<select className="border-border bg-surface min-h-11 rounded-md border px-3" value={days} onChange={e => setDays(e.target.value)}><option value="1">1 day</option><option value="30">30 days</option><option value="90">90 days</option><option value="">No expiry</option></select></label>
      <p className="text-muted text-sm">Rights cover your resources across all your projects. Permission decisions are attributed to this connection. Grant only the rights this connection needs.</p>
      <div className="flex gap-2"><Button size="lg" type="submit" disabled={mutation.isPending || !granted.length}>Create access</Button><Button size="lg" type="button" disabled={mutation.isPending} onClick={() => setCreating(false)}>Cancel</Button></div>
    </form>}
    {accesses.isPending && <p role="status">Loading MCP accesses…</p>}
    {!accesses.isPending && accesses.data?.items.length === 0 && <EmptyState>No MCP accesses. Create a dedicated access for each calling instance.</EmptyState>}
    {!!accesses.data?.items.length && <RecordList>{accesses.data.items.map(access => {
      const expired = !!access.expiresAt && Date.parse(access.expiresAt) <= Date.now();
      const inactive = !!access.revokedAt || expired;
      return <RecordRow key={access.id} icon={KeyRound} title={access.label} body={access.scopes.join(' · ')} meta={access.expiresAt ? `Expires ${new Date(access.expiresAt).toLocaleString()}` : 'No expiry'} badges={<Badge tone={inactive ? 'neutral' : 'ok'}>{access.revokedAt ? 'Revoked' : expired ? 'Expired' : 'Active'}</Badge>}
        menuLabel={`Actions for ${access.label}`} menu={!access.revokedAt && <MenuItem onSelect={() => setRevoking(access.id)}>Revoke access…</MenuItem>}
        below={<>{access.bootstrap && <p className="text-muted text-sm">Initialized by the environment. Restarting Core does not restore a revoked access.</p>}{revoking === access.id && <ConfirmLine pending={mutation.isPending} question={`Revoke ${access.label}? Further calls will be refused. Jobs it already started keep running.`} confirm="Revoke access" onCancel={() => setRevoking('')} onConfirm={() => mutation.mutate(async () => { await api.delete(`/api/v1/control-accesses/${access.id}`); setRevoking(''); })} />}</>} />;
    })}</RecordList>}
  </section>;
}
