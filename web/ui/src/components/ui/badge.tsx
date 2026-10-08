import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

const badge = cva(
  'inline-flex items-center rounded px-1.5 py-0.5 text-[0.6875rem] font-medium whitespace-nowrap',
  {
    variants: {
      tone: {
        neutral: 'bg-surface-2 text-muted',
        ok: 'bg-ok/15 text-ok',
        warn: 'bg-warn/15 text-warn-text',
        danger: 'bg-danger/15 text-danger',
        accent: 'bg-accent/15 text-accent',
      },
    },
    defaultVariants: { tone: 'neutral' },
  },
);

export type BadgeTone = NonNullable<VariantProps<typeof badge>['tone']>;

export function Badge({
  className,
  tone,
  ...props
}: ComponentProps<'span'> & VariantProps<typeof badge>) {
  return <span className={cn(badge({ tone }), className)} {...props} />;
}
