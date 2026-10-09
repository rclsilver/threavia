/**
 * Numbers and times as a person reads them, in the web client's words
 * (web/ui/src/lib/utils.ts), so the two clients say the same thing.
 */

/** The time of day, for a line in a log that already says which day it is. */
export function clock(value: string | undefined | null): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

/** An enum value as prose: WAITING_INPUT becomes "waiting input". */
export function humanise(value: string | undefined): string {
  return (value ?? '').toLowerCase().replace(/_/g, ' ');
}

/** A duration a person can read, from a millisecond count. */
export function duration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds % 60);
  if (minutes < 60) return `${minutes}m ${rest}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** A token count, abbreviated the way people write them. */
export function tokens(count: number): string {
  if (count < 1000) return String(count);
  if (count < 1_000_000) return `${(count / 1000).toFixed(count < 10_000 ? 1 : 0)}k`;
  return `${(count / 1_000_000).toFixed(1)}M`;
}

/**
 * A cost in US dollars. Small amounts keep the digits that distinguish them:
 * $0.0007 rounded to $0.00 would read as free.
 */
export function cost(usd: number): string {
  if (usd === 0) return '$0';
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

/** A byte count a person can read. */
export function bytes(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} kB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

/** What an artifact is, as far as showing it goes. */
export type ArtifactKind = 'image' | 'html' | 'text' | 'other';

export function artifactKind(mimeType: string, filename: string): ArtifactKind {
  const type = mimeType.toLowerCase();
  const extension = filename.split('.').pop()?.toLowerCase() ?? '';
  if (type.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(extension)) return 'image';
  if (type.startsWith('text/html') || ['html', 'htm'].includes(extension)) return 'html';
  if (
    type.startsWith('text/') ||
    /json|yaml|xml|csv/.test(type) ||
    ['md', 'txt', 'csv', 'json', 'yaml', 'yml', 'log'].includes(extension)
  ) {
    return 'text';
  }
  return 'other';
}
