import * as Primitive from '@radix-ui/react-dropdown-menu';
import { Check } from 'lucide-react';
import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

export const Menu = Primitive.Root;
export const MenuTrigger = Primitive.Trigger;

export function MenuContent({ className, ...props }: ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        sideOffset={4}
        className={cn(
          'bg-surface border-border z-50 max-h-[min(24rem,var(--radix-dropdown-menu-content-available-height))] min-w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto rounded-md border p-1 shadow-lg',
          className,
        )}
        {...props}
      />
    </Primitive.Portal>
  );
}

export function MenuItem({ className, ...props }: ComponentProps<typeof Primitive.Item>) {
  return (
    <Primitive.Item
      className={cn(
        'data-[highlighted]:bg-surface-2 flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm outline-none',
        className,
      )}
      {...props}
    />
  );
}

export function MenuLabel({ className, ...props }: ComponentProps<typeof Primitive.Label>) {
  return <Primitive.Label className={cn('text-muted px-2 pt-1 pb-1.5 text-xs', className)} {...props} />;
}

export function MenuSeparator({ className, ...props }: ComponentProps<typeof Primitive.Separator>) {
  return <Primitive.Separator className={cn('bg-border -mx-1 my-1 h-px', className)} {...props} />;
}

/** An item that is on or off, and keeps the menu open so several can be set. */
export function MenuCheckboxItem({
  className,
  children,
  onSelect,
  ...props
}: ComponentProps<typeof Primitive.CheckboxItem>) {
  return (
    <Primitive.CheckboxItem
      className={cn(
        'group data-[highlighted]:bg-surface-2 flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm outline-none',
        className,
      )}
      onSelect={(event) => {
        event.preventDefault();
        onSelect?.(event);
      }}
      {...props}
    >
      <span className="border-border group-data-[state=checked]:bg-accent group-data-[state=checked]:border-accent flex size-4 shrink-0 items-center justify-center rounded border">
        <Primitive.ItemIndicator>
          <Check className="text-accent-text size-3" />
        </Primitive.ItemIndicator>
      </span>
      {children}
    </Primitive.CheckboxItem>
  );
}
