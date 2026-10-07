import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/** Merges class names, letting a caller override what a component chose. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** A timestamp as the reader's locale writes it. */
export function when(value: string | undefined | null): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
}

/** A byte count a person can read. */
export function bytes(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} kB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

/** An enum value as prose: WAITING_INPUT becomes "waiting input". */
export function humanise(value: string | undefined): string {
  return (value ?? '').toLowerCase().replace(/_/g, ' ');
}
