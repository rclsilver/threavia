import * as Primitive from '@radix-ui/react-checkbox';
import { Check } from 'lucide-react';
import type { ComponentProps, ReactNode } from 'react';

import { cn } from '@/lib/utils';

export function Checkbox({ className, ...props }: ComponentProps<typeof Primitive.Root>) {
  return (
    <Primitive.Root
      className={cn(
        'border-border data-[state=checked]:bg-accent data-[state=checked]:border-accent grid size-4 shrink-0 place-items-center rounded border',
        className,
      )}
      {...props}
    >
      <Primitive.Indicator>
        <Check className="text-accent-text size-3" />
      </Primitive.Indicator>
    </Primitive.Root>
  );
}

/** A checkbox with its label, which is how every one of them is used here. */
export function CheckboxField({
  checked,
  onCheckedChange,
  children,
  disabled,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  children: ReactNode;
  disabled?: boolean;
}) {
  return (
    <label
      className={
        disabled
          ? 'text-muted flex items-center gap-2 text-sm opacity-50'
          : 'text-muted flex cursor-pointer items-center gap-2 text-sm'
      }
    >
      <Checkbox
        checked={checked}
        disabled={disabled}
        onCheckedChange={(value) => onCheckedChange(value === true)}
      />
      {children}
    </label>
  );
}
