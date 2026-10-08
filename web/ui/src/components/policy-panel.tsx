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
 * A Session normally has no policy of its own and follows its Project, and the
 * panel has two states rather than one. While it follows, the fields show what
 * is in force and cannot be touched: an editable field pre-filled with the
 * Project's values reads as a choice someone made here, and the first
 * keystroke silently turns a shared default into a copy that no longer
 * follows anything. Overriding is a thing you do on purpose, with a button.
 */
export function PolicyPanel({ sessionId }: { sessionId: string }) {
  const { data } = usePolicy(sessionId);
  const save = useSetPolicy(sessionId);
  const [overriding, setOverriding] = useState(false);
  const [draft, setDraft] = useState<ExecutionPolicy | null>(null);

  const editing = Boolean(data && (!data.inherited || overriding));

  // Two different things to show. While following, this is a view of what
  // applies, Project rules included. While editing, it is what this Session
  // sets — and its rules are its own, because the Project's are edited where
  // they were written.
  useEffect(() => {
    if (!data) return;
    setDraft({
      ...data.effective,
      rules: (editing ? data.own?.rules : data.effective.rules) ?? [],
    });
  }, [data, editing]);

  if (!data || !draft) return null;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-muted text-xs">
          {!editing && 'Following the project. Changing the project changes this session.'}
          {editing && data.inherited && 'Overriding for this session. Nothing is saved yet.'}
          {editing && !data.inherited && 'Set for this session, apart from the project.'}
        </span>

        {!editing && (
          <Button variant="ghost" size="sm" className="text-xs" onClick={() => setOverriding(true)}>
            Override for this session
          </Button>
        )}
        {editing && data.inherited && (
          <Button variant="ghost" size="sm" className="text-xs" onClick={() => setOverriding(false)}>
            Cancel
          </Button>
        )}
        {editing && !data.inherited && (
          <Button
            variant="ghost"
            size="sm"
            className="text-xs"
            disabled={save.isPending}
            onClick={() => {
              setOverriding(false);
              save.mutate(null);
            }}
          >
            Follow the project again
          </Button>
        )}
      </div>

      <PolicyForm value={draft} onChange={setDraft} idPrefix="session-policy" disabled={!editing} />

      <RulesEditor
        rules={draft.rules ?? []}
        onChange={(rules) => setDraft({ ...draft, rules })}
        disabled={!editing}
      />

      <p className="text-muted text-xs">
        A guard rail, not a sandbox: filesystem and process access stay the real permissions of the
        backend account.
      </p>

      {save.error && <p className="text-danger text-sm">{save.error.message}</p>}

      {editing && (
        <Button
          variant="primary"
          size="sm"
          disabled={save.isPending}
          onClick={() => {
            setOverriding(false);
            save.mutate(draft);
          }}
        >
          Apply to this session
        </Button>
      )}
    </div>
  );
}
