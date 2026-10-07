import * as Primitive from '@radix-ui/react-tabs';
import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

export const Tabs = Primitive.Root;

export function TabsList({ className, ...props }: ComponentProps<typeof Primitive.List>) {
  return (
    <Primitive.List
      className={cn('border-border flex flex-wrap gap-1 border-b', className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof Primitive.Trigger>) {
  return (
    <Primitive.Trigger
      className={cn(
        'text-muted data-[state=active]:text-text data-[state=active]:border-accent -mb-px border-b-2 border-transparent px-3 py-2 text-sm focus:outline-none',
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({ className, ...props }: ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Content
      className={cn('min-h-0 flex-1 overflow-y-auto pt-4 focus:outline-none', className)}
      {...props}
    />
  );
}
