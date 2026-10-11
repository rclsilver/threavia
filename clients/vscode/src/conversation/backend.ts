import type { BackendInstance, BackendQuota, BackendQuotas } from '../api/types';
import { humanise } from './format';

import { quotaLabel, quotaReset } from '../../../shared/quotas';
export { quotaLabel, quotaPercent, quotaReset } from '../../../shared/quotas';

export function quotaSummary(limit: BackendQuota): string {
  return [limit.percentUsed === undefined ? 'Usage not reported' : `${limit.percentUsed.toLocaleString(undefined, { maximumFractionDigits: 2 })}% used`, limit.status && humanise(limit.status)].filter(Boolean).join(' · ');
}

export function quotaText(snapshot?: BackendQuotas): string {
  if (!snapshot) return 'No quota report received yet.';
  const lines = [`Quotas: ${humanise(snapshot.availability)}`, `Observed: ${new Date(snapshot.observedAt).toLocaleString()}`];
  if (snapshot.message) lines.push(snapshot.message);
  for (const limit of snapshot.limits) {
    lines.push('', `${quotaLabel(limit)}: ${quotaSummary(limit)}`);
    for (const field of ['used', 'limit', 'remaining'] as const) if (limit[field] !== undefined) lines.push(`${humanise(field)}: ${limit[field]} ${limit.unit ?? ''}`);
    if (limit.resetsAt) lines.push(`Resets: ${quotaReset(limit.resetsAt)}`);
    for (const threshold of limit.thresholds ?? []) lines.push(`${threshold.label}: ${threshold.value ?? 'not reported'} ${threshold.unit ?? ''}${threshold.status ? ` · ${humanise(threshold.status)}` : ''}`);
  }
  return lines.join('\n');
}

export function backendTooltip(backend: BackendInstance): string {
  return `${backend.name} · ${backend.backend || 'Unknown provider'}\n${humanise(backend.operationalStatus)}\n\n${quotaText(backend.quotas)}`;
}
