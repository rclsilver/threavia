import { Radio } from 'lucide-react';
import { useMonitors, useStopMonitor, monitorActive } from '@/api/monitors';
import type { Session } from '@/api/types';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';

export function MonitorBadge({ session }: { session: Session }) {
 const list = useMonitors('projects', session.projectId);
 const active = list.data?.filter((m) => m.sessionId === session.id && monitorActive(m)) ?? [];
 if (!active.length) return null;
 const confirmed = active.every((m) => m.confirmed);
 return <span className={confirmed ? 'text-accent inline-flex items-center gap-1 text-xs' : 'text-muted inline-flex items-center gap-1 text-xs'} title={confirmed ? `${active.length} background monitors active` : 'Background monitoring is not confirmed'}>
  <Radio className="size-3.5" aria-hidden /> <span className="sr-only">{confirmed ? 'Active monitors' : 'Unconfirmed monitors'}</span>{active.length}{!confirmed && '?'}
 </span>;
}
export function MonitorsPanel({ sessionId, working }: { sessionId: string; working: boolean }) {
 const list = useMonitors('sessions', sessionId);
 const stop = useStopMonitor();
 if (list.error) return <ActionError error={list.error} outcome="Background monitoring could not be checked" className="px-3 py-1 text-xs" />;
 if (!list.data?.length) return null;
 const active = list.data.filter(monitorActive);
 const confirmed = active.every((m) => m.confirmed);
 return <details data-tour="monitors" className="border-border max-h-[35vh] shrink-0 overflow-y-auto border-b px-3 py-2 text-sm sm:px-6">
  <summary className="min-h-11 cursor-pointer content-center md:min-h-6">
   {active.length ? `${working ? 'Background monitoring' : 'Waiting'} · ${active.length} ${confirmed ? 'active' : 'unconfirmed'} monitor${active.length > 1 ? 's' : ''}` : 'Background monitoring · recent results'}
  </summary>
  <ActionError error={stop.error} />
  <ul className="mt-2 space-y-3">{list.data.map((m) => <li key={m.id} className="border-border rounded-md border p-2">
   <div className="flex items-center justify-between gap-2"><strong className="min-w-0 break-words">{m.description}</strong>
    {monitorActive(m) && <Button variant="ghost" disabled={stop.isPending || m.state === 'STOPPING'} onClick={() => stop.mutate(m.id)}>{m.state === 'STOPPING' ? 'Stopping…' : 'Stop'}</Button>}
   </div>
   <p className="text-muted text-xs">{monitorActive(m) && !m.confirmed ? 'Activity not confirmed · ' : ''}{m.state.toLowerCase()} · started {new Date(m.startedAt).toLocaleString()} · last activity {new Date(m.updatedAt).toLocaleString()} · expires {new Date(m.expiresAt).toLocaleString()}</p>
   <p className="text-muted text-xs">{m.automaticResumesRemaining ? `${m.automaticResumesRemaining} automatic agent resumptions remaining` : 'Automatic resumption limit reached; output remains available here.'}</p>
   <code className="block break-all text-xs">{m.command}</code>
   {m.output && <pre className="bg-surface-2 mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded p-2 text-xs">{m.output}</pre>}
   {m.error && <p className="text-warn-text text-xs">{m.error}</p>}
  </li>)}</ul>
 </details>;
}
