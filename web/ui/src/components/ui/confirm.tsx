import { useState, type ReactNode } from 'react';

import { Button } from '@/components/ui/button';

/**
 * A destructive action that asks once, in place, before acting.
 *
 * Everywhere something is removed for good it asks the same way — the
 * question where the control was, "No" first — so a person learns one
 * gesture rather than guessing which deletions are one click.
 */
export function ConfirmAction({
  trigger,
  question,
  confirm,
  pending,
  onConfirm,
}: {
  /** The control shown until it is pressed. */
  trigger: (ask: () => void) => ReactNode;
  question: string;
  confirm: string;
  pending?: boolean;
  onConfirm: () => void;
}) {
  const [asking, setAsking] = useState(false);
  if (!asking) return <>{trigger(() => setAsking(true))}</>;
  return (
    <span className="flex items-center gap-2 text-xs">
      <span className="text-muted">{question}</span>
      <Button variant="ghost" size="sm" onClick={() => setAsking(false)}>
        No
      </Button>
      <Button
        variant="danger"
        size="sm"
        disabled={pending}
        onClick={() => {
          onConfirm();
          setAsking(false);
        }}
      >
        {confirm}
      </Button>
    </span>
  );
}
