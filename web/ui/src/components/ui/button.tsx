import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

const button = cva(
  'inline-flex shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        primary: 'bg-accent text-accent-text hover:opacity-90',
        secondary: 'bg-surface-2 text-text border-border border hover:bg-border/60',
        ghost: 'text-muted hover:bg-surface-2 hover:text-text',
        danger: 'bg-danger-solid text-white hover:opacity-90',
        link: 'text-accent h-auto p-0 underline-offset-4 hover:underline',
      },
      size: {
        sm: 'h-8 px-2.5',
        md: 'h-9 px-3.5',
        icon: 'size-8',
        // A thumb's size on a phone, the usual size where a pointer is precise.
        lg: 'h-11 px-4 text-[0.9375rem] sm:h-9 sm:px-3.5 sm:text-sm',
      },
    },
    // A link is text: whatever size it is given, it takes no button's box.
    compoundVariants: [{ variant: 'link', className: 'h-auto p-0' }],
    defaultVariants: { variant: 'secondary', size: 'md' },
  },
);

export interface ButtonProps
  extends ComponentProps<'button'>,
    VariantProps<typeof button> {
  asChild?: boolean;
}

export function Button({ className, variant, size, asChild, ...props }: ButtonProps) {
  const Component = asChild ? Slot : 'button';
  return <Component className={cn(button({ variant, size }), className)} {...props} />;
}
