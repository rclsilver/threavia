import { Link, useParams } from '@tanstack/react-router';
import { PanelLeftClose, PanelLeftOpen, Plus } from 'lucide-react';
import { useEffect, useState } from 'react';

import { useBackends, useCreateProject, useProjects, useSessions } from '@/api/queries';
import type { BackendInstance, Session } from '@/api/types';
import { useStream } from '@/use-stream';
import { Badge, type BadgeTone } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogClose, DialogContent, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input, Label } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn, humanise } from '@/lib/utils';

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
  const [collapsed, setCollapsed] = useCollapsed();

  // Collapsed, the sidebar keeps a rail rather than disappearing: the control
  // that brings it back has to live somewhere a person can find without
  // guessing, and the main view keeps a left edge of its own.
  return (
    <div
      className={cn(
        'grid h-full grid-cols-1',
        collapsed ? 'md:grid-cols-[3rem_1fr]' : 'md:grid-cols-[17rem_1fr]',
      )}
    >
      <aside
        className={cn(
          'border-border bg-surface flex max-h-[40vh] min-h-0 flex-col border-b md:max-h-none md:border-r md:border-b-0',
          collapsed ? 'gap-2 p-2' : 'gap-4 p-4',
        )}
      >
        <header className={cn('flex items-center gap-2', collapsed && 'flex-col')}>
          {!collapsed && (
            <>
              <Link to="/" className="text-base font-semibold">
                Threavia
              </Link>
              <Badge tone={connected ? 'ok' : 'neutral'} title="Realtime stream" className="ml-auto">
                {connected ? 'live' : 'offline'}
              </Badge>
            </>
          )}
          <Button
            variant="ghost"
            size="icon"
            title={collapsed ? 'Show the sidebar' : 'Hide the sidebar'}
            onClick={() => setCollapsed(!collapsed)}
          >
            {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
          </Button>
        </header>

        {collapsed ? (
          // Nothing else fits legibly in a rail, and half a session title is
          // worse than none: the rail is a way back, not a smaller sidebar.
          <span className="sr-only">Sidebar hidden</span>
        ) : (
          <>
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
          {/*
           * Grouped by when they were last touched, and titled by nothing else:
           * a timestamp under every row doubles the height of the list to
           * answer a question nobody asks per session, only per group.
           */}
          <div className="-mx-1 min-h-0 flex-1 space-y-4 overflow-y-auto px-1">
            {groupSessions(sessions.data ?? []).map((group) => (
              <div key={group.label} className="space-y-0.5">
                <p className="text-muted/80 px-2 pb-1 text-xs">{group.label}</p>
                <ul>
                  {group.sessions.map((session) => (
                    <li key={session.id}>
                      <Link
                        to="/sessions/$sessionId"
                        params={{ sessionId: session.id }}
                        title={session.title || 'Untitled session'}
                        className={cn(
                          'hover:bg-surface-2 block truncate rounded-lg px-2 py-1.5 text-sm',
                          params.sessionId === session.id && 'bg-surface-2 font-medium',
                        )}
                      >
                        {session.title || 'Untitled session'}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </section>

            <section className="space-y-2">
              <Label>Backends</Label>
              <ul className="space-y-2">
                {(backends.data ?? []).map((backend) => (
                  <BackendRow key={backend.id} backend={backend} />
                ))}
              </ul>
            </section>
          </>
        )}
      </aside>

      <main className="flex min-h-0 flex-col overflow-hidden">{children}</main>
    </div>
  );
}

const COLLAPSED_KEY = 'threavia.sidebar.collapsed';

/**
 * Whether the sidebar is folded away, remembered between visits.
 *
 * Someone who hid it did so because they wanted the room, and having to hide it
 * again on every reload is the same annoyance repeated. Browser storage can be
 * unavailable or refused, and the sidebar showing is the state that still works
 * when it is.
 */
function useCollapsed(): [boolean, (collapsed: boolean) => void] {
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return window.localStorage.getItem(COLLAPSED_KEY) === 'true';
    } catch {
      return false;
    }
  });

  const remember = (next: boolean) => {
    setCollapsed(next);
    try {
      window.localStorage.setItem(COLLAPSED_KEY, String(next));
    } catch {
      // Nothing to do: the preference lasts this visit instead of every visit.
    }
  };

  return [collapsed, remember];
}

/**
 * Sessions bucketed by how recently they were touched.
 *
 * The buckets are the ones a person actually uses to find something again: what
 * they were doing a moment ago, earlier today, this week, before that.
 */
function groupSessions(sessions: Session[]): { label: string; sessions: Session[] }[] {
  const now = Date.now();
  const day = 24 * 60 * 60 * 1000;
  const buckets: { label: string; within: number; sessions: Session[] }[] = [
    { label: 'Today', within: day, sessions: [] },
    { label: 'Previous 7 days', within: 7 * day, sessions: [] },
    { label: 'Previous 30 days', within: 30 * day, sessions: [] },
    { label: 'Older', within: Infinity, sessions: [] },
  ];

  for (const session of sessions) {
    const age = now - Date.parse(session.updatedAt);
    // An unreadable date compares false against every bound and lands in the
    // last bucket, which beats disappearing from the list.
    const bucket = buckets.find((candidate) => age < candidate.within) ?? buckets[buckets.length - 1];
    bucket.sessions.push(session);
  }
  return buckets.filter((bucket) => bucket.sessions.length > 0);
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
