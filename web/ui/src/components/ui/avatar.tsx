import { cn } from '@/lib/utils';

/** A person's initial in a tinted circle: how the client shows who is using it. */
export function Avatar({ name, className }: { name: string; className?: string }) {
  return (
    <span
      className={cn(
        'bg-accent/12 text-accent flex size-8 shrink-0 items-center justify-center rounded-full text-sm font-semibold uppercase',
        className,
      )}
    >
      {name.trim().slice(0, 1) || '?'}
    </span>
  );
}
