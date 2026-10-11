/** Compact quota labels shared by the Web and editor clients. */
export interface QuotaWindow {
  id: string;
  label: string;
  windowSeconds?: number;
  percentUsed?: number;
  resetsAt?: string;
}
export function quotaDuration(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400}d`;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}
export function quotaLabel(limit: QuotaWindow): string {
  const label = limit.label.replace(/\b(\d+(?:\.\d+)?)\s*min\b/g, (_, minutes: string) => quotaDuration(Number(minutes) * 60));
  const seconds = limit.windowSeconds || (limit.id === 'five_hour' ? 18000 : limit.id === 'seven_day' ? 604800 : undefined);
  if (!seconds) return label;
  const period = quotaDuration(seconds);
  if (/^(five hour|seven day|codex)$/i.test(label)) return period;
  if (label.includes(period)) return label.replace(/^codex · /i, '');
  return `${label} · ${period}`;
}
export function quotaPercent(limit: QuotaWindow): number | undefined {
  return limit.percentUsed !== undefined && Number.isFinite(limit.percentUsed) ? limit.percentUsed : undefined;
}
export function quotaReset(value: string): string {
  return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(value));
}
