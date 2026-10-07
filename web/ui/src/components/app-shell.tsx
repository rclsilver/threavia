import { Link, useParams } from '@tanstack/react-router';
import {
  BookText,
  FileText,
  History,
  ListChecks,
  Package,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Sparkles,
} from 'lucide-react';
import { useEffect, useState } from 'react';

import {
  useBackends,
  useClaimBackend,
  useCreateProject,
  useIssueBackendToken,
  useMe,
  useProjects,
  useRevokeBackend,
  useSessions,
  useTasks,
} from '@/api/queries';
import type { BackendInstance, Session } from '@/api/types';
import { useWideLayout } from '@/use-layout';
import { SelectedProject } from '@/use-project';
import { useStream } from '@/use-stream';
import { Logo } from '@/components/logo';
import { Badge, type BadgeTone } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
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
        {/* The mark says whose window this is and goes nowhere: every
            destination is one of the entries below, and a logo that quietly
            lands on one of them makes that entry look like two places. */}
        <header className={cn('flex items-center gap-2', collapsed && 'flex-col')}>
          {collapsed ? (
            <span title="Threavia" className="py-1">
              <Logo className="size-6" />
            </span>
          ) : (
            <>
              <span className="flex items-center gap-2 text-base font-semibold">
                <Logo className="size-6" />
                Threavia
              </span>
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
          // The rail keeps what is not a list: the way back, the work still
          // owed, and the person using it. A session title cut to three
          // characters would be worse than not showing one.
          <>
            {projectId && <ProjectNav projectId={projectId} collapsed />}
            <UserMenu backends={backends.data ?? []} collapsed />
          </>
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
          {projectId && <ProjectNav projectId={projectId} />}
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

            <UserMenu backends={backends.data ?? []} />
          </>
        )}
      </aside>

      <main className="flex min-h-0 flex-col overflow-hidden">
        {/* Which Project the sidebar is pointing at, for the views that have no
            Project in their own URL. */}
        <SelectedProject.Provider value={projectId}>{children}</SelectedProject.Provider>
      </main>
    </div>
  );
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

/**
 * What every Project navigation entry looks like, and how it says it is the
 * one on screen.
 */
const nav = {
  className: 'hover:bg-surface-2 flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm',
  activeProps: { className: 'bg-surface-2 font-medium' },
};

const rail = {
  className: 'hover:bg-surface-2 text-muted hover:text-text flex size-8 items-center justify-center rounded-lg [&_svg]:size-4',
  activeProps: { className: 'bg-surface-2 text-text' },
};

/**
 * The parts of a Project, in the order they are usually wanted.
 *
 * Tasks are not in here: they carry a count, so they have a component of their
 * own rather than a row that pretends to be like the others.
 */
const SECTIONS = [
  { to: '/projects/$projectId/memory', label: 'Memory', Icon: BookText },
  { to: '/projects/$projectId/artifacts', label: 'Artifacts', Icon: Package },
  { to: '/projects/$projectId/skills', label: 'Skills', Icon: Sparkles },
  { to: '/projects/$projectId/instructions', label: 'Instructions', Icon: FileText },
  { to: '/projects/$projectId/audit', label: 'Audit', Icon: History },
] as const;

/**
 * Where everything a Project holds is reached from.
 *
 * Folded, it keeps every entry rather than only the tasks: a rail that drops
 * four of its five destinations is not folded, it is emptied, and the way back
 * to them would be unfolding the sidebar first.
 */
function ProjectNav({ projectId, collapsed = false }: { projectId: string; collapsed?: boolean }) {
  if (collapsed) {
    return (
      <nav className="flex flex-col items-center gap-1">
        <OpenTasks projectId={projectId} collapsed />
        {SECTIONS.map(({ to, label, Icon }) => (
          <Link key={to} to={to} params={{ projectId }} title={label} {...rail}>
            <Icon />
          </Link>
        ))}
      </nav>
    );
  }

  return (
    <nav className="space-y-0.5">
      <OpenTasks projectId={projectId} />
      {SECTIONS.map(({ to, label, Icon }) => (
        <Link key={to} to={to} params={{ projectId }} {...nav}>
          <Icon className="text-muted size-4" />
          {label}
        </Link>
      ))}
    </nav>
  );
}

/**
 * The work still owed on this Project, as a count that is always on screen.
 *
 * Tasks are what an agent and a person agree is left to do, and they were a tab
 * behind a link: something you had to already know about to go and look at. The
 * count belongs where the eye passes anyway, folded sidebar included, because a
 * number nobody sees cannot remind anyone of anything.
 */
function OpenTasks({ projectId, collapsed = false }: { projectId: string; collapsed?: boolean }) {
  const tasks = useTasks(projectId, false);
  const open = tasks.data?.length ?? 0;
  const title = `${open} open task${open === 1 ? '' : 's'}`;

  if (collapsed) {
    return (
      <Button asChild variant="ghost" size="icon" title={title} className="relative self-center">
        <Link to="/projects/$projectId/tasks" params={{ projectId }}>
          <ListChecks />
          {open > 0 && (
            <span className="bg-accent text-accent-text ring-surface absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[0.625rem] font-medium ring-2">
              {open > 99 ? '99+' : open}
            </span>
          )}
        </Link>
      </Button>
    );
  }

  return (
    <Link to="/projects/$projectId/tasks" params={{ projectId }} title={title} {...nav}>
      <ListChecks className="text-muted size-4" />
      <span className="flex-1">Tasks</span>
      {/* Nothing left is worth saying too: an empty badge reads as a bug. */}
      <Badge tone={open > 0 ? 'accent' : 'neutral'}>{open}</Badge>
    </Link>
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
 * Who is using Threavia, and the machines that work for them.
 *
 * It sits at the foot of the sidebar because that is where a person looks for
 * themselves, and it opens what it holds in one move: the whole of it fits on
 * one panel, and a menu whose every branch leads to the same panel is a step
 * that exists only to be clicked through. The warning dot is the part that
 * cannot wait for anyone to open anything: a backend that is not ready is why
 * nothing is answering.
 */
function UserMenu({
  backends,
  collapsed = false,
}: {
  backends: BackendInstance[];
  collapsed?: boolean;
}) {
  const me = useMe();
  // Read here because this is the one control mounted on every page: asking for
  // it applies the stored choice before anything is laid out.
  const [wide, setWide] = useWideLayout();

  // Authenticated or not is not a detail to smooth over: in ModeNone nobody was
  // asked to prove anything, and the id is whatever Core was configured with.
  const anonymous = !me.data || me.data.authMode === 'none';
  const name = anonymous ? 'anonymous' : me.data.name || me.data.email || me.data.userId;
  const unwell = backends.filter((backend) => backend.operationalStatus !== 'READY');

  const warning = unwell.length > 0 ? `, ${unwell.length} backend(s) not ready` : '';
  const title = `${name} — account and backends${warning}`;

  return (
    <Dialog>
      <DialogTrigger asChild>
        {collapsed ? (
          // In the rail the avatar is the whole control, pushed to the foot
          // where it sits when the sidebar is open, so folding the sidebar
          // moves nothing a hand has already learned.
          <button
            type="button"
            title={title}
            className="hover:bg-surface-2 relative mt-auto self-center rounded-full p-1"
          >
            <span className="bg-surface-2 text-muted flex size-7 items-center justify-center rounded-full text-xs font-medium uppercase">
              {name.slice(0, 1)}
            </span>
            {unwell.length > 0 && (
              <span className="bg-warn ring-surface absolute top-0.5 right-0.5 size-2 rounded-full ring-2" />
            )}
          </button>
        ) : (
          <button
            type="button"
            title={title}
            className="hover:bg-surface-2 border-border flex w-full items-center gap-2 rounded-lg border px-2 py-1.5 text-left text-sm"
          >
            <span className="bg-surface-2 text-muted flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-medium uppercase">
              {name.slice(0, 1)}
            </span>
            <span className="min-w-0 flex-1 truncate">{name}</span>
            {unwell.length > 0 && <span className="bg-warn size-1.5 shrink-0 rounded-full" />}
          </button>
        )}
      </DialogTrigger>

      <DialogContent className="w-[min(40rem,calc(100vw-2rem))]">
        <div>
          <DialogTitle>{name}</DialogTitle>
          <DialogDescription>
            {anonymous
              ? 'No authentication is configured, so everything here is attributed to one user.'
              : `Signed in with ${me.data?.authMode} as ${me.data?.userId}.`}
          </DialogDescription>
        </div>

        <section className="border-border space-y-2 border-t pt-4">
          <h3 className="text-sm font-medium">Appearance</h3>
          <CheckboxField checked={wide} onCheckedChange={setWide}>
            use the full width of the window
          </CheckboxField>
          <p className="text-muted text-xs">
            A conversation is held to a reading width by default, because a line running
            the whole of a wide screen loses the eye on the way back to the left margin.
            Turn this on and nothing is held back.
          </p>
        </section>

        <section className="border-border space-y-3 border-t pt-4">
          <h3 className="text-sm font-medium">Backends</h3>
          <BackendsPane backends={backends} />
        </section>
      </DialogContent>
    </Dialog>
  );
}

/** The machines that can run work, and what can be done about them. */
function BackendsPane({ backends }: { backends: BackendInstance[] }) {
  const revoke = useRevokeBackend();
  const issue = useIssueBackendToken();
  const claim = useClaimBackend();
  const [claimCode, setClaimCode] = useState('');
  const [confirming, setConfirming] = useState<string | null>(null);

  return (
    <div className="space-y-5">
      <ul className="space-y-2">
        {backends.length === 0 && (
          <li className="text-muted text-sm">
            None yet. Issue a registration token below and start a backend with it.
          </li>
        )}
        {backends.map((backend) => (
          <li key={backend.id} className="border-border rounded-[--radius-card] border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{backend.name}</span>
              <Badge tone={backendTone(backend)}>{humanise(backend.operationalStatus)}</Badge>
              {backend.ownershipStatus !== 'CLAIMED' && (
                <Badge tone="warn">{humanise(backend.ownershipStatus)}</Badge>
              )}
              {backend.ownershipStatus !== 'REVOKED' &&
                (confirming === backend.id ? (
                  <span className="ml-auto flex items-center gap-2 text-xs">
                    <span className="text-muted">Revoke its credential?</span>
                    <Button variant="ghost" size="sm" onClick={() => setConfirming(null)}>
                      No
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      disabled={revoke.isPending}
                      onClick={() => {
                        revoke.mutate(backend.id);
                        setConfirming(null);
                      }}
                    >
                      Revoke
                    </Button>
                  </span>
                ) : (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="ml-auto"
                    onClick={() => setConfirming(backend.id)}
                  >
                    Revoke
                  </Button>
                ))}
            </div>
            <p className="text-muted mt-1 text-xs">
              {(backend.capabilities ?? []).map(humanise).join(', ') || 'no capability'} ·{' '}
              {backend.capacity?.activeRuns ?? 0}/{backend.capacity?.maxConcurrentRuns ?? 0} runs ·
              last seen {when(backend.lastHeartbeatAt) || 'never'}
            </p>
            {(backend.conditions ?? []).map((condition) => (
              <p key={condition.type} className="text-danger mt-1 text-xs break-words">
                {condition.message || condition.reason || condition.type}
              </p>
            ))}
          </li>
        ))}
      </ul>

      {revoke.error && <p className="text-danger text-sm">{revoke.error.message}</p>}

      <section className="border-border space-y-2 border-t pt-4">
        <h3 className="text-sm font-medium">Add a backend</h3>
        <p className="text-muted text-xs">
          A one-shot token, good for one registration. Core returns it once and cannot show it
          again.
        </p>
        <Button
          variant="secondary"
          size="sm"
          disabled={issue.isPending}
          onClick={() => issue.mutate({ label: 'issued from the web client', ttlSeconds: 3600 })}
        >
          New registration token
        </Button>

        {issue.data && (
          <div className="space-y-1">
            <code className="bg-surface-2 block rounded-md p-2 text-xs break-all">
              {issue.data.token}
            </code>
            <p className="text-muted text-xs">Valid until {when(issue.data.expiresAt)}.</p>
          </div>
        )}
        {issue.error && <p className="text-danger text-sm">{issue.error.message}</p>}
      </section>

      <section className="border-border space-y-2 border-t pt-4">
        <h3 className="text-sm font-medium">Claim an instance</h3>
        <p className="text-muted text-xs">
          A backend that registered without a user prints a one-time code. Claiming it makes it
          yours.
        </p>
        <div className="flex gap-2">
          <Input
            value={claimCode}
            onChange={(event) => setClaimCode(event.target.value)}
            placeholder="claim code"
          />
          <Button
            variant="secondary"
            disabled={!claimCode.trim() || claim.isPending}
            onClick={() => claim.mutate(claimCode.trim(), { onSuccess: () => setClaimCode('') })}
          >
            Claim
          </Button>
        </div>
        {claim.error && <p className="text-danger text-sm">{claim.error.message}</p>}
      </section>
    </div>
  );
}

function backendTone(backend: BackendInstance): BadgeTone {
  if (backend.ownershipStatus === 'REVOKED') return 'danger';
  if (backend.operationalStatus === 'READY') return 'ok';
  if (backend.operationalStatus === 'DEGRADED') return 'warn';
  return 'neutral';
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
