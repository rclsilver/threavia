import { useEffect, useState } from 'react';

import { usePolicy, useSetPolicy } from '@/api/queries';
import type { ExecutionPolicy } from '@/api/types';
import { PolicyForm, RulesEditor } from '@/components/policy-form';
import { Button } from '@/components/ui/button';

/**
 * What the agent may do in this Session.
 *
 * Enforced by Core and by the backend, never by the prompt alone (spec section
 * 17), and a guard rail rather than a sandbox: filesystem and process access
 * stay the real permissions of the backend account.
 *
 * A Session normally has no policy of its own and follows its Project. The
 * panel says so rather than showing the inherited values as if someone had
 * chosen them here — that distinction is what keeps a change meant for a whole
 * Project from being made one Session at a time.
 */
export function PolicyPanel({ sessionId }: { sessionId: string }) {
  const { data } = usePolicy(sessionId);
  const save = useSetPolicy(sessionId);
  const [draft, setDraft] = useState<ExecutionPolicy | null>(null);

  // The switches start from what is in force, so an inheriting Session shows
  // the values it is actually running under. The rules do not: only what this
  // Session set. The Project's apply all the same — the effective policy
  // carries them, and a refusal written there cannot be lifted here — but they
  // are edited where they were written. Listing them here would invite editing
  // a Project rule from a Session, which saves a copy that stops following it.
  useEffect(() => {
    if (data) setDraft({ ...data.effective, rules: data.own?.rules ?? [] });
  }, [data]);

  if (!data || !draft) return null;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-muted text-xs">
          {data.inherited
            ? 'Following the project. Changing the project changes this session.'
            : 'Set for this session, apart from the project.'}
        </span>
        {!data.inherited && (
          <Button
            variant="ghost"
            size="sm"
            className="text-xs"
            disabled={save.isPending}
            onClick={() => save.mutate(null)}
          >
            Follow the project again
          </Button>
        )}
      </div>

      <PolicyForm value={draft} onChange={setDraft} idPrefix="session-policy" />

      <RulesEditor rules={draft.rules ?? []} onChange={(rules) => setDraft({ ...draft, rules })} />

      <p className="text-muted text-xs">
        A guard rail, not a sandbox: filesystem and process access stay the real permissions of the
        backend account.
      </p>

      {save.error && <p className="text-danger text-sm">{save.error.message}</p>}

      <Button
        variant="primary"
        size="sm"
        disabled={save.isPending}
        onClick={() => save.mutate(draft)}
      >
        Apply to this session
      </Button>
    </div>
  );
}
