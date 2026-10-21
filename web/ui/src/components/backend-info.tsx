import { Server } from 'lucide-react';
import type { BackendInstance, BackendQuotas as QuotaSnapshot } from '@/api/types';
import claudeIcon from '@/assets/providers/claude.svg';
import codexIcon from '@/assets/providers/codex.png';
import { quotaLabel, quotaNumber, quotaPercent, quotaReset } from '@/lib/backend';
import { cn, humanise } from '@/lib/utils';

export function BackendIcon({ provider, className = 'size-4 shrink-0' }: { provider?: string; className?: string }) {
  const source = provider?.toLowerCase() === 'claude' ? claudeIcon : provider?.toLowerCase() === 'codex' ? codexIcon : undefined;
  return source ? <img src={source} alt="" className={className} /> : <Server className={className} aria-hidden="true" />;
}

export function BackendQuotas({ snapshot }: { snapshot?: QuotaSnapshot }) {
  if (!snapshot) return <p className="text-muted text-xs">No quota report received yet.</p>;
  return (
    <div className="space-y-3 text-xs">
      <p className="text-muted">Quotas · {humanise(snapshot.availability)} · observed {new Date(snapshot.observedAt).toLocaleString()}</p>
      {snapshot.message && <p>{snapshot.message}</p>}
      {snapshot.limits.map((limit) => {
        const percentage = quotaPercent(limit);
        const label = quotaLabel(limit);
        return (
          <div key={limit.id} className="border-border space-y-1.5 border-t pt-3">
            <p className="flex flex-wrap justify-between gap-x-4 gap-y-1">
              <span className="font-medium">{label}</span>
              <span className="tabular-nums">{percentage !== undefined ? `${quotaNumber(percentage)}% used` : 'Usage not reported'}{limit.status && ` · ${humanise(limit.status)}`}</span>
            </p>
            {percentage !== undefined && (
              <div role="progressbar" aria-label={`${label} usage`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.max(0, Math.min(100, percentage))} aria-valuetext={`${quotaNumber(percentage)}% used`} className="bg-surface-2 h-1.5 overflow-hidden rounded-sm">
                <div className={`h-full ${percentage >= 100 ? 'bg-danger' : percentage >= 80 ? 'bg-warn' : 'bg-accent'}`} style={{ width: `${Math.max(0, Math.min(100, percentage))}%` }} />
              </div>
            )}
            {(limit.used !== undefined || limit.limit !== undefined || limit.remaining !== undefined) && (
              <p className="text-muted tabular-nums">{[
                limit.used !== undefined && `Used ${quotaNumber(limit.used)} ${limit.unit ?? ''}`,
                limit.limit !== undefined && `Limit ${quotaNumber(limit.limit)} ${limit.unit ?? ''}`,
                limit.remaining !== undefined && `Remaining ${quotaNumber(limit.remaining)} ${limit.unit ?? ''}`,
              ].filter(Boolean).join(' · ')}</p>
            )}
            {limit.resetsAt && <p className="text-muted tabular-nums">Resets <time dateTime={limit.resetsAt} title={new Date(limit.resetsAt).toLocaleString()}>{quotaReset(limit.resetsAt)}</time></p>}
            {(limit.thresholds ?? []).map((threshold, index) => (
              <p key={index}>{threshold.label}{threshold.value !== undefined && `: ${quotaNumber(threshold.value)} ${threshold.unit ?? ''}`}{threshold.status && ` · ${humanise(threshold.status)}`}</p>
            ))}
          </div>
        );
      })}
    </div>
  );
}

/** Hover, keyboard focus and a touch/click all reveal the same quota report. */
export function BackendInfo({ backend, compact = false }: { backend: BackendInstance; compact?: boolean }) {
  return (
    <details className={cn('relative', compact && 'min-w-0')}
      onMouseEnter={(event) => { event.currentTarget.open = true; }}
      onMouseLeave={(event) => { if (!event.currentTarget.contains(document.activeElement)) event.currentTarget.open = false; }}
      onFocus={(event) => { if (event.target instanceof HTMLElement && event.target.matches(':focus-visible')) event.currentTarget.open = true; }}
      onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false; }}
      onKeyDown={(event) => { if (event.key === 'Escape') { event.currentTarget.open = false; event.stopPropagation(); } }}>
      <summary className="hover:text-text flex cursor-pointer list-none items-center gap-1 rounded focus-visible:outline focus-visible:outline-2 [&::-webkit-details-marker]:hidden" aria-label={`${backend.backend || 'Backend'} ${backend.name}: show quotas`}>
        <BackendIcon provider={backend.backend} className="size-3.5 shrink-0" /><span className={cn(compact && 'truncate')}>{backend.name}</span>
      </summary>
      <div className="bg-surface text-text border-border absolute top-full left-0 z-30 max-h-[60vh] w-[min(23rem,calc(100vw-3rem))] overflow-auto rounded-md border p-3 shadow-lg max-sm:fixed max-sm:top-24 max-sm:left-3">
        <p className="mb-2 font-medium">{backend.name} · {backend.backend || 'Unknown provider'}</p>
        <BackendQuotas snapshot={backend.quotas} />
      </div>
    </details>
  );
}
