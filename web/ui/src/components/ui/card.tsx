import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

export function Card({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={cn('bg-surface border-border rounded-[--radius-card] border p-3', className)}
      {...props}
    />
  );
}

/** A list that says so when it is empty, rather than showing nothing at all. */
export function EmptyState({ children }: { children: React.ReactNode }) {
  return (
    <div className="border-border text-muted rounded-[--radius-card] border border-dashed p-4 text-sm">
      {children}
    </div>
  );
}
