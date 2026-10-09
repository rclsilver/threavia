import { Link, useLocation, useNavigate, useParams } from '@tanstack/react-router';
import {
  Archive,
  ArchiveRestore,
  BookText,
  Bell,
  Check,
  ChevronsUpDown,
  Clock,
  FileText,
  History,
  Inbox,
  ListChecks,
  LoaderCircle,
  Monitor,
  Menu as MenuIcon,
  MessageCircleQuestion,
  Package,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  ShieldAlert,
  Server,
  ShieldCheck,
  Sparkles,
  X,
} from 'lucide-react';
import { useEffect, useState } from 'react';

import {
  useAttention,
  useBackends,
  useCreateProject,
  useMe,
  useProjects,
  useSessions,
  useSnapshot,
  useTasks,
} from '@/api/queries';
import type { BackendInstance, Project, Session } from '@/api/types';
import { DEMO } from '@/demo/mode';
import { SelectedProject } from '@/use-project';
import { useStream } from '@/use-stream';
import { JobEndedNotices } from '@/components/job-ended-notices';
import { Logo } from '@/components/logo';
import { ActionError } from '@/components/ui/action-error';
import { Avatar } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input, Label } from '@/components/ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '@/components/ui/menu';
import { cn } from '@/lib/utils';

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
  // Project rather than leaving the sidebar pointing somewhere else. A
  // Session's URL carries no Project, but the Session knows its own: opened
  // from a link, from what waits, or from a notification, the sidebar follows
  // it. The snapshot is the one the Session view reads, so this asks nothing.
  const snapshot = useSnapshot(params.sessionId);
  const draftProject = useLocation({
    select: (location) => (location.pathname === '/sessions/new' ? (location.search as { projectId?: string }).projectId : undefined),
  });
  const routeProject = params.projectId ?? snapshot.data?.session.projectId ?? (draftProject || undefined);
  useEffect(() => {
    if (routeProject) setProjectId(routeProject);
  }, [routeProject]);
  // Nothing chosen and nothing in the route: the first Project. Never over
  // the route's, which can arrive in the same render as the list of Projects.
  useEffect(() => {
    if (!projectId && !routeProject && projects.data?.length) setProjectId(projects.data[0].id);
  }, [projectId, routeProject, projects.data]);

  const [showArchived, setShowArchived] = useState(false);
  // Archived Sessions are read from the same list, because asking for them
  // separately would mean a second shape of answer for the same question.
  const sessions = useSessions(projectId, showArchived);
  const shown = (sessions.data ?? []).filter((session) =>
    showArchived ? session.status === 'ARCHIVED' : session.status !== 'ARCHIVED',
  );
  const [collapsed, setCollapsed] = useCollapsed();

  // Where each Project was left, so coming back to one lands there. Only a
  // Session listed under the selected Project is recorded against it.
  useEffect(() => {
    const session = params.sessionId;
    if (!session || !projectId) return;
    if (sessions.data?.some((candidate) => candidate.id === session)) rememberSession(projectId, session);
  }, [params.sessionId, projectId, sessions.data]);

  // Choosing another Project goes to it. Staying on a Session of the one just
  // left, with the sidebar listing the other, showed two Projects at once. A
  // Project page becomes the same page of the other Project; anything else
  // becomes the Session last opened there, or a new one. What waits for a
  // decision is every Project's at once, so it stays.
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const switchProject = (id: string) => {
    if (id === projectId) return;
    setProjectId(id);
    if (pathname === '/waiting') return;
    if (params.projectId) {
      void navigate({ to: '.', params: (previous) => ({ ...previous, projectId: id }) });
      return;
    }
    const last = lastSession(id);
    void (last
      ? navigate({ to: '/sessions/$sessionId', params: { sessionId: last } })
      : navigate({ to: '/sessions/new', search: { projectId: id } }));
  };

  // What waits for the user, by Session. The attention route is already the
  // live answer to that question, kept current by the stream.
  const attention = useAttention();
  const waiting = new Map<string, 'validation' | 'input'>();
  for (const request of attention.data?.userInputs ?? []) waiting.set(request.scope.sessionId, 'input');
  // A permission outranks a question: it is the one blocking a tool call.
  for (const request of attention.data?.validations ?? []) waiting.set(request.scope.sessionId, 'validation');
  // Requests, not sessions: one session may ask twice, and each needs an answer.
  const waitingCount = (attention.data?.validations.length ?? 0) + (attention.data?.userInputs.length ?? 0);

  // On a phone the sidebar is a drawer over the content, closed until asked
  // for: a column of navigation beside a conversation leaves neither readable
  // on a screen that narrow. It closes again on the way to wherever was picked.
  const wide = useMediaQuery('(min-width: 768px)');
  const [drawer, setDrawer] = useState(false);
  useEffect(() => {
    setDrawer(false);
  }, [pathname, wide]);
  // Escape closes the drawer, as it closes any surface laid over the page.
  useEffect(() => {
    if (!drawer) return;
    const onKey = (event: KeyboardEvent) => event.key === 'Escape' && setDrawer(false);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [drawer]);
  // The rail is a desktop affordance; a drawer is either open in full or gone.
  const folded = collapsed && wide;

  // Collapsed, the sidebar keeps a rail rather than disappearing: the control
  // that brings it back has to live somewhere a person can find without
  // guessing, and the main view keeps a left edge of its own.
  return (
    <div
      className={cn(
        // The dynamic viewport height, so the composer of a phone is never
        // under its browser's toolbar.
        'grid h-dvh grid-cols-1 grid-rows-[auto_1fr] md:grid-rows-1',
        folded ? 'md:grid-cols-[3rem_1fr]' : 'md:grid-cols-[17rem_1fr]',
      )}
    >
      {/* What a phone keeps of the sidebar: the way into it, and whether the
          stream is up. */}
      <div className="border-border bg-surface flex items-center gap-2 border-b px-2 py-1.5 md:hidden">
        <Button variant="ghost" size="icon" title="Open the menu" onClick={() => setDrawer(true)}>
          <MenuIcon />
        </Button>
        <Logo className="h-5 w-auto" />
        <span className="text-sm font-semibold">Threavia</span>
        {/* The way to what waits, one tap from anywhere: the reason the phone
            was taken out of the pocket. */}
        {waitingCount > 0 && (
          <Link
            to="/waiting"
            className="bg-warn/15 text-warn-text ml-1 inline-flex h-9 items-center gap-1.5 rounded-md px-2.5 text-sm font-medium"
          >
            <Inbox className="size-4" />
            {waitingCount} waiting
          </Link>
        )}
        <StreamBadge connected={connected} />
      </div>

      {drawer && (
        <div
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          aria-hidden
          onClick={() => setDrawer(false)}
        />
      )}

      <aside
        // Closed, the drawer is off-screen but would still take the keyboard's
        // focus and a screen reader's attention; inert takes it out of both.
        inert={!wide && !drawer}
        className={cn(
          'border-border bg-surface flex min-h-0 flex-col border-r',
          // A drawer below md, a column of the grid from md up.
          'fixed inset-y-0 left-0 z-40 w-[min(20rem,85vw)] shadow-xl transition-transform',
          'md:static md:z-auto md:w-auto md:translate-x-0 md:shadow-none md:transition-none',
          drawer ? 'translate-x-0' : '-translate-x-full',
          folded ? 'gap-2 p-2' : 'gap-4 p-4',
        )}
      >
        {/* The mark says whose window this is and goes nowhere: every
            destination is one of the entries below, and a logo that quietly
            lands on one of them makes that entry look like two places. */}
        <header className={cn('flex items-center gap-2', folded && 'flex-col')}>
          {folded ? (
            <span title="Threavia" className="py-1">
              <Logo className="w-7" />
            </span>
          ) : (
            <>
              <span className="flex items-center gap-2 text-base font-semibold">
                <Logo className="h-5 w-auto" />
                Threavia
              </span>
              <StreamBadge connected={connected} />
            </>
          )}
          {wide ? (
            <Button
              variant="ghost"
              size="icon"
              title={collapsed ? 'Show the sidebar' : 'Hide the sidebar'}
              onClick={() => setCollapsed(!collapsed)}
            >
              {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
            </Button>
          ) : (
            <Button variant="ghost" size="icon" title="Close the menu" onClick={() => setDrawer(false)}>
              <X />
            </Button>
          )}
        </header>

        {folded ? (
          // The rail keeps what is not a list: the way back, the work still
          // owed, and the person using it. A session title cut to three
          // characters would be worse than not showing one.
          <>
            <WaitingLink count={waitingCount} active={pathname === '/waiting'} collapsed />
            {projectId && <ProjectNav projectId={projectId} collapsed />}
            <UserMenu backends={backends.data ?? []} collapsed />
          </>
        ) : (
          <>
        <WaitingLink count={waitingCount} active={pathname === '/waiting'} />
        <section className="space-y-2">
          <ProjectSwitcher projects={projects.data ?? []} current={projectId} onSwitch={switchProject} />
          {projectId && <ProjectNav projectId={projectId} />}
        </section>

        <section data-tour="sessions" className="flex min-h-0 flex-1 flex-col gap-2">
          <div className="flex items-center justify-between">
            <Label>{showArchived ? 'Archived sessions' : 'Sessions'}</Label>
            <div className="flex items-center">
              <Button
                variant="ghost"
                size="icon"
                title={showArchived ? 'Back to active sessions' : 'Show archived sessions'}
                onClick={() => setShowArchived((shown) => !shown)}
              >
                {showArchived ? <ArchiveRestore /> : <Archive />}
              </Button>
              {projectId && !showArchived && (
                <Button asChild variant="ghost" size="icon" title="New session">
                  <Link to="/sessions/new" search={{ projectId }}>
                    <Plus />
                  </Link>
                </Button>
              )}
            </div>
          </div>
          {/*
           * Grouped by when they were last touched, and titled by nothing else:
           * a timestamp under every row doubles the height of the list to
           * answer a question nobody asks per session, only per group.
           */}
          <div className="-mx-1 min-h-0 flex-1 space-y-4 overflow-y-auto px-1">
            {shown.length === 0 && (
              <p className="text-muted px-2 text-sm">
                {showArchived ? 'Nothing archived.' : 'No sessions yet.'}
              </p>
            )}
            {groupSessions(shown, waiting).map((group) => (
              <div key={group.label} className="space-y-0.5">
                <p className={cn('px-2 pb-1 text-xs', group.label === 'Waiting for you' ? 'text-warn-text font-medium' : 'text-muted')}>
                  {group.label}
                </p>
                <ul>
                  {group.sessions.map((session) => (
                    <li key={session.id}>
                      <Link
                        to="/sessions/$sessionId"
                        params={{ sessionId: session.id }}
                        title={session.title || 'Untitled session'}
                        className={cn(
                          'hover:bg-surface-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-sm',
                          params.sessionId === session.id && 'bg-surface-2 font-medium',
                        )}
                      >
                        <span className="min-w-0 flex-1 truncate">
                          {session.title || 'Untitled session'}
                        </span>
                        <SessionState state={sessionState(session, waiting)} />
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
      <JobEndedNotices />
    </div>
  );
}

type SessionActivity = 'validation' | 'input' | 'running' | 'stopping' | 'queued' | 'idle';

function sessionState(
  session: Session,
  waiting: Map<string, 'validation' | 'input'>,
): SessionActivity {
  const asked = waiting.get(session.id);
  if (asked) return asked;
  switch (session.activeJobStatus) {
    case undefined:
      return 'idle';
    case 'QUEUED':
      return 'queued';
    case 'CANCELLING':
      return 'stopping';
    default:
      return 'running';
  }
}

/**
 * Which Session is working and which one waits for the user, at a glance.
 *
 * The two that wait are the loud ones, in the warning colour: they are the only
 * states a person has to act on. Working is a quiet pulse, and a Session with
 * nothing going shows nothing at all, which is most of the list.
 */
function SessionState({ state }: { state: SessionActivity }) {
  switch (state) {
    case 'validation':
      return <ShieldAlert className="text-warn-text size-4 shrink-0" aria-label="Waiting for your approval" />;
    case 'input':
      return (
        <MessageCircleQuestion className="text-warn-text size-4 shrink-0" aria-label="Waiting for your answer" />
      );
    case 'running':
      return <LoaderCircle className="text-accent size-3.5 shrink-0 animate-spin" aria-label="Working" />;
    case 'stopping':
      return <LoaderCircle className="text-muted size-3.5 shrink-0 animate-spin" aria-label="Stopping" />;
    case 'queued':
      return <Clock className="text-muted size-3.5 shrink-0" aria-label="Queued" />;
    case 'idle':
      return null;
  }
}

/**
 * Sessions bucketed by how recently they were touched.
 *
 * The buckets are the ones a person actually uses to find something again: what
 * they were doing a moment ago, earlier today, this week, before that.
 */
function groupSessions(
  sessions: Session[],
  waiting: Map<string, 'validation' | 'input'>,
): { label: string; sessions: Session[] }[] {
  const now = Date.now();
  const day = 24 * 60 * 60 * 1000;
  // What waits for a decision comes first, whatever its age: it is the one
  // row a person opening the list is looking for.
  const pinned = sessions.filter((session) => waiting.has(session.id));
  sessions = sessions.filter((session) => !waiting.has(session.id));
  const buckets: { label: string; within: number; sessions: Session[] }[] = [
    { label: 'Waiting for you', within: -1, sessions: pinned },
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
  className: 'hover:bg-surface-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-sm',
  activeProps: { className: 'bg-surface-2 font-medium' },
};

const rail = {
  className: 'hover:bg-surface-2 text-muted hover:text-text flex size-8 items-center justify-center rounded-md [&_svg]:size-4',
  activeProps: { className: 'bg-surface-2 text-text' },
};

/**
 * The way to everything waiting for a decision, across projects, above the
 * project it would otherwise hide behind. Quiet when nothing waits; loud when
 * something does.
 */
function WaitingLink({ count, active, collapsed = false }: { count: number; active: boolean; collapsed?: boolean }) {
  const label = count > 0 ? `${count} waiting for you` : 'Nothing waiting';
  if (collapsed) {
    return (
      <Link to="/waiting" title={label} aria-label={label} className={cn(rail.className, active && rail.activeProps.className, 'relative')}>
        <Inbox />
        {count > 0 && <span className="bg-warn ring-surface absolute top-1 right-1 size-2 rounded-full ring-2" />}
      </Link>
    );
  }
  return (
    <Link to="/waiting" data-tour="waiting" className={cn(nav.className, active && nav.activeProps.className)}>
      <Inbox className={cn('size-4', count > 0 ? 'text-warn-text' : 'text-muted')} />
      <span className={cn('flex-1', count > 0 && 'font-medium')}>Waiting</span>
      {count > 0 && <Badge tone="warn" className="figures">{count}</Badge>}
    </Link>
  );
}

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
  { to: '/projects/$projectId/permissions', label: 'Permissions', Icon: ShieldCheck },
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

/** Whether a media query matches, following the window as it is resized. */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const media = window.matchMedia(query);
    const onChange = () => setMatches(media.matches);
    onChange();
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, [query]);
  return matches;
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
 * Who is using the client, and the way to everything about them.
 *
 * At the foot of the sidebar because that is where a person looks for
 * themselves. It says the one thing that cannot wait for anyone to open
 * anything — whether the backends are there to answer — and opens onto the
 * settings, each a tab of their own page.
 */
function UserMenu({
  backends,
  collapsed = false,
}: {
  backends: BackendInstance[];
  collapsed?: boolean;
}) {
  const me = useMe();
  const navigate = useNavigate();

  // Authenticated or not is not a detail to smooth over: in ModeNone nobody was
  // asked to prove anything, and the id is whatever Core was configured with.
  const anonymous = !me.data || me.data.authMode === 'none';
  const name = anonymous ? 'anonymous' : me.data.name || me.data.email || me.data.userId;
  const live = backends.filter((backend) => backend.ownershipStatus !== 'REVOKED');
  const unwell = live.filter((backend) => backend.operationalStatus !== 'READY');
  const status =
    live.length === 0
      ? 'No backend yet'
      : unwell.length > 0
        ? `${unwell.length} of ${live.length} ${live.length === 1 ? 'backend' : 'backends'} not ready`
        : // Counted, never named: a name alone reads as "the" backend, as if
          // there could only be one.
          `${live.length} ${live.length === 1 ? 'backend' : 'backends'} ready`;
  const go = (tab: 'appearance' | 'notifications' | 'backends') => void navigate({ to: '/settings/$tab', params: { tab } });

  return (
    <Menu>
      <MenuTrigger asChild>
        {collapsed ? (
          // In the rail the avatar is the whole control, pushed to the foot
          // where it sits when the sidebar is open, so folding the sidebar
          // moves nothing a hand has already learned.
          <button
            type="button"
            title={`${name} · ${status}`}
            className="hover:bg-surface-2 data-[state=open]:bg-surface-2 relative mt-auto self-center rounded-full p-1"
          >
            <Avatar name={name} className="size-7 text-xs" />
            {(unwell.length > 0 || live.length === 0) && (
              <span className="bg-warn ring-surface absolute top-0.5 right-0.5 size-2 rounded-full ring-2" />
            )}
          </button>
        ) : (
          <button
            type="button"
            data-tour="user"
            className="hover:bg-surface-2 data-[state=open]:bg-surface-2 flex w-full items-center gap-2.5 rounded-lg p-2 text-left"
          >
            <Avatar name={name} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">{name}</span>
              <span
                className={cn(
                  'flex items-center gap-1.5 truncate text-xs',
                  unwell.length > 0 || live.length === 0 ? 'text-warn-text' : 'text-muted',
                )}
              >
                <span
                  className={cn(
                    'size-1.5 shrink-0 rounded-full',
                    unwell.length > 0 || live.length === 0 ? 'bg-warn' : 'bg-ok',
                  )}
                />
                {status}
              </span>
            </span>
            <ChevronsUpDown className="text-muted size-4 shrink-0" />
          </button>
        )}
      </MenuTrigger>
      <MenuContent side="top" align="start" className="min-w-60">
        <MenuLabel className="pb-1">
          <span className="text-text block text-sm font-medium">{name}</span>
          {anonymous ? 'No authentication configured' : `Signed in with ${me.data?.authMode}`}
        </MenuLabel>
        <MenuSeparator />
        <MenuItem onSelect={() => go('appearance')}>
          <Monitor className="text-muted size-4" />
          Appearance
        </MenuItem>
        <MenuItem onSelect={() => go('notifications')}>
          <Bell className="text-muted size-4" />
          Notifications
        </MenuItem>
        <MenuItem onSelect={() => go('backends')}>
          <Server className="text-muted size-4" />
          <span className="flex-1">Backends</span>
          {unwell.length > 0 && <span className="bg-warn size-1.5 rounded-full" />}
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

const LAST_SESSIONS = 'threavia.lastSessions';

function lastSessions(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(LAST_SESSIONS) ?? '{}') as Record<string, string>;
  } catch {
    return {};
  }
}

function lastSession(projectId: string): string | undefined {
  return lastSessions()[projectId];
}

function rememberSession(projectId: string, sessionId: string) {
  try {
    localStorage.setItem(LAST_SESSIONS, JSON.stringify({ ...lastSessions(), [projectId]: sessionId }));
  } catch {
    // Without storage a switch lands on a new Session, which is still a place.
  }
}

/**
 * Which Project the sidebar shows, and the way to another or a new one.
 *
 * One control, the width of the sidebar, rather than a list box with a button
 * squeezed beside it: the Project is the frame of everything below, and
 * creating one is something done from the same place a Project is chosen.
 */
function ProjectSwitcher({
  projects,
  current,
  onSwitch,
}: {
  projects: Project[];
  current?: string;
  onSwitch: (projectId: string) => void;
}) {
  const [creating, setCreating] = useState(false);
  const project = projects.find((candidate) => candidate.id === current);

  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <button
            type="button"
            title="Switch project"
            className="hover:bg-surface-2 data-[state=open]:bg-surface-2 border-border flex w-full items-center gap-2 rounded-lg border px-2 py-1.5 text-left text-sm"
          >
            <ProjectMark name={project?.name} />
            <span className={cn('min-w-0 flex-1 truncate', project ? 'font-medium' : 'text-muted')}>
              {project?.name ?? 'No project'}
            </span>
            <ChevronsUpDown className="text-muted size-4 shrink-0" />
          </button>
        </MenuTrigger>
        <MenuContent align="start">
          <MenuLabel>Projects</MenuLabel>
          {projects.map((candidate) => (
            <MenuItem key={candidate.id} onSelect={() => onSwitch(candidate.id)}>
              <ProjectMark name={candidate.name} />
              <span className="min-w-0 flex-1 truncate">{candidate.name}</span>
              {candidate.id === current && <Check className="text-muted size-3.5 shrink-0" />}
            </MenuItem>
          ))}
          {projects.length > 0 && <MenuSeparator />}
          <MenuItem onSelect={() => setCreating(true)}>
            <span className="text-muted flex size-6 shrink-0 items-center justify-center">
              <Plus className="size-4" />
            </span>
            New project…
          </MenuItem>
        </MenuContent>
      </Menu>
      <NewProjectDialog open={creating} onOpenChange={setCreating} onCreated={onSwitch} />
    </>
  );
}

/** A Project's initial, so it is found by shape in a list before it is read. */
function ProjectMark({ name }: { name?: string }) {
  return (
    <span className="bg-accent/10 text-accent flex size-6 shrink-0 items-center justify-center rounded-md text-xs font-semibold uppercase">
      {(name ?? '').trim().slice(0, 1) || '·'}
    </span>
  );
}

function NewProjectDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (projectId: string) => void;
}) {
  const create = useCreateProject();
  const [name, setName] = useState('');
  const setOpen = onOpenChange;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent>
        <DialogTitle>New project</DialogTitle>
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (!name.trim()) return;
            // The handler returns void, so the promise is discarded here rather
            // than handed to the DOM, which would never await it.
            // A failure is shown below the form; the promise only resolves
            // the success path.
            void create
              .mutateAsync(name.trim())
              .then((project) => {
                onCreated(project.id);
                setName('');
                setOpen(false);
              })
              .catch(() => {});
          }}
        >
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="homelab"
            autoFocus
          />
          <ActionError error={create.error} />
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

/**
 * Whether the stream is up — or, in the demo, that this is the demo, and the
 * way back into its tour.
 */
function StreamBadge({ connected }: { connected: boolean }) {
  if (DEMO) {
    return (
      <button
        type="button"
        title="Fictitious data, nothing leaves this browser. Restart the tour."
        onClick={() => window.dispatchEvent(new Event('threavia:tour'))}
        className="bg-accent/12 text-accent hover:bg-accent/20 ml-auto rounded px-1.5 py-0.5 text-xs font-medium whitespace-nowrap"
      >
        Demo
      </button>
    );
  }
  return (
    <Badge tone={connected ? 'ok' : 'neutral'} title="Realtime stream" className="ml-auto">
      {connected ? 'live' : 'offline'}
    </Badge>
  );
}
