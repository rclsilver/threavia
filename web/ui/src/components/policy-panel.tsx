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

  useEffect(() => {
    if (data) setDraft(data.effective);
  }, [data]);

  if (!data || !draft) return null;

  // Only what the Project refuses is binding; the rest of its rules are shown
  // with the Session's own, because they are what the Session starts from.
  const inherited = (data.project.rules ?? []).filter((rule) => rule.effect === 'DENY');
  const own = (draft.rules ?? []).filter(
    (rule) => !inherited.some((bound) => bound.capability === rule.capability && bound.match === rule.match),
  );

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

      <RulesEditor
        rules={own}
        inherited={inherited}
        onChange={(rules) => setDraft({ ...draft, rules })}
      />

      <p className="text-muted text-xs">
        A guard rail, not a sandbox: filesystem and process access stay the real permissions of the
        backend account.
      </p>

      {save.error && <p className="text-danger text-sm">{save.error.message}</p>}

      <Button
        variant="primary"
        size="sm"
        disabled={save.isPending}
        onClick={() => save.mutate({ ...draft, rules: own })}
      >
        Apply to this session
      </Button>
    </div>
  );
}
