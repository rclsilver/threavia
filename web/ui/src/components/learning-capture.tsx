import { useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { BookOpen } from 'lucide-react';
import { learningSource, requestLearningCapture } from '../../../../clients/shared/learnings';
import { api } from '@/api/client';
import { keys } from '@/api/keys';
import type { BackendInstance, Job, Snapshot } from '@/api/types';
import { ActionError } from './ui/action-error';
import { Button } from './ui/button';
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from './ui/dialog';

export function LearningCapture({ snapshot, backend }: { snapshot: Snapshot; backend?: BackendInstance }) {
  const [open, setOpen] = useState(false);
  const submitting = useRef(false);
  const queries = useQueryClient();
  const sessionId = snapshot.session.id;
  const source = learningSource(snapshot);
  const capture = useMutation({
    mutationFn: (jobId: string) => requestLearningCapture(jobId,
      () => api.get<Snapshot>(`/api/v1/sessions/${sessionId}`),
      (message, key) => api.post<Job>(`/api/v1/sessions/${sessionId}/messages`, { message, delivery: 'QUEUE', afterJobId: jobId }, { 'Idempotency-Key': key })),
    onSuccess: async () => {
      setOpen(false);
      await Promise.all([
        queries.invalidateQueries({ queryKey: keys.snapshot(sessionId) }),
        queries.invalidateQueries({ queryKey: ['sessions'] }),
      ]);
    },
  });
  // Keep an open dialog mounted if another device starts work; confirmation
  // becomes unavailable rather than silently closing or losing its error.
  if (!source && !open) return null;
  return <Dialog open={open} onOpenChange={(next) => { if (!capture.isPending) { setOpen(next); capture.reset(); } }}>
    <DialogTrigger asChild><Button variant="ghost" size="lg" className="h-11 md:h-9" data-tour="capture-learnings"><BookOpen className="size-4" />Capture learnings</Button></DialogTrigger>
    <DialogContent onEscapeKeyDown={(event) => { if (capture.isPending) event.preventDefault(); }} onPointerDownOutside={(event) => { if (capture.isPending) event.preventDefault(); }}>
      <DialogTitle>Capture learnings from this resolution</DialogTitle>
      <DialogDescription>Ask the agent to compare this result with existing memory and propose up to three useful facts, procedures or project rules.</DialogDescription>
      <p className="text-sm">Review the proposals in this conversation. For each one, choose Save, Edit or Ignore before it is applied.</p>
      <p className="text-muted text-sm">This starts a normal agent turn on <strong>{backend?.name ?? 'this session’s backend'}</strong>, using the session’s model and subscription quota. It does not use a separate model service.</p>
      {!source && <p role="status" className="text-warn-text text-sm">This session changed. Wait for its work to finish before capturing the latest result.</p>}
      <ActionError error={capture.error} outcome="Learning capture could not be confirmed" recovery="Check the conversation before retrying; a request may already have been accepted." />
      <div className="flex flex-wrap justify-end gap-2">
        <DialogClose asChild><Button variant="ghost" size="lg" disabled={capture.isPending}>Cancel</Button></DialogClose>
        <Button size="lg" disabled={!source || capture.isPending} onClick={() => {
          if (!source || submitting.current) return;
          submitting.current = true;
          void capture.mutateAsync(source.id).catch(() => { /* The dialog keeps the request error. */ })
            .finally(() => { submitting.current = false; });
        }}>{capture.isPending ? 'Requesting…' : 'Propose learnings'}</Button>
      </div>
    </DialogContent>
  </Dialog>;
}
