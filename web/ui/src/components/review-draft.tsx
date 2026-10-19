import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { Button } from './ui/button';
import { ActionError } from './ui/action-error';

export interface DraftHandle { flush(): Promise<void> }

/** Text survives errors and navigation waits until the Core accepted the draft. */
export const ReviewDraft = forwardRef<DraftHandle, {
  label: string;
  initialBody?: string;
  save: (body: string) => Promise<void>;
  close: () => void;
}>(function ReviewDraft({ label, initialBody = '', save, close }, ref) {
  const [body, setBody] = useState(initialBody);
  const [saved, setSaved] = useState(initialBody);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const latest = useRef({ body, saved, save }); latest.current = { body, saved, save };
  const inFlight = useRef<Promise<void> | null>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);

  const flush = async () => {
    if (inFlight.current) await inFlight.current;
    const value = latest.current;
    if (!value.body.trim() || value.body === value.saved) return;
    setSaving(true); setError(null);
    const work = value.save(value.body).then(() => {
      latest.current.saved = value.body;
      if (alive.current) setSaved(value.body);
    }).catch((failure: unknown) => { if (alive.current) setError(failure); throw failure; })
      .finally(() => { inFlight.current = null; if (alive.current) setSaving(false); });
    inFlight.current = work;
    await work;
  };
  const flushRef = useRef(flush); flushRef.current = flush;
  useImperativeHandle(ref, () => ({ flush: () => flushRef.current() }), []);

  useEffect(() => {
    if (body === saved || !body.trim()) return;
    const timer = setTimeout(() => void flushRef.current().catch(() => undefined), 800);
    return () => clearTimeout(timer);
  }, [body, saved]);
  useEffect(() => {
    if (body === saved) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [body, saved]);

  return <section className="review-draft" aria-label="Draft comment">
    <label htmlFor="review-comment" className="text-sm font-medium">{label}</label>
    <textarea id="review-comment" rows={4} maxLength={20000} value={body}
      onChange={(event) => setBody(event.target.value)} placeholder="Describe the change you want, or ask a question…" />
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span role="status" className="text-muted text-xs">{saving ? 'Saving draft…' : body !== saved ? 'Draft not saved yet' : saved ? 'Draft saved · visible on your other devices' : 'Only sent when you request corrections'}</span>
      <Button size="lg" disabled={saving} onClick={() => void flush().then(close).catch(() => undefined)}>Done</Button>
    </div>
    <ActionError error={error ? (error instanceof Error ? error : new Error(typeof error === 'string' ? error : 'Unexpected error')) : null} outcome="Your draft was not saved" recovery="Your text is still here. Press Retry to save it." />
    {Boolean(error) && <Button size="lg" onClick={() => void flush().catch(() => undefined)}>Retry saving</Button>}
  </section>;
});
