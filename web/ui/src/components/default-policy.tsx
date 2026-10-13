import { useEffect, useState } from 'react';

import { useDefaultPolicy, useSetDefaultPolicy } from '@/api/queries';
import type { ExecutionPolicy } from '@/api/types';
import { PolicyForm, RulesEditor } from '@/components/policy-form';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';

export function DefaultPolicyPane({ backendId }: { backendId?: string }) {
  const { data, error } = useDefaultPolicy(backendId);
  const save = useSetDefaultPolicy(backendId);
  const [draft, setDraft] = useState<ExecutionPolicy | null>(null);
  const [overriding, setOverriding] = useState(false);
  const [saved, setSaved] = useState(false);
  const editing = Boolean(data && (!backendId || !data.inherited || overriding));
  useEffect(() => {
    if (data) setDraft(data.own ?? { ...data.effective, rules: [] });
  }, [data]);
  const dirty = Boolean(data && draft && (overriding || JSON.stringify(draft) !== JSON.stringify(data.own ?? { ...data.effective, rules: [] })));
  useEffect(() => {
    if (!dirty) return;
    const onLeave = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', onLeave);
    return () => window.removeEventListener('beforeunload', onLeave);
  }, [dirty]);
  if (error) return <ActionError error={error} outcome="The permissions could not be read" recovery="Reload the page." />;
  if (!data || !draft) return <p className="text-muted text-sm">Reading the permissions…</p>;
  const reset = () => save.mutate(null, { onSuccess: () => { setOverriding(false); setSaved(true); } });
  return (
    <div className="space-y-5" data-tour={backendId ? 'backend-policy' : 'instance-policy'}>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-muted">{data.inherited ? (backendId ? 'Following the instance defaults.' : 'Using the built-in defaults.') : (backendId ? 'Set for this backend.' : 'Set for your instance.')}</span>
        {backendId && !editing && <Button size="sm" onClick={() => setOverriding(true)}>Override for this backend</Button>}
        {!data.inherited && <Button size="sm" variant="ghost" disabled={save.isPending} onClick={reset}>{backendId ? 'Follow the instance again' : 'Restore built-in defaults'}</Button>}
      </div>
      <PolicyForm value={editing ? draft : data.effective} onChange={(next) => { setDraft(next); setSaved(false); }} idPrefix={`default-policy-${backendId ?? 'instance'}`} disabled={!editing} />
      <RulesEditor rules={(editing ? draft.rules : data.effective.rules) ?? []} onChange={(rules) => { setDraft({ ...draft, rules }); setSaved(false); }} disabled={!editing} note="Explicit refusals also apply to every level below this one." />
      {editing && backendId && (data.parent.rules?.length ?? 0) > 0 && <RulesEditor rules={data.parent.rules ?? []} onChange={() => {}} disabled note="Inherited from the instance; edit them in Settings → Permissions." />}
      <p className="text-muted text-xs">Permissions follow instance → backend → project → session → job. A provider may apply changes only from the next message.</p>
      <ActionError error={save.error} outcome="Not saved" recovery="Your changes are still here; save again." />
      {editing && dirty && <div className="bg-surface border-border sticky bottom-3 flex gap-2 rounded-xl border p-3 shadow-lg">
        <Button variant="ghost" size="lg" onClick={() => { setDraft(data.own ?? { ...data.effective, rules: [] }); setOverriding(false); }}>Discard</Button>
        <Button variant="primary" size="lg" disabled={save.isPending} onClick={() => save.mutate(draft, { onSuccess: () => { setOverriding(false); setSaved(true); } })}>Save permissions</Button>
      </div>}
      {saved && <p role="status" className="text-ok text-sm">Permissions saved.</p>}
    </div>
  );
}
