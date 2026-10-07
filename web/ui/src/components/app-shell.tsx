import { Link, useParams } from '@tanstack/react-router';
import { Plus } from 'lucide-react';
import { useEffect, useState } from 'react';

import { useBackends, useCreateProject, useProjects, useSessions } from '@/api/queries';
import type { BackendInstance } from '@/api/types';
import { useStream } from '@/use-stream';
import { Badge, type BadgeTone } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input, Label } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn, humanise, when } from '@/lib/utils';

/**
 * The frame every view sits in: which Project, its Sessions, and the backends
 * that can run them.
 *
 * The user thinks in Projects and Sessions (spec section 34), so those are what
 * the navigation shows. Runs are an infrastructure detail and appear only where
 * someone has to choose or debug a backend.
 */
export function AppShell({ children }: { children: React.ReactNode }) {
  const { connected } = useStream();
  const projects = useProjects();
  const backends = useBackends();
  const params = useParams({ strict: false });

  const [projectId, setProjectId] = useState<string | undefined>(params.projectId);

  // The route wins over local selection: opening a Session link selects its
  // Project rather than leaving the sidebar pointing somewhere else.
  useEffect(() => {
    if (params.projectId) setProjectId(params.projectId);
  }, [params.projectId]);
  useEffect(() => {
    if (!projectId && projects.data?.length) setProjectId(projects.data[0].id);
  }, [projectId, projects.data]);

  const sessions = useSessions(projectId);

  return (
    <div className="grid h-full grid-cols-1 md:grid-cols-[17rem_1fr]">
      <aside className="border-border bg-surface flex max-h-[40vh] min-h-0 flex-col gap-4 border-b p-4 md:max-h-none md:border-r md:border-b-0">
        <header className="flex items-center justify-between">
          <Link to="/" className="text-base font-semibold">
            Threavia
          </Link>
          <Badge tone={connected ? 'ok' : 'neutral'} title="Realtime stream">
            {connected ? 'live' : 'offline'}
          </Badge>
        </header>

        <section className="space-y-2">
          <Label>Project</Label>
          <div className="flex gap-2">
            <Select value={projectId ?? ''} onValueChange={setProjectId}>
              <SelectTrigger className="flex-1">
                <SelectValue placeholder="No project" />
              </SelectTrigger>
              <SelectContent>
                {(projects.data ?? []).map((project) => (
                  <SelectItem key={project.id} value={project.id}>
                    {project.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <NewProjectButton onCreated={setProjectId} />
          </div>
          {projectId && (
            <Link
              to="/projects/$projectId"
              params={{ projectId }}
              className="text-accent block text-sm hover:underline"
            >
              Tasks, decisions, skills…
            </Link>
          )}
        </section>

        <section className="flex min-h-0 flex-1 flex-col gap-2">
          <div className="flex items-center justify-between">
            <Label>Sessions</Label>
            {projectId && (
              <Button asChild variant="ghost" size="icon" title="New session">
                <Link to="/sessions/new" search={{ projectId }}>
                  <Plus />
                </Link>
              </Button>
            )}
          </div>
          <ul className="min-h-0 flex-1 space-y-1 overflow-y-auto">
            {(sessions.data ?? []).map((session) => (
              <li key={session.id}>
                <Link
                  to="/sessions/$sessionId"
                  params={{ sessionId: session.id }}
                  className={cn(
                    'hover:bg-surface-2 block rounded-md px-2 py-1.5',
                    params.sessionId === session.id && 'bg-surface-2 border-accent border-l-2',
                  )}
                >
                  <span className="block truncate text-sm">{session.title || 'Untitled session'}</span>
                  <span className="text-muted text-xs">{when(session.updatedAt)}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>

        <section className="space-y-2">
          <Label>Backends</Label>
          <ul className="space-y-2">
            {(backends.data ?? []).map((backend) => (
              <BackendRow key={backend.id} backend={backend} />
            ))}
          </ul>
        </section>
      </aside>

      <main className="flex min-h-0 flex-col overflow-hidden">{children}</main>
    </div>
  );
}

function BackendRow({ backend }: { backend: BackendInstance }) {
  const tone: BadgeTone =
    backend.operationalStatus === 'READY'
      ? 'ok'
      : backend.operationalStatus === 'DEGRADED'
        ? 'warn'
        : 'neutral';

  return (
    <li className="text-sm">
      <div className="flex items-center gap-2">
        <span className="truncate">{backend.name}</span>
        <Badge tone={tone}>{humanise(backend.operationalStatus)}</Badge>
      </div>
      {/* Why it is in that state. A status alone sends someone to the logs. */}
      {(backend.conditions ?? []).map((condition) => (
        <p key={condition.type} className="text-danger mt-0.5 text-xs break-words">
          {condition.message || condition.reason || condition.type}
        </p>
      ))}
    </li>
  );
}

function NewProjectButton({ onCreated }: { onCreated: (projectId: string) => void }) {
  const create = useCreateProject();
  const [name, setName] = useState('');
  const [open, setOpen] = useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="secondary" size="icon" title="New project">
          <Plus />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogTitle>New project</DialogTitle>
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (!name.trim()) return;
            // The handler returns void, so the promise is discarded here rather
            // than handed to the DOM, which would never await it.
            void create.mutateAsync(name.trim()).then((project) => {
              onCreated(project.id);
              setName('');
              setOpen(false);
            });
          }}
        >
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="homelab"
            autoFocus
          />
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
