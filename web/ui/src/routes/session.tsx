import { useParams } from '@tanstack/react-router';
import { ArrowUp } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';

import {
  useBackends,
  useCancelJob,
  useMoveSession,
  usePostMessage,
  useSnapshot,
} from '@/api/queries';
import { AttentionPanel } from '@/components/attention';
import { PolicyPanel } from '@/components/policy-panel';
import { Timeline, type Pending } from '@/components/timeline';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useStream } from '@/use-stream';
import { humanise } from '@/lib/utils';

const FINISHED = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);

export function SessionView() {
  const { sessionId } = useParams({ from: '/sessions/$sessionId' });
  const { seen, activity } = useStream();
  const snapshot = useSnapshot(sessionId);
  const cancel = useCancelJob(sessionId);
  const [showPolicy, setShowPolicy] = useState(false);

  // The snapshot is a point the stream has already passed, so a reconnection
  // resumes from here rather than replaying what is on screen.
  useEffect(() => {
    if (snapshot.data) seen(snapshot.data.cursor);
  }, [snapshot.data, seen]);

  if (snapshot.isPending) {
    return <p className="text-muted p-6 text-sm">Loading…</p>;
  }
  if (snapshot.error) {
    return <p className="text-danger p-6 text-sm">{(snapshot.error).message}</p>;
  }

  const data = snapshot.data;
  const active = data.jobs.find((job) => !FINISHED.has(job.status));
  const working = activity(sessionId);

  // Every Job still going somewhere, so the timeline can offer a stop on the
  // message that started it. A Session has one active Job and may have several
  // queued, and a person wants to drop one of those as much as to interrupt the
  // one running.
  const pending: Pending = {};
  for (const job of data.jobs) {
    if (!FINISHED.has(job.status)) pending[job.id] = job.status;
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* The frame keeps the window; only what is read is held to a column. */}
      <header className="border-border/70 flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-b px-5 py-3">
        <div className="min-w-0">
          <h2 className="truncate text-[0.9375rem] font-semibold">
            {data.session.title || 'Untitled session'}
          </h2>
          <p className="text-muted text-xs">
            {active ? `Job ${humanise(active.status)}` : 'Idle'}
            {active && working && (
              // A liveness signal, not history: it says the agent is still
              // there between two things worth remembering. Shown only while a
              // Job is running, because nothing orders these against the
              // timeline and one can arrive after the Job it describes ended.
              <span className="text-accent ml-2">· {humanise(working)}…</span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-1">
          <MoveSessionButton sessionId={sessionId} />
          <Button variant="ghost" size="sm" onClick={() => setShowPolicy((shown) => !shown)}>
            Execution policy
          </Button>
        </div>
      </header>

      {showPolicy && (
        <div className="mx-auto w-full max-w-reading px-6 pt-4">
          <PolicyPanel sessionId={sessionId} />
        </div>
      )}

      {(data.attention.validations?.length || data.attention.userInputs?.length) && (
        <div className="mx-auto w-full max-w-reading px-6 pt-4">
          <AttentionPanel
            validations={data.attention.validations ?? []}
            userInputs={data.attention.userInputs ?? []}
          />
        </div>
      )}

      <Timeline events={data.events} pending={pending} onStop={(jobId) => cancel.mutate(jobId)} />

      <Composer sessionId={sessionId} />
    </div>
  );
}

/**
 * Where a message is written.
 *
 * One rounded field holding its own send button, rather than a box and a
 * detached button: the whole thing reads as the single place to type, and it
 * grows with what is being written instead of hiding the top of a long message
 * behind a fixed three rows.
 */
function Composer({ sessionId }: { sessionId: string }) {
  const send = usePostMessage(sessionId);
  const [message, setMessage] = useState('');
  const field = useRef<HTMLTextAreaElement>(null);

  useLayoutEffect(() => {
    const element = field.current;
    if (!element) return;
    element.style.height = 'auto';
    element.style.height = `${Math.min(element.scrollHeight, 240)}px`;
  }, [message]);

  const submit = () => {
    const text = message.trim();
    if (!text) return;
    setMessage('');
    send.mutate(text, { onError: () => setMessage(text) });
  };

  return (
    <div className="mx-auto w-full max-w-reading px-6 pb-4">
      <form
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <div className="bg-surface border-border focus-within:border-muted/60 flex items-end gap-2 rounded-3xl border py-1.5 pr-1.5 pl-4 transition-colors">
          <textarea
            ref={field}
            rows={1}
            value={message}
            onChange={(event) => setMessage(event.target.value)}
            onKeyDown={(event) => {
              // Enter sends, as in every chat. A newline is still reachable
              // with Shift, which is where a person already looks for it. An
              // Enter that closes an input method is composing text, not
              // sending it.
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault();
                submit();
              }
            }}
            placeholder="Send a message…"
            className="placeholder:text-muted/80 max-h-60 flex-1 resize-none bg-transparent py-2 text-sm outline-none"
          />
          <Button
            type="submit"
            variant="primary"
            size="icon"
            className="rounded-full"
            title="Send (Enter)"
            disabled={send.isPending || !message.trim()}
          >
            <ArrowUp />
          </Button>
        </div>
      </form>
      <p className="text-muted/70 mt-2 text-center text-xs">
        Enter sends · Shift+Enter for a newline
      </p>
    </div>
  );
}

/**
 * Moving a Session to another backend, and saying what it costs.
 *
 * The move is explicit and the timeline stays continuous, but the native
 * provider session does not travel: it belongs to the machine holding it
 * (spec section 34).
 */
function MoveSessionButton({ sessionId }: { sessionId: string }) {
  const backends = useBackends();
  const move = useMoveSession(sessionId);
  const [open, setOpen] = useState(false);
  const [target, setTarget] = useState('');

  const usable = (backends.data ?? []).filter(
    (backend) => backend.ownershipStatus !== 'REVOKED',
  );

  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        Change backend
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogTitle>Change backend</DialogTitle>
          <DialogDescription>
            The timeline stays continuous. The provider session does not travel, so the next message
            starts a fresh one there.
          </DialogDescription>

          <Select value={target} onValueChange={setTarget}>
            <SelectTrigger>
              <SelectValue placeholder="Choose a backend" />
            </SelectTrigger>
            <SelectContent>
              {usable.map((backend) => (
                <SelectItem key={backend.id} value={backend.id}>
                  {backend.name} ({humanise(backend.operationalStatus)})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {move.error && <p className="text-danger text-sm">{(move.error).message}</p>}

          {move.data?.missingSkills?.length ? (
            <p className="text-warn text-sm">
              Moved. These local skills are not on the new backend:{' '}
              {move.data.missingSkills.join(', ')}.
            </p>
          ) : null}

          <div className="flex justify-end gap-2">
            <DialogClose asChild>
              <Button variant="ghost" size="sm">
                Close
              </Button>
            </DialogClose>
            <Button
              variant="primary"
              size="sm"
              disabled={!target || move.isPending}
              onClick={() => move.mutate(target)}
            >
              Move
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
