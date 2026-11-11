import { Link, useNavigate } from '@tanstack/react-router';
import { AlarmClock, Archive, ArchiveRestore, BookOpen, EllipsisVertical, FileDiff, Pin, PinOff, Settings2, Trash2 } from 'lucide-react';
import { useState, type ReactNode } from 'react';

import { useArchiveSession, useDeleteSession, usePinnedSessions, usePinSession, useSessionArtifacts } from '@/api/queries';
import type { BackendInstance, Session, Snapshot } from '@/api/types';
import { LearningCapture } from '@/components/learning-capture';
import { ConfirmLine } from '@/components/record-list';
import { SnoozeItems } from '@/components/snooze-menu';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuSub, MenuSubContent, MenuSubTrigger, MenuTrigger } from '@/components/ui/menu';
import { cn } from '@/lib/utils';
import { learningSource } from '../../../../clients/shared/learnings';
import { isSnoozed } from '../../../../clients/shared/sessions';

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

/** The confirmation a Session is deleted through, wherever the menu that asks for it sits. */
function DeleteSessionDialog({ session, current, open, onOpenChange }: { session: Session; current?: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate();
  const title = session.title || 'Untitled session';
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <div className="pr-8">
          <DialogTitle className="truncate">Delete “{title}”</DialogTitle>
          <DialogDescription>
            The timeline, what it cost and what was asked go with it. Archiving is the move that keeps all of it.
          </DialogDescription>
        </div>
        {open && (
          <DeleteSessionConfirm
            sessionId={session.id}
            projectId={session.projectId}
            onCancel={() => onOpenChange(false)}
            onDeleted={() => {
              onOpenChange(false);
              // Nothing is left to look at when it was the one on screen.
              if (current === session.id) void navigate({ to: '/' });
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

/**
 * Everything that can be done to a Session from the list, without opening
 * it: put it off, put it away, or get rid of it.
 */
export function SessionMenu({ session, current, children }: { session: Session; current?: string; children: ReactNode }) {
  const archive = useArchiveSession();
  const [deleting, setDeleting] = useState(false);
  const archived = session.status === 'ARCHIVED';

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
      <DeleteSessionDialog session={session} current={current} open={deleting} onOpenChange={setDeleting} />
      {archive.error && <ActionError error={archive.error} outcome={archived ? 'Not restored' : 'Not archived'} className="px-2 text-xs" />}
    </>
  );
}

/**
 * Everything that can be done to the Session on screen, behind one button in
 * its header: the conversation keeps the room, each action is a line here.
 */
export function SessionHeaderMenu({ snapshot, backend }: { snapshot: Snapshot; backend?: BackendInstance }) {
  const session = snapshot.session;
  const archive = useArchiveSession();
  const pinned = usePinnedSessions();
  const pin = usePinSession();
  const [deleting, setDeleting] = useState(false);
  const [capturing, setCapturing] = useState(false);
  const archived = session.status === 'ARCHIVED';
  const isPinned = pinned.data ? pinned.data.some((entry) => entry.id === session.id) : Boolean(session.pinnedAt);
  const snoozed = isSnoozed(session);
  const item = 'max-md:min-h-11';

  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-11 md:size-8" title="Session actions" aria-label="Session actions" data-tour="session-menu">
            <EllipsisVertical />
          </Button>
        </MenuTrigger>
        <MenuContent align="end" className="w-60">
          <MenuItem asChild className={item}>
            <Link to="/sessions/$sessionId/review" params={{ sessionId: session.id }} data-tour="open-review">
              <FileDiff className="text-muted size-4" />
              Review changes
            </Link>
          </MenuItem>
          {learningSource(snapshot) && (
            <MenuItem className={item} onSelect={() => setCapturing(true)} data-tour="capture-learnings">
              <BookOpen className="text-muted size-4" />
              Capture learnings…
            </MenuItem>
          )}
          <MenuSeparator />
          {!archived && (
            <MenuSub>
              <MenuSubTrigger className={cn(item, snoozed && 'text-accent')}>
                <AlarmClock className={cn('size-4', !snoozed && 'text-muted')} />
                {snoozed ? 'Snoozed' : 'Snooze'}
              </MenuSubTrigger>
              <MenuSubContent className="w-60">
                <SnoozeItems session={session} />
              </MenuSubContent>
            </MenuSub>
          )}
          <MenuItem className={item} onSelect={() => pin.mutate({ session, pinned: !isPinned })}>
            {isPinned ? <PinOff className="text-muted size-4" /> : <Pin className="text-muted size-4" />}
            {isPinned ? 'Unpin from the sidebar' : 'Pin to the sidebar'}
          </MenuItem>
          <MenuItem className={item} onSelect={() => archive.mutate({ sessionId: session.id, archived: !archived })}>
            {archived ? <ArchiveRestore className="text-muted size-4" /> : <Archive className="text-muted size-4" />}
            {archived ? 'Restore from the archive' : 'Archive'}
          </MenuItem>
          <MenuSeparator />
          <MenuItem asChild className={item}>
            <Link to="/sessions/$sessionId/settings/$tab" params={{ sessionId: session.id, tab: 'permissions' }}>
              <Settings2 className="text-muted size-4" />
              Session settings
            </Link>
          </MenuItem>
          <MenuSeparator />
          <MenuItem className={cn('text-danger', item)} onSelect={() => setDeleting(true)}>
            <Trash2 className="size-4" />
            Delete…
          </MenuItem>
        </MenuContent>
      </Menu>
      <LearningCapture snapshot={snapshot} backend={backend} open={capturing} onOpenChange={setCapturing} />
      <DeleteSessionDialog session={session} current={session.id} open={deleting} onOpenChange={setDeleting} />
      <ActionError error={archive.error} outcome={archived ? 'Not restored' : 'Not archived'} className="text-xs" />
    </>
  );
}
