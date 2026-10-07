import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
} from '@tanstack/react-router';

import { AppShell } from '@/components/app-shell';
import { DraftView } from '@/routes/draft';
import { ProjectView } from '@/routes/project';
import { SessionView } from '@/routes/session';
import { TasksView } from '@/routes/tasks';

const rootRoute = createRootRoute({
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  // Landing on the work still owed, for whichever Project the sidebar points at.
  path: '/',
  component: TasksView,
});

const tasksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/tasks',
  component: TasksView,
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

const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId',
  component: ProjectView,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  draftRoute,
  sessionRoute,
  tasksRoute,
  projectRoute,
]);

export const router = createRouter({ routeTree, defaultPreload: 'intent' });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
