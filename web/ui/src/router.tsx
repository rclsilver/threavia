import {
  Navigate,
  createRootRoute,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
} from '@tanstack/react-router';

import { CALLBACK_PATH } from '@/auth/oidc';
import { landingPath } from '@/auth/landing';
import { lazy, Suspense } from 'react';

import { AppShell } from '@/components/app-shell';
import { DEMO } from '@/demo/mode';
import { ACCOUNT_TABS } from '@/lib/account-tabs';
import type { SettingsTab } from '@/routes/session-settings';

// Fetch only the page being opened. Account settings, review and project
// administration should not delay a conversation on a phone.
const DraftView = lazyRouteComponent(() => import('@/routes/draft'), 'DraftView');
const SessionView = lazyRouteComponent(() => import('@/routes/session'), 'SessionView');
const ReviewView = lazyRouteComponent(() => import('@/routes/review'), 'ReviewView');
const SettingsView = lazyRouteComponent(() => import('@/routes/settings'), 'SettingsView');
const SessionSettingsView = lazyRouteComponent(() => import('@/routes/session-settings'), 'SessionSettingsView');
const TasksView = lazyRouteComponent(() => import('@/routes/tasks'), 'TasksView');
const HomeView = lazyRouteComponent(() => import('@/routes/waiting'), 'HomeView');
const WaitingView = lazyRouteComponent(() => import('@/routes/waiting'), 'WaitingView');
const ArtifactsView = lazyRouteComponent(() => import('@/routes/project'), 'ArtifactsView');
const AuditView = lazyRouteComponent(() => import('@/routes/project'), 'AuditView');
const InstructionsView = lazyRouteComponent(() => import('@/routes/project'), 'InstructionsView');
const MemoryView = lazyRouteComponent(() => import('@/routes/project'), 'MemoryView');
const DirectoriesView = lazyRouteComponent(() => import('@/routes/project'), 'DirectoriesView');
const PermissionsView = lazyRouteComponent(() => import('@/routes/project'), 'PermissionsView');
const SkillsView = lazyRouteComponent(() => import('@/routes/project'), 'SkillsView');

// The tour exists only in the demo, and is fetched only there.
const DemoTour = lazy(() => import('@/demo/tour'));

const rootRoute = createRootRoute({
  component: () => (
    <AppShell>
      <Outlet />
      {DEMO && (
        <Suspense fallback={null}>
          <DemoTour />
        </Suspense>
      )}
    </AppShell>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  // Resume the last workspace; first visits use the attention/tasks dashboard.
  path: '/',
  component: HomeView,
});

const waitingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/waiting',
  component: WaitingView,
});

const draftRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions/new',
  validateSearch: (search: Record<string, unknown>): { projectId: string } => ({
    projectId: typeof search.projectId === 'string' ? search.projectId : '',
  }),
  component: DraftView,
});

const sessionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions/$sessionId',
  component: SessionView,
});

const reviewRoute = createRoute({ getParentRoute: () => rootRoute, path: '/sessions/$sessionId/review', component: ReviewView });

/** Each settings page keeps its address; unknown pages return to the index. */
const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings/$tab',
  beforeLoad: ({ params }) => {
    if (!ACCOUNT_TABS.some(({ tab }) => tab === params.tab)) {
      // eslint-disable-next-line @typescript-eslint/only-throw-error
      throw redirect({ to: '/settings' });
    }
  },
  component: SettingsView,
});

const settingsIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: SettingsView,
});

const SETTINGS_TABS: SettingsTab[] = ['mcp', 'permissions', 'schedules', 'backend', 'session', 'model'];

/** A Session's settings, one tab per URL; an unknown tab lands on the first. */
const sessionSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions/$sessionId/settings/$tab',
  beforeLoad: ({ params }) => {
    if (!SETTINGS_TABS.includes(params.tab as SettingsTab)) {
      // The router signals a redirect by throwing it.
      // eslint-disable-next-line @typescript-eslint/only-throw-error
      throw redirect({ to: '/sessions/$sessionId/settings/$tab', params: { ...params, tab: 'permissions' } });
    }
  },
  component: SessionSettingsView,
});

const sessionSettingsIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions/$sessionId/settings',
  beforeLoad: ({ params }) => {
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw redirect({ to: '/sessions/$sessionId/settings/$tab', params: { ...params, tab: 'permissions' } });
  },
});

/**
 * One route per part of a Project, so each is asked for by name.
 *
 * The bare Project path keeps working — links to it predate the split — and
 * lands on the tasks, which is what a Project is mostly consulted for.
 */
const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId',
  beforeLoad: ({ params }) => {
    // The router signals a redirect by throwing it. The thrown value is a
    // route, not an error, which the rule has no way of knowing.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw redirect({ to: '/projects/$projectId/tasks', params });
  },
});

const tasksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/tasks',
  component: TasksView,
});

const memoryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/memory',
  component: MemoryView,
});

const artifactsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/artifacts',
  component: ArtifactsView,
});

const skillsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/skills',
  component: SkillsView,
});

const instructionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/instructions',
  component: InstructionsView,
});

const auditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/audit',
  component: AuditView,
});

const permissionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/permissions',
  component: PermissionsView,
});

const directoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/directories',
  component: DirectoriesView,
});

/**
 * Where the provider sends the browser back.
 *
 * The exchange has already happened by the time this renders — the session
 * provider does it before anything else mounts — so all that is left is to put
 * the person back where they were.
 */
const callbackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: CALLBACK_PATH,
  component: () => <Navigate to={landingPath()} replace />,
});
const routeTree = rootRoute.addChildren([
  indexRoute,
  callbackRoute,
  waitingRoute,
  draftRoute,
  sessionRoute,
  reviewRoute,
  sessionSettingsRoute,
  sessionSettingsIndexRoute,
  settingsRoute,
  settingsIndexRoute,
  tasksRoute,
  memoryRoute,
  artifactsRoute,
  skillsRoute,
  instructionsRoute,
  auditRoute,
  permissionsRoute,
  directoriesRoute,
  projectRoute,
]);

// The demo is the whole client under /demo, so every link it follows stays in
// the demo.
export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  defaultPendingComponent: () => <p role="status" className="text-muted p-6 text-sm">Opening page…</p>,
  basepath: DEMO ? '/demo' : undefined,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
