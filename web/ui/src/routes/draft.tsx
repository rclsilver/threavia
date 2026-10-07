import { useNavigate, useSearch } from '@tanstack/react-router';
import { useState } from 'react';

import { useBackends, useCreateDirectory, useDirectories, useStartSession } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { humanise } from '@/lib/utils';

const NONE = '__none__';

/**
 * A new Session is a local draft until the first send (spec section 34).
 *
 * Nothing exists in Core until the message goes out, and then it all exists at
 * once: Session, Run, Job and the message, in one transaction.
 */
export function DraftView() {
  const { projectId } = useSearch({ from: '/sessions/new' });
  const navigate = useNavigate();

  const backends = useBackends();
  const directories = useDirectories(projectId);
  const start = useStartSession();

  const [backendId, setBackendId] = useState('');
  const [directoryId, setDirectoryId] = useState(NONE);
  const [message, setMessage] = useState('');

  const usable = (backends.data ?? []).filter((backend) => backend.ownershipStatus !== 'REVOKED');
  const chosenBackend = backendId || usable[0]?.id || '';

  const submit = () => {
    const text = message.trim();
    if (!text || !chosenBackend) return;

    void start
      .mutateAsync({
        projectId,
        backendInstanceId: chosenBackend,
        workingDirectoryId: directoryId === NONE ? null : directoryId,
        message: text,
      })
      .then((result) =>
        navigate({ to: '/sessions/$sessionId', params: { sessionId: result.session.id } }),
      );
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-5">
      <header>
        <h2 className="text-lg font-semibold">New session</h2>
        <p className="text-muted text-sm">Nothing is created until you send the first message.</p>
      </header>

      <div className="max-w-xl space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="draft-backend">Backend</Label>
          <Select value={chosenBackend} onValueChange={setBackendId}>
            <SelectTrigger id="draft-backend">
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
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="draft-directory">Working directory</Label>
          <div className="flex gap-2">
            <Select value={directoryId} onValueChange={setDirectoryId}>
              <SelectTrigger id="draft-directory" className="flex-1">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>— none —</SelectItem>
                {(directories.data ?? []).map((directory) => (
                  <SelectItem key={directory.id} value={directory.id}>
                    {directory.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <NewDirectoryButton projectId={projectId} onCreated={setDirectoryId} />
          </div>
          <p className="text-muted text-xs">
            Optional. The backend locates it under its discovery roots when the Job starts, and asks
            you only if it cannot. It is the initial directory, not a boundary.
          </p>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="draft-message">First message</Label>
          <Textarea
            id="draft-message"
            rows={5}
            value={message}
            onChange={(event) => setMessage(event.target.value)}
            placeholder="Analyse ce projet"
          />
        </div>

        {start.error && <p className="text-danger text-sm">{(start.error).message}</p>}

        <Button
          variant="primary"
          disabled={start.isPending || !message.trim() || !chosenBackend}
          onClick={submit}
        >
          Start session
        </Button>
      </div>
    </div>
  );
}

function NewDirectoryButton({
  projectId,
  onCreated,
}: {
  projectId: string;
  onCreated: (directoryId: string) => void;
}) {
  const create = useCreateDirectory(projectId);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [gitRemote, setGitRemote] = useState('');

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button variant="secondary" size="icon" onClick={() => setOpen(true)} title="New directory">
        +
      </Button>
      <DialogContent>
        <DialogTitle>New known directory</DialogTitle>
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (!name.trim()) return;
            // The handler returns void, so the promise is discarded here rather
            // than handed to the DOM, which would never await it.
            void create
              .mutateAsync({ name: name.trim(), gitRemote: gitRemote.trim() || null })
              .then((directory) => {
                onCreated(directory.id);
                setName('');
                setGitRemote('');
                setOpen(false);
              });
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="directory-name">Name</Label>
            <Input
              id="directory-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="puppet"
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="directory-remote">Git remote</Label>
            <Input
              id="directory-remote"
              value={gitRemote}
              onChange={(event) => setGitRemote(event.target.value)}
              placeholder="git@github.com:owner/repo.git"
            />
            <p className="text-muted text-xs">
              Optional. It lets a backend offer to clone the directory when it is not there.
            </p>
          </div>
          <div className="flex justify-end gap-2">
            <DialogClose asChild>
              <Button variant="ghost" size="sm" type="button">
                Cancel
              </Button>
            </DialogClose>
            <Button variant="primary" size="sm" type="submit" disabled={create.isPending}>
              Create
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
