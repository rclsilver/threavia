import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

const field =
  'bg-surface border-border text-text placeholder:text-muted w-full rounded-md border px-3 py-2 text-sm disabled:opacity-50';

export function Input({ className, ...props }: ComponentProps<'input'>) {
  return <input className={cn(field, 'h-9', className)} {...props} />;
}

export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return <textarea className={cn(field, 'resize-y', className)} {...props} />;
}

export function Label({ className, ...props }: ComponentProps<'label'>) {
  return (
    <label
      className={cn('text-muted text-xs font-medium tracking-wide uppercase', className)}
      {...props}
    />
  );
}
