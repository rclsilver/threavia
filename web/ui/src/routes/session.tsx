import { useParams } from '@tanstack/react-router';
import { useEffect, useState } from 'react';

import {
  useBackends,
  useCancelJob,
  useMoveSession,
  usePostMessage,
  useSnapshot,
} from '@/api/queries';
import type { Job } from '@/api/types';
import { AttentionPanel } from '@/components/attention';
import { PolicyPanel } from '@/components/policy-panel';
import { Timeline } from '@/components/timeline';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useStream } from '@/use-stream';
import { humanise } from '@/lib/utils';

const FINISHED = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);

export function SessionView() {
  const { sessionId } = useParams({ from: '/sessions/$sessionId' });
  const { seen, activity } = useStream();
  const snapshot = useSnapshot(sessionId);
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

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 p-5">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="truncate text-lg font-semibold">
            {data.session.title || 'Untitled session'}
          </h2>
          <p className="text-muted text-sm">
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
        <div className="flex items-center gap-3">
          <MoveSessionButton sessionId={sessionId} />
          <Button variant="link" onClick={() => setShowPolicy((shown) => !shown)}>
            Execution policy
          </Button>
          <CancelButton sessionId={sessionId} job={active} />
        </div>
      </header>

      {showPolicy && <PolicyPanel sessionId={sessionId} />}

      <AttentionPanel
        validations={data.attention.validations ?? []}
        userInputs={data.attention.userInputs ?? []}
      />

      <Timeline events={data.events} />

      <Composer sessionId={sessionId} />
    </div>
  );
}

function CancelButton({ sessionId, job }: { sessionId: string; job: Job | undefined }) {
  const cancel = useCancelJob(sessionId);
  if (!job || job.status === 'CANCELLING') return null;

  return (
    <Button variant="danger" size="sm" disabled={cancel.isPending} onClick={() => cancel.mutate(job.id)}>
      Cancel
    </Button>
  );
}

function Composer({ sessionId }: { sessionId: string }) {
  const send = usePostMessage(sessionId);
  const [message, setMessage] = useState('');

  const submit = () => {
    const text = message.trim();
    if (!text) return;
    setMessage('');
    send.mutate(text, { onError: () => setMessage(text) });
  };

  return (
    <form
      className="flex items-end gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <Textarea
        rows={3}
        value={message}
        onChange={(event) => setMessage(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) submit();
        }}
        placeholder="Send a message…"
      />
      <Button type="submit" variant="primary" disabled={send.isPending}>
        Send
      </Button>
    </form>
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
      <Button variant="link" onClick={() => setOpen(true)}>
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
