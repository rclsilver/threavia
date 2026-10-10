import type { BackendInstance, BackendQuotas as QuotaSnapshot } from '@/api/types';
import { backendIcon, quotaNumber } from '@/lib/backend';
import { humanise } from '@/lib/utils';

export function BackendIcon({ provider, className = 'size-4 shrink-0' }: { provider?: string; className?: string }) {
  const Icon = backendIcon(provider);
  return <Icon className={className} aria-hidden="true" />;
}

export function BackendQuotas({ snapshot }: { snapshot?: QuotaSnapshot }) {
  if (!snapshot) return <p className="text-muted text-xs">No quota report received yet.</p>;
  return (
    <div className="space-y-2 text-xs">
      <p className="text-muted">
        Quotas · {humanise(snapshot.availability)} · observed {new Date(snapshot.observedAt).toLocaleString()}
      </p>
      {snapshot.message && <p>{snapshot.message}</p>}
      {snapshot.limits.map((limit) => (
        <div key={limit.id} className="border-border space-y-1 border-t pt-2">
          <p className="flex flex-wrap justify-between gap-x-4 gap-y-1">
            <span className="font-medium">{limit.label}</span>
            <span className="font-mono tabular-nums">
              {limit.percentUsed !== undefined ? `${quotaNumber(limit.percentUsed)}% used` : 'Usage not reported'}
              {limit.status && ` · ${humanise(limit.status)}`}
            </span>
          </p>
          {(limit.used !== undefined || limit.limit !== undefined || limit.remaining !== undefined) && (
            <p className="text-muted tabular-nums">
              {limit.used !== undefined && `Used ${quotaNumber(limit.used)} ${limit.unit ?? ''} · `}
              {limit.limit !== undefined && `Limit ${quotaNumber(limit.limit)} ${limit.unit ?? ''} · `}
              {limit.remaining !== undefined && `Remaining ${quotaNumber(limit.remaining)} ${limit.unit ?? ''}`}
            </p>
          )}
          {!!limit.windowSeconds && <p className="text-muted">Window: {quotaNumber(limit.windowSeconds / 60)} min</p>}
          {limit.resetsAt && <p className="text-muted">Resets {new Date(limit.resetsAt).toLocaleString()}</p>}
          {(limit.thresholds ?? []).map((threshold, index) => (
            <p key={index}>{threshold.label}{threshold.value !== undefined && `: ${quotaNumber(threshold.value)} ${threshold.unit ?? ''}`}{threshold.status && ` · ${humanise(threshold.status)}`}</p>
          ))}
          {limit.details && <ProviderDetails value={limit.details} />}
        </div>
      ))}
      {snapshot.details && <ProviderDetails value={snapshot.details} />}
    </div>
  );
}

function ProviderDetails({ value }: { value: Record<string, unknown> }) {
  return <details className="text-muted"><summary className="cursor-pointer focus-visible:outline focus-visible:outline-2">Provider details</summary><pre className="mt-1 max-h-52 overflow-auto whitespace-pre-wrap break-all font-mono text-xs">{JSON.stringify(value, null, 2)}</pre></details>;
}

/** Hover, keyboard focus and a touch/click all reveal the same quota report. */
export function BackendInfo({ backend }: { backend: BackendInstance }) {
  return (
    <details className="relative"
      onMouseEnter={(event) => { event.currentTarget.open = true; }}
      onMouseLeave={(event) => { if (!event.currentTarget.contains(document.activeElement)) event.currentTarget.open = false; }}
      onFocus={(event) => { if (event.target instanceof HTMLElement && event.target.matches(':focus-visible')) event.currentTarget.open = true; }}
      onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false; }}
      onKeyDown={(event) => { if (event.key === 'Escape') { event.currentTarget.open = false; event.stopPropagation(); } }}>
      <summary className="hover:text-text flex cursor-pointer list-none items-center gap-1 rounded focus-visible:outline focus-visible:outline-2 [&::-webkit-details-marker]:hidden" aria-label={`${backend.backend || 'Backend'} ${backend.name}: show quotas`}>
        <BackendIcon provider={backend.backend} className="size-3.5 shrink-0" />{backend.name}
      </summary>
      <div className="bg-surface text-text border-border absolute top-full left-0 z-30 max-h-[60vh] w-[min(23rem,calc(100vw-3rem))] overflow-auto rounded-md border p-3 shadow-lg max-sm:fixed max-sm:top-24 max-sm:left-3">
        <p className="mb-2 font-medium">{backend.name} · {backend.backend || 'Unknown provider'}</p>
        <BackendQuotas snapshot={backend.quotas} />
      </div>
    </details>
  );
}
