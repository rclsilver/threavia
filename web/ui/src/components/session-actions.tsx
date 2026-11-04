import { useNavigate } from '@tanstack/react-router';
import { AlarmClock, Archive, ArchiveRestore, Trash2 } from 'lucide-react';
import { useState, type ReactNode } from 'react';

import { useArchiveSession, useDeleteSession, useSessionArtifacts } from '@/api/queries';
import type { Session } from '@/api/types';
import { ConfirmLine } from '@/components/record-list';
import { SnoozeItems } from '@/components/snooze-menu';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuSub, MenuSubContent, MenuSubTrigger, MenuTrigger } from '@/components/ui/menu';
import { cn } from '@/lib/utils';

/**
 * Archives a Session, or brings it back. Archiving keeps everything, so it
 * asks nothing: the same button undoes it.
 */
export function ArchiveButton({ session, className }: { session: Session; className?: string }) {
  const archive = useArchiveSession();
  const archived = session.status === 'ARCHIVED';
  const label = archived ? 'Restore from the archive' : 'Archive';
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className={cn('size-11 md:size-8', className)}
        title={label}
        aria-label={label}
        disabled={archive.isPending}
        onClick={() => archive.mutate({ sessionId: session.id, archived: !archived })}
      >
        {archived ? <ArchiveRestore /> : <Archive />}
      </Button>
      <ActionError error={archive.error} outcome={archived ? 'Not restored' : 'Not archived'} className="text-xs" />
    </>
  );
}

/**
 * What it takes to delete a Session: its files are the one thing that may
 * outlive it, so what becomes of them is asked, named, before anything goes.
 */
export function DeleteSessionConfirm({
  sessionId,
  projectId,
  onCancel,
  onDeleted,
}: {
  sessionId: string;
  projectId?: string;
  onCancel: () => void;
  onDeleted: () => void;
}) {
  const remove = useDeleteSession();
  const files = useSessionArtifacts(projectId, sessionId);
  const [artifacts, setArtifacts] = useState<'keep' | 'delete'>('keep');
  const made = files.data ?? [];

  return (
    <div className="space-y-3">
      {made.length > 0 && (
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">
            Its {made.length === 1 ? 'file' : `${made.length} files`}:{' '}
            <span className="text-muted figures font-mono text-xs font-normal">
              {made.slice(0, 3).map((file) => file.filename).join(', ')}
              {made.length > 3 && ` and ${made.length - 3} more`}
            </span>
          </legend>
          <div role="radiogroup" aria-label="Its files" className="grid gap-2 sm:grid-cols-2">
            {(
              [
                { value: 'keep', label: 'Keep them', detail: 'They stay in the project’s artifacts.' },
                { value: 'delete', label: 'Delete them too', detail: 'They are gone for good, with the session.' },
              ] as const
            ).map((option) => (
              <button
                key={option.value}
                type="button"
                role="radio"
                aria-checked={artifacts === option.value}
                onClick={() => setArtifacts(option.value)}
                className={cn(
                  'rounded-(--radius-card) border p-3 text-left',
                  artifacts === option.value
                    ? 'border-accent bg-accent/5 ring-accent/30 ring-1'
                    : 'border-border hover:bg-surface-2',
                )}
              >
                <span className="block text-sm font-medium">{option.label}</span>
                <span className="text-muted block text-sm">{option.detail}</span>
              </button>
            ))}
          </div>
        </fieldset>
      )}
      <ConfirmLine
        question={
          made.length > 0 && artifacts === 'delete'
            ? `Delete this session and its ${made.length === 1 ? 'file' : `${made.length} files`} for good?`
            : 'Delete this session for good?'
        }
        confirm="Delete"
        pending={remove.isPending}
        onCancel={onCancel}
        onConfirm={() => remove.mutate({ sessionId, artifacts: made.length > 0 ? artifacts : 'keep' }, { onSuccess: onDeleted })}
      />
      <ActionError error={remove.error} />
    </div>
  );
}

/**
 * Everything that can be done to a Session from the list, without opening
 * it: put it off, put it away, or get rid of it.
 */
export function SessionMenu({ session, current, children }: { session: Session; current?: string; children: ReactNode }) {
  const archive = useArchiveSession();
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState(false);
  const archived = session.status === 'ARCHIVED';
  const title = session.title || 'Untitled session';

  return (
    <>
      <Menu>
        <MenuTrigger asChild>{children}</MenuTrigger>
        <MenuContent align="end" className="w-48">
          {!archived && (
            <MenuSub>
              <MenuSubTrigger className="max-md:min-h-11">
                <AlarmClock className="text-muted size-4" />
                Snooze
              </MenuSubTrigger>
              <MenuSubContent className="w-60">
                <SnoozeItems session={session} />
              </MenuSubContent>
            </MenuSub>
          )}
          <MenuItem className="max-md:min-h-11" onSelect={() => archive.mutate({ sessionId: session.id, archived: !archived })}>
            {archived ? <ArchiveRestore className="text-muted size-4" /> : <Archive className="text-muted size-4" />}
            {archived ? 'Restore' : 'Archive'}
          </MenuItem>
          <MenuSeparator />
          <MenuItem className="text-danger max-md:min-h-11" onSelect={() => setDeleting(true)}>
            <Trash2 className="size-4" />
            Delete…
          </MenuItem>
        </MenuContent>
      </Menu>
      <Dialog open={deleting} onOpenChange={setDeleting}>
        <DialogContent>
          <div className="pr-8">
            <DialogTitle className="truncate">Delete “{title}”</DialogTitle>
            <DialogDescription>
              The timeline, what it cost and what was asked go with it. Archiving is the move that keeps all of it.
            </DialogDescription>
          </div>
          {deleting && (
            <DeleteSessionConfirm
              sessionId={session.id}
              projectId={session.projectId}
              onCancel={() => setDeleting(false)}
              onDeleted={() => {
                setDeleting(false);
                // Nothing is left to look at when it was the one on screen.
                if (current === session.id) void navigate({ to: '/' });
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      {archive.error && <ActionError error={archive.error} outcome={archived ? 'Not restored' : 'Not archived'} className="px-2 text-xs" />}
    </>
  );
}
