import { useState } from 'react';
import { useBackends, useSessionModel, useSnapshot } from '@/api/queries';
import type { BackendInstance, Session } from '@/api/types';
import { ActionError } from './ui/action-error';
import { Button } from './ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/select';

export function SessionModelPanel({ sessionId }: { sessionId: string }) {
  const snapshot = useSnapshot(sessionId);
  const backends = useBackends();
  if (snapshot.error || backends.error) return <ActionError error={snapshot.error || backends.error} outcome="Model settings unavailable" recovery="Reload to try again." />;
  if (!snapshot.data || !backends.data) return <p className="text-muted text-sm">Loading model settings…</p>;
  const data = snapshot.data;
  const latest = [...data.runs].sort((a, b) => a.createdAt.localeCompare(b.createdAt)).at(-1);
  const backend = backends.data.find((entry) => entry.id === latest?.backendInstanceId);
  if (!backend?.features?.includes('MODEL_SELECTION')) return <p className="text-muted text-sm">This backend has not reported support for model selection. Update or reconnect it to make this setting available.</p>;
  const running = data.jobs.find((job) => !['QUEUED', 'COMPLETED', 'FAILED', 'CANCELLED'].includes(job.status));
  return <div className="space-y-4" data-tour="model-settings">
    {running && <p className="text-muted text-sm" role="status">Current job: {running.model || 'Model not reported'} · {running.effort ? `${running.effort} effort` : 'Effort not reported'}. Changes apply to the next job.</p>}
    <ModelForm key={`${backend.id}:${data.session.model}:${data.session.effort}`} session={data.session} backend={backend} />
  </div>;
}

function ModelForm({ session, backend }: { session: Session; backend: BackendInstance }) {
  const [model, setModel] = useState(session.model || '');
  const [effort, setEffort] = useState(session.effort || '');
  const save = useSessionModel(session.id);
  const catalog = backend.modelCatalog;
  const models = catalog?.models ?? [];
  const selected = models.find((option) => option.id === (model || catalog?.defaultModel));
  const efforts = selected?.efforts ?? [];
  const changed = model !== (session.model || '') || effort !== (session.effort || '');
  return <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); save.mutate({ model, effort }); }}>
    <div className="space-y-2">
      <label htmlFor="session-model" className="text-sm font-medium">Model</label>
      <Select value={model || 'backend-default'} onValueChange={(value) => { setModel(value === 'backend-default' ? '' : value); setEffort(''); }} disabled={save.isPending}>
        <SelectTrigger id="session-model" className="h-11 w-full"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="backend-default">Backend default{catalog?.defaultModel ? ` · ${catalog.defaultModel}` : ''}</SelectItem>
          {model && !models.some((option) => option.id === model) && <SelectItem value={model} disabled>{model} (unavailable)</SelectItem>}
          {models.map((option) => <SelectItem key={option.id} value={option.id}>{option.name}</SelectItem>)}
        </SelectContent>
      </Select>
    </div>
    <div className="space-y-2">
      <label htmlFor="session-effort" className="text-sm font-medium">Reasoning effort</label>
      <Select value={effort || 'backend-default'} onValueChange={(value) => setEffort(value === 'backend-default' ? '' : value)} disabled={save.isPending || efforts.length === 0}>
        <SelectTrigger id="session-effort" className="h-11 w-full"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="backend-default">Backend default{selected?.defaultEffort ? ` · ${selected.defaultEffort}` : ''}</SelectItem>
          {effort && !efforts.includes(effort) && <SelectItem value={effort} disabled>{effort} (unavailable)</SelectItem>}
          {efforts.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}
        </SelectContent>
      </Select>
      {efforts.length === 0 && <p className="text-muted text-xs">{selected ? 'This model does not offer configurable effort.' : 'Choose a model to see its supported efforts.'}</p>}
    </div>
    <p className="text-muted text-sm">Saved for this session across devices. Every new job uses these settings; work already running keeps its current model and effort.</p>
    <Button type="submit" size="lg" disabled={!changed || save.isPending}>{save.isPending ? 'Saving…' : 'Save model & effort'}</Button>
    <ActionError error={save.error} outcome="Model settings not saved" recovery="Choose a supported model and effort, then try again." />
  </form>;
}
