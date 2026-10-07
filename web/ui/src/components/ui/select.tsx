import * as Primitive from '@radix-ui/react-select';
import { Check, ChevronDown } from 'lucide-react';
import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

export const Select = Primitive.Root;
export const SelectValue = Primitive.Value;

export function SelectTrigger({ className, children, ...props }: ComponentProps<typeof Primitive.Trigger>) {
  return (
    <Primitive.Trigger
      className={cn(
        'bg-surface border-border text-text flex h-9 w-full items-center justify-between gap-2 rounded-md border px-3 text-sm',
        className,
      )}
      {...props}
    >
      {children}
      <Primitive.Icon>
        <ChevronDown className="text-muted size-4" />
      </Primitive.Icon>
    </Primitive.Trigger>
  );
}

export function SelectContent({ className, children, ...props }: ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        position="popper"
        sideOffset={4}
        className={cn(
          'bg-surface border-border z-50 max-h-72 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border shadow-lg',
          className,
        )}
        {...props}
      >
        <Primitive.Viewport className="p-1">{children}</Primitive.Viewport>
      </Primitive.Content>
    </Primitive.Portal>
  );
}

export function SelectItem({ className, children, ...props }: ComponentProps<typeof Primitive.Item>) {
  return (
    <Primitive.Item
      className={cn(
        'data-[highlighted]:bg-surface-2 relative flex cursor-pointer items-center rounded px-2 py-1.5 pr-7 text-sm focus:outline-none',
        className,
      )}
      {...props}
    >
      <Primitive.ItemText>{children}</Primitive.ItemText>
      <Primitive.ItemIndicator className="absolute right-2">
        <Check className="size-3.5" />
      </Primitive.ItemIndicator>
    </Primitive.Item>
  );
}
