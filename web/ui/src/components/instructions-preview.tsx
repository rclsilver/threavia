import { Eye } from 'lucide-react';
import { useId, useState } from 'react';

import { useBackends, useInstructionsPreview } from '@/api/queries';
import { BackendIcon } from '@/components/backend-info';
import { ActionError } from '@/components/ui/action-error';
import { Button, type ButtonProps } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

/**
 * The two texts a Job of this Project would read, as Core assembles them for
 * the Job: every level, broadest first, with the note on precedence when more
 * than one speaks.
 *
 * The backend is a choice because it is a level: the same project reads
 * differently on two machines. It starts on the one a Job would most likely
 * run on — the Session's, else the default, else the first.
 */
export function InstructionsPreview({
  projectId,
  backendId,
  sessionId,
}: {
  projectId: string;
  backendId?: string;
  sessionId?: string;
}) {
  const id = useId();
  const backends = useBackends();
  const usable = (backends.data ?? []).filter((backend) => backend.ownershipStatus !== 'REVOKED');
  const [chosen, setChosen] = useState<string>();
  const target = chosen ?? backendId ?? usable.find((backend) => backend.isDefault)?.id ?? usable[0]?.id;
  const preview = useInstructionsPreview(projectId, target, sessionId);

  if (backends.error) return <ActionError error={backends.error} outcome="The backends could not be read" recovery="Reload the page." />;
  if (backends.isPending) return <p className="text-muted text-sm">Reading the backends…</p>;
  if (usable.length === 0) {
    return <p className="text-muted text-sm">No backend yet. The preview needs one, since each backend adds its own instructions.</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <span id={`${id}-backend`} className="text-sm font-medium">On</span>
        <Select value={target ?? ''} onValueChange={setChosen}>
          <SelectTrigger aria-labelledby={`${id}-backend`} className="h-11 min-w-56 flex-1 sm:h-9">
            <SelectValue placeholder="Choose a backend" />
          </SelectTrigger>
          <SelectContent>
            {usable.map((backend) => (
              <SelectItem key={backend.id} value={backend.id}>
                <span className="flex items-center gap-2">
                  <BackendIcon provider={backend.backend} />
                  {backend.name}
                  {backend.isDefault && <span className="text-muted">(default)</span>}
                </span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <ActionError error={preview.error} outcome="The preview could not be assembled" recovery="Choose the backend again or reload." />
      {preview.isPending ? (
        <p className="text-muted text-sm">Assembling the instructions…</p>
      ) : preview.data && (
        <>
          <PreviewText title="Agent instructions" text={preview.data.agent} />
          <PreviewText title="Supervisor instructions" text={preview.data.supervision} />
        </>
      )}
      <p className="text-muted text-xs">
        The Threavia base prompt, this machine's LOCAL_INSTRUCTIONS and per-job notes are added when a job starts.
      </p>
    </div>
  );
}

function PreviewText({ title, text }: { title: string; text: string }) {
  return (
    <section className="space-y-1.5">
      <h3 className="text-muted text-xs tracking-wide uppercase">{title}</h3>
      {text.trim() ? (
        <pre className="bg-surface-2 border-border max-h-72 overflow-y-auto rounded-md border px-3 py-2 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</pre>
      ) : (
        <p className="border-border text-muted rounded-md border border-dashed px-3 py-2 text-sm">Nothing from the levels.</p>
      )}
    </section>
  );
}

/** A button that opens the preview over the page, where reading it does not lose the place. */
export function InstructionsPreviewButton({
  projectId,
  backendId,
  sessionId,
  ...button
}: {
  projectId: string;
  backendId?: string;
  sessionId?: string;
} & Pick<ButtonProps, 'size' | 'variant' | 'className'>) {
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button title="What a Job reads, every level assembled" {...button}>
          <Eye />
          Preview
        </Button>
      </DialogTrigger>
      <DialogContent className="w-[min(48rem,calc(100vw-2rem))]">
        <div className="space-y-1 pr-6">
          <DialogTitle>What a Job reads</DialogTitle>
          <DialogDescription>Every level assembled, broadest first, as Core sends it to the Job.</DialogDescription>
        </div>
        <InstructionsPreview projectId={projectId} backendId={backendId} sessionId={sessionId} />
      </DialogContent>
    </Dialog>
  );
}
