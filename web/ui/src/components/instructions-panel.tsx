import { useEffect, useId, useState } from 'react';

import { useInstructions, useSetInstructions, type InstructionsScope } from '@/api/queries';
import type { Instructions } from '@/api/types';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/input';

/** What Core accepts per text at the global and backend levels, and for the project supervision. */
const MAX_LENGTH = 4000;

type Field = keyof Instructions;

const FIELDS: Record<Field, { label: string; hint: string; placeholder: string }> = {
  agent: {
    label: 'Agent instructions',
    hint: "Added to the agent's system prompt at the start of every Job.",
    placeholder: '- Answer in English.\n- Run the tests before every commit.',
  },
  supervision: {
    // Said here because the box looks like the one above it, and the reader
    // is not the same: the agent never sees this text.
    label: 'Supervisor instructions',
    hint: 'Read only by the reviewer of a Supervised session, before every message; the agent never sees them. A statement, not a guarantee: write a refusal in the permissions for what has to hold regardless.',
    placeholder: 'Never deploy or migrate without asking. Reading anything is fine.',
  },
};

/** Where this level sits, said once above its fields. */
const PLACE: Record<InstructionsScope['kind'], string> = {
  instance: 'The broadest level: every project and backend adds to these. When two levels conflict, the narrower one wins.',
  project: 'Added after the global instructions in every Job of this project. When they conflict, these win.',
  backend: "Added after the global and the Job's project instructions on this backend. When they conflict, these win.",
};

/**
 * What the levels above already say, under the box that adds to it.
 *
 * Shown read-only and as the agent or reviewer will read it, so a level is
 * written as a complement — or a correction — of something visible, not of
 * something remembered.
 */
export function InheritedText({ text, note }: { text: string; note?: string }) {
  return (
    <div className="bg-surface-2 border-border rounded-md border px-3 py-2">
      <h4 className="text-muted text-xs tracking-wide uppercase">Inherited from above</h4>
      {text.trim() ? (
        <pre className="text-muted mt-1 max-h-60 overflow-y-auto font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</pre>
      ) : (
        <p className="text-muted mt-1 text-xs">Nothing above this level.</p>
      )}
      {note && <p className="text-muted mt-1 text-xs">{note}</p>}
    </div>
  );
}

/**
 * The agent and supervisor instructions of one level, edited in place.
 *
 * Only what was touched is held here, over what Core returned: a refresh
 * brought by another client or by the other field's save shows through
 * instead of being written back over. Core replaces both texts on a save, so
 * the untouched one goes back as it stands.
 */
export function InstructionsPanel({
  scope,
  only,
}: {
  scope: InstructionsScope;
  /** One field alone, where the other has an editor of its own. */
  only?: Field;
}) {
  const { data, error } = useInstructions(scope);
  const save = useSetInstructions(scope);
  const [edits, setEdits] = useState<Partial<Instructions>>({});
  const [saved, setSaved] = useState(false);
  const fields: Field[] = only ? [only] : ['agent', 'supervision'];
  const dirty = Boolean(data && fields.some((field) => edits[field] !== undefined && edits[field] !== data.own[field]));

  useEffect(() => {
    if (!dirty) return;
    const onLeave = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', onLeave);
    return () => window.removeEventListener('beforeunload', onLeave);
  }, [dirty]);

  // "Saved" is said once, then the page is simply the page again.
  useEffect(() => {
    if (!saved) return;
    const timer = setTimeout(() => setSaved(false), 3000);
    return () => clearTimeout(timer);
  }, [saved]);

  if (error) return <ActionError error={error} outcome="The instructions could not be read" recovery="Reload the page." />;
  if (!data) return <p className="text-muted text-sm">Reading the instructions…</p>;

  const value: Instructions = { ...data.own, ...edits };
  const commit = () => {
    if (!dirty || save.isPending) return;
    save.mutate(value, { onSuccess: () => { setEdits({}); setSaved(true); } });
  };

  return (
    <div
      className="space-y-5"
      data-tour={`${scope.kind}-instructions`}
      onKeyDown={(event) => {
        if (event.key === 's' && (event.metaKey || event.ctrlKey)) {
          event.preventDefault();
          commit();
        }
      }}
    >
      {!only && <p className="text-muted text-sm">{PLACE[scope.kind]}</p>}
      {fields.map((field) => (
        <InstructionsField
          key={field}
          field={field}
          value={value[field]}
          onChange={(text) => { setEdits({ ...edits, [field]: text }); setSaved(false); save.reset(); }}
          inherited={scope.kind === 'instance' ? undefined : data.inherited[field]}
          inheritedNote={scope.kind === 'backend' ? "A Job's project instructions come between these and this backend's." : undefined}
        />
      ))}
      <p className="text-muted text-xs">A change reaches the next Job; a Job already running keeps the instructions it started with.</p>
      <ActionError error={save.error} outcome="Not saved" recovery="Your changes are still here; save again." />
      {dirty && (
        <div className="bg-surface border-border sticky bottom-3 z-10 flex flex-wrap items-center gap-2 rounded-xl border p-3 shadow-lg">
          <span className="text-muted mr-auto hidden text-xs sm:inline">Ctrl+S saves</span>
          <Button variant="ghost" size="lg" onClick={() => { setEdits({}); save.reset(); }}>Discard</Button>
          <Button variant="primary" size="lg" disabled={save.isPending} onClick={commit}>
            {save.isPending ? 'Saving…' : 'Save instructions'}
          </Button>
        </div>
      )}
      {saved && <p role="status" className="text-ok text-sm">Instructions saved.</p>}
    </div>
  );
}

function InstructionsField({
  field,
  value,
  onChange,
  inherited,
  inheritedNote,
}: {
  field: Field;
  value: string;
  onChange: (text: string) => void;
  /** Absent at the global level, which has nothing above it. */
  inherited?: string;
  inheritedNote?: string;
}) {
  const id = useId();
  const { label, hint, placeholder } = FIELDS[field];
  return (
    <section className="space-y-1.5">
      <label htmlFor={id} className="text-sm font-medium">{label}</label>
      <p id={`${id}-hint`} className="text-muted text-xs">{hint}</p>
      <Textarea
        id={id}
        rows={6}
        maxLength={MAX_LENGTH}
        aria-describedby={`${id}-hint ${id}-count`}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className="font-mono text-[0.8125rem] leading-6"
      />
      <p id={`${id}-count`} className="text-muted figures text-right text-xs">{value.length} / {MAX_LENGTH}</p>
      {inherited !== undefined && <InheritedText text={inherited} note={inheritedNote} />}
    </section>
  );
}
