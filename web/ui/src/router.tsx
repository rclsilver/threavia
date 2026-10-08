import {
  Navigate,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
} from '@tanstack/react-router';

import { CALLBACK_PATH } from '@/auth/oidc';
import { landingPath } from '@/auth/landing';
import { AppShell } from '@/components/app-shell';
import { DraftView } from '@/routes/draft';
import {
  ArtifactsView,
  AuditView,
  InstructionsView,
  MemoryView,
  PermissionsView,
  SkillsView,
} from '@/routes/project';
import { SessionView } from '@/routes/session';
import { TasksView } from '@/routes/tasks';
import { HomeView, WaitingView } from '@/routes/waiting';

const rootRoute = createRootRoute({
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  // Landing on what waits for a decision when something does, on the work
  // still owed otherwise.
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
  tasksRoute,
  memoryRoute,
  artifactsRoute,
  skillsRoute,
  instructionsRoute,
  auditRoute,
  permissionsRoute,
  projectRoute,
]);

export const router = createRouter({ routeTree, defaultPreload: 'intent' });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
