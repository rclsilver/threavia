import { useEffect, useState } from 'react';

import { usePolicy, useSetPolicy, useSnapshot } from '@/api/queries';
import type { ExecutionPolicy } from '@/api/types';
import { InstructionsPreviewButton } from '@/components/instructions-preview';
import { PolicyForm, RulesEditor } from '@/components/policy-form';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';

const FINISHED = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);

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
  const snapshot = useSnapshot(sessionId);
  const running = snapshot.data?.jobs.some((job) => !FINISHED.has(job.status));
  const [draft, setDraft] = useState<ExecutionPolicy | null>(null);

  const editing = Boolean(data && (!data.inherited || overriding));
  const projectId = snapshot.data?.session.projectId;
  // The backend of the latest Run is the one the next Job goes to.
  const backendId = [...(snapshot.data?.runs ?? [])].sort((a, b) => a.createdAt.localeCompare(b.createdAt)).at(-1)?.backendInstanceId;

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
          {!editing && 'Following project, backend and instance defaults. Changes there also apply here.'}
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
            Follow defaults again
          </Button>
        )}
      </div>

      {/* The reviewer's box belongs to this Session's own policy. While it
          follows, the Session says nothing to the reviewer itself, and an
          empty box in grey would read as a text that was cleared. */}
      <PolicyForm value={draft} onChange={setDraft} idPrefix="session-policy" disabled={!editing} showSupervision={editing} />

      <RulesEditor
        rules={draft.rules ?? []}
        onChange={(rules) => setDraft({ ...draft, rules })}
        disabled={!editing}
      />

      {projectId && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-muted text-xs">
            {editing
              ? "The agent and supervisor instructions of this session's next Job, every level assembled."
              : 'The reviewer reads the supervisor instructions of the global, project and backend levels.'}
          </span>
          <InstructionsPreviewButton projectId={projectId} backendId={backendId} sessionId={sessionId} size="sm" variant="ghost" className="text-xs" />
        </div>
      )}

      <p className="text-muted text-xs">
        A guard rail, not a sandbox: filesystem and process access stay the real permissions of the
        backend account.
      </p>

      <ActionError error={save.error} outcome="Not saved" recovery="The permissions that were in place still apply." />

      {/* The provider reads its permissions when the process starts, so a
          change cannot reach a turn already under way. Saying so here is the
          difference between a setting and a promise. */}
      {running && (
        <p className="text-warn-text text-xs">
          A job is running and keeps the permissions it started with. A change applies from the next
          message.
        </p>
      )}

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
