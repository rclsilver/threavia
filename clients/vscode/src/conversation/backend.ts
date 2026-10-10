import type { BackendInstance, BackendQuota, BackendQuotas } from '../api/types';
import { humanise } from './format';

export function backendIcon(provider?: string): string {
  switch (provider?.toLowerCase()) { case 'claude': return 'sparkle'; case 'codex': return 'terminal'; default: return 'server'; }
}

export function quotaSummary(limit: BackendQuota): string {
  return [limit.percentUsed === undefined ? 'Usage not reported' : `${limit.percentUsed.toLocaleString(undefined, { maximumFractionDigits: 2 })}% used`, limit.status && humanise(limit.status)].filter(Boolean).join(' · ');
}

export function quotaText(snapshot?: BackendQuotas): string {
  if (!snapshot) return 'No quota report received yet.';
  const lines = [`Quotas: ${humanise(snapshot.availability)}`, `Observed: ${new Date(snapshot.observedAt).toLocaleString()}`];
  if (snapshot.message) lines.push(snapshot.message);
  for (const limit of snapshot.limits) {
    lines.push('', `${limit.label}: ${quotaSummary(limit)}`);
    for (const field of ['used', 'limit', 'remaining'] as const) if (limit[field] !== undefined) lines.push(`${humanise(field)}: ${limit[field]} ${limit.unit ?? ''}`);
    if (limit.windowSeconds) lines.push(`Window: ${limit.windowSeconds / 60} min`);
    if (limit.resetsAt) lines.push(`Resets: ${new Date(limit.resetsAt).toLocaleString()}`);
    for (const threshold of limit.thresholds ?? []) lines.push(`${threshold.label}: ${threshold.value ?? 'not reported'} ${threshold.unit ?? ''}${threshold.status ? ` · ${humanise(threshold.status)}` : ''}`);
    if (limit.details) lines.push(`Provider details: ${JSON.stringify(limit.details, null, 2)}`);
  }
  if (snapshot.details) lines.push('', `Provider details: ${JSON.stringify(snapshot.details, null, 2)}`);
  return lines.join('\n');
}

export function backendTooltip(backend: BackendInstance): string {
  return `${backend.name} · ${backend.backend || 'Unknown provider'}\n${humanise(backend.operationalStatus)}\n\n${quotaText(backend.quotas)}`;
}
