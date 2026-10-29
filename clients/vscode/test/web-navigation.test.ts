// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';
import { createMemoryHistory, RouterProvider, useParams } from '../../../web/ui/node_modules/@tanstack/react-router';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError } from '../../../web/ui/src/api/client';
import { forgetSession, lastNavigation, rememberProject } from '../../../web/ui/src/lib/navigation';
import { useSelectedProject } from '../../../web/ui/src/use-project';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
const mocks = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/demo/mode', () => ({ DEMO: false }));
vi.mock('@/api/client', async (original) => ({
  ...await original<typeof import('../../../web/ui/src/api/client')>(),
  api: { get: mocks.get },
}));
// Keep the real shell, router, landing page and query hooks; the destination's
// timeline/composer is unrelated to selecting and persisting the workspace.
vi.mock('@/routes/session', () => ({ SessionView: () => h('h1', {}, `Session ${useParams({ strict: false }).sessionId} in ${useSelectedProject()}`) }));
vi.mock('@/routes/draft', () => ({ DraftView: () => h('h1', {}, `New session in ${useSelectedProject()}`) }));
vi.mock('@/routes/tasks', () => ({ TasksView: () => h('h1', {}, `Tasks in ${useSelectedProject()}`) }));

let router: typeof import('../../../web/ui/src/router')['router'];
let root: ReturnType<typeof createRoot> | undefined;
let client: QueryClient;
let history: ReturnType<typeof createMemoryHistory>;
let owner: string;
let available: string[];
let attention: Promise<unknown>;
let sessionFailure: Error | undefined;
const session = (id: string) => ({ id, projectId: id === 's-b' ? 'p-b' : 'p-a', title: id, status: 'ARCHIVED', updatedAt: '2026-10-29T07:00:00Z' });
beforeEach(async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  window.matchMedia = vi.fn().mockImplementation(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  localStorage.clear();
  owner = 'owner'; available = ['p-a', 'p-b']; sessionFailure = undefined;
  attention = Promise.resolve({ validations: [], userInputs: [] });
  mocks.get.mockReset().mockImplementation((path: string) => {
    if (path === '/api/v1/me') return Promise.resolve({ userId: owner, authMode: 'none' });
    if (path === '/api/v1/projects') return Promise.resolve({ items: available.map(id => ({ id, name: id })) });
    if (path === '/api/v1/me/attention') return attention;
    if (/^\/api\/v1\/sessions\/[^/]+$/.test(path)) return sessionFailure ? Promise.reject(sessionFailure) : Promise.resolve({ session: session(path.split('/').pop()!), cursor: 0, events: [], jobs: [] });
    return Promise.resolve({ items: [] });
  });
  router = (await import('../../../web/ui/src/router')).router;
});
async function close() {
  if (root) await act(() => root?.unmount());
  root = undefined; client?.clear(); history?.destroy(); document.body.replaceChildren();
}
afterEach(async () => { await close(); vi.unstubAllGlobals(); });
async function show(path = '/') {
  history = createMemoryHistory({ initialEntries: [path] }); router.update({ history });
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  const mount = document.createElement('div'); document.body.append(mount); root = createRoot(mount);
  await act(async () => { root?.render(h(QueryClientProvider, { client }, h(RouterProvider, { router }))); await router.load(); });
}
async function settled(check: () => void) {
  await vi.waitFor(async () => { await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); }); check(); });
}
const calls = () => mocks.get.mock.calls.map(([path]) => path as string);

it('persists a directly opened archived session and restores it with a fresh cache without waiting for attention', async () => {
  await show('/sessions/s-b');
  await settled(() => expect(lastNavigation('owner')).toMatchObject({ projectId: 'p-b', sessionId: 's-b' }));
  await close(); mocks.get.mockClear(); attention = new Promise(() => {});
  await show('/');
  await settled(() => expect(history.location.pathname).toBe('/sessions/s-b'));
  expect(document.querySelector('h1')?.textContent).toBe('Session s-b in p-b');
  expect(calls().filter(path => path === '/api/v1/sessions/s-b')).toHaveLength(1);
  expect(calls().some(path => /pinned-sessions|\/tasks|\/monitors|\/projects\/.*\/sessions/.test(path))).toBe(false);
});

it('loads the mobile navigation only when its drawer opens', async () => {
  rememberProject('owner', 'p-b'); await show('/waiting');
  await settled(() => expect(document.querySelector('h1')?.textContent).toBe('Waiting for you'));
  expect(calls().some(path => /pinned-sessions|\/tasks|\/monitors|\/projects\/.*\/sessions/.test(path))).toBe(false);
  await act(() => { document.querySelector<HTMLButtonElement>('button[title="Open the menu"]')!.click(); });
  await settled(() => expect(calls()).toContain('/api/v1/projects/p-b/sessions?includeArchived=false'));
  expect(document.querySelector('button[title="Switch project"]')?.textContent).toContain('p-b');
});

it('remembers a project selected in the waiting view and resumes its own last session', async () => {
  rememberProject('owner', 'p-b', 's-b'); rememberProject('owner', 'p-a', 's-a');
  await show('/waiting');
  await settled(() => expect(document.querySelector('h1')?.textContent).toBe('Waiting for you'));
  await act(() => { document.querySelector<HTMLButtonElement>('button[title="Open the menu"]')!.click(); });
  await settled(() => expect(document.querySelector('button[title="Switch project"]')?.textContent).toContain('p-a'));
  await act(() => { document.querySelector<HTMLButtonElement>('button[title="Switch project"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
  await settled(() => expect(document.querySelector('[role="menu"]')).not.toBeNull());
  await act(() => { [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(item => item.textContent?.endsWith('p-b'))!.click(); });
  expect(history.location.pathname).toBe('/waiting');
  expect(lastNavigation('owner')).toMatchObject({ projectId: 'p-b', sessionId: 's-b' });
  await close(); await show();
  await settled(() => expect(history.location.pathname).toBe('/sessions/s-b'));
});

it('keeps the desktop navigation populated on startup', async () => {
  window.matchMedia = vi.fn().mockImplementation(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  rememberProject('owner', 'p-b'); await show('/waiting');
  await settled(() => expect(calls()).toContain('/api/v1/projects/p-b/sessions?includeArchived=false'));
  expect(calls()).toContain('/api/v1/me/pinned-sessions');
  expect(document.querySelector('button[title="Switch project"]')?.textContent).toContain('p-b');
});

it('keeps direct links ahead of the stored session and saves the new project', async () => {
  rememberProject('owner', 'p-b', 's-b'); await show('/sessions/s-a');
  await settled(() => expect(lastNavigation('owner')).toMatchObject({ projectId: 'p-a', sessionId: 's-a' }));
  expect(history.location.pathname).toBe('/sessions/s-a');
  expect(calls()).not.toContain('/api/v1/sessions/s-b');
});

it('returns a deleted session to a new session in the same project and clears the stale identifier', async () => {
  rememberProject('owner', 'p-b', 's-b'); sessionFailure = new ApiError(404, 'not_found', 'Session deleted');
  await show();
  await settled(() => expect(history.location.pathname).toBe('/sessions/new'));
  expect(history.location.search).toContain('projectId=p-b');
  expect(lastNavigation('owner').sessionId).toBeUndefined();
});

it('keeps the saved place after a temporary failure and lets the user retry', async () => {
  rememberProject('owner', 'p-b', 's-b'); sessionFailure = new Error('Network unavailable'); await show();
  await settled(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain('Your last session did not open'));
  expect(lastNavigation('owner').sessionId).toBe('s-b'); sessionFailure = undefined;
  await act(() => { [...document.querySelectorAll('button')].find(button => button.textContent === 'Try again')!.click(); });
  await settled(() => expect(history.location.pathname).toBe('/sessions/s-b'));
});

it('ignores a deleted project and another account’s saved place', async () => {
  rememberProject('owner', 'p-b', 's-b'); available = ['p-a']; await show();
  await settled(() => expect(document.querySelector('h1')?.textContent).toBe('Tasks in p-a'));
  expect(calls()).not.toContain('/api/v1/sessions/s-b'); await close();
  available = ['p-a', 'p-b']; owner = 'someone-else'; mocks.get.mockClear(); await show();
  await settled(() => expect(document.querySelector('h1')?.textContent).toBe('Tasks in p-a'));
  expect(calls()).not.toContain('/api/v1/sessions/s-b');
});

it('restores a selected project even when no session has been opened there', async () => {
  await show('/sessions/new?projectId=p-b');
  await settled(() => expect(lastNavigation('owner').projectId).toBe('p-b')); await close(); await show();
  await settled(() => expect(history.location.pathname).toBe('/sessions/new'));
  expect(history.location.search).toContain('projectId=p-b');
});

it('treats a failed attention request as an error with retry rather than an empty inbox', async () => {
  attention = Promise.reject(new Error('Offline')); void attention.catch(() => {}); await show('/waiting');
  await settled(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain('Pending requests did not load'));
  expect(document.body.textContent).not.toContain('Nothing needs a decision.');
  expect(document.body.textContent).not.toContain('The agents are not waiting');
});

it('ignores malformed storage and keeps per-project sessions when selection changes', () => {
  for (const value of ['{', 'null', '[]', '{"projectId":5,"sessions":null}', '{"projectId":"p-a","sessions":{"p-a":true}}']) {
    localStorage.setItem('threavia.navigation.owner', value);
    expect(lastNavigation('owner').sessionId).toBeUndefined();
  }
  rememberProject('owner', 'p-a', 's-a'); rememberProject('owner', 'p-b', 's-b'); rememberProject('owner', 'p-a');
  expect(lastNavigation('owner')).toMatchObject({ projectId: 'p-a', sessionId: 's-a' });
  forgetSession('owner', 's-a'); expect(lastNavigation('owner').sessions).toEqual({ 'p-b': 's-b' });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => { throw new Error('Storage unavailable'); });
  expect(() => rememberProject('owner', 'p-a')).not.toThrow(); vi.restoreAllMocks();
});
