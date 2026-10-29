// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';
import { createBrowserHistory, RouterProvider } from '../../../web/ui/node_modules/@tanstack/react-router';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act, StrictMode } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
  StrictMode: unknown;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), backends: [{ id: 'other', name: 'Other backend', operationalStatus: 'READY' }, { id: 'backend', name: 'Development', operationalStatus: 'READY' }] }));
vi.mock('@/api/client', () => ({ api: { get: mocks.get, post: mocks.post, query: mocks.post } }));
vi.mock('@/api/queries', () => ({
  useBackends: () => ({ data: mocks.backends }),
  useProjects: () => ({ data: [] }),
  useMe: () => ({ data: { userId: 'dev', authMode: 'none' } }),
}));
vi.mock('@/components/app-shell', () => ({ AppShell: ({ children }: { children: unknown }) => children }));

const names = ['mock_read', 'mock_list_notes', 'mock_create_note', 'mock_delete_note', 'mock_slow_read'];
const discovered = { state: 'CONNECTED', serverId: 'mock', resources: false, prompts: false, tools: names.map(name => ({ name })) };
const pending = { backend: 'backend', serverId: 'mock', state: 'mock-state' };
let root: ReturnType<typeof createRoot> | undefined;
let history: ReturnType<typeof createBrowserHistory> | undefined;
const clients: QueryClient[] = [];

beforeEach(() => {
  window.history.replaceState(null, '', '/settings/mcp');
  mocks.backends = [{ id: 'other', name: 'Other backend', operationalStatus: 'READY' }, { id: 'backend', name: 'Development', operationalStatus: 'READY' }];
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  mocks.get.mockReset().mockResolvedValue({ items: [{ id: 'mock', name: 'oauth_mock', transport: 'http', oauth: true, enabled: true, tools: names, readTools: names.slice(0, 2) }] });
  mocks.post.mockReset().mockImplementation((_path: string, input: { operation: string }) => {
    if (input.operation === 'auth_finish') return Promise.resolve({ state: 'AUTHENTICATED' });
    if (input.operation === 'probe') return Promise.resolve(discovered);
    throw new Error('Unexpected operation');
  });
});
afterEach(async () => {
  const mounted = root;
  if (mounted) await act(() => { mounted.unmount(); });
  root = undefined;
  history?.destroy();
  clients.splice(0).forEach(client => client.clear());
  document.body.replaceChildren(); sessionStorage.clear(); vi.unstubAllGlobals();
});

async function settle(work?: () => void) {
  await act(async () => { work?.(); await new Promise(resolve => setTimeout(resolve, 100)); });
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
function callback(query = 'code=mock-code&state=mock-state&iss=http%3A%2F%2F127.0.0.1%3A18084') {
  window.history.replaceState(null, '', `/settings/mcp?${query}`);
  sessionStorage.setItem('threavia-mcp-auth', JSON.stringify(pending));
  return window.location.href;
}
function operations() {
  return mocks.post.mock.calls.map(call => (call[1] as { operation: string }).operation);
}
function button(text: string) {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === text);
  expect(found).toBeDefined(); return found!;
}
async function settings() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); clients.push(client);
  history = createBrowserHistory({ window });
  const { router } = await import('../../../web/ui/src/router'); router.update({ history });
  const mount = document.createElement('div'); document.body.append(mount); root = createRoot(mount);
  const render = () => { root!.render(h(StrictMode, null, h(QueryClientProvider, { client }, h(RouterProvider, { router })))); };
  // A page module is fetched on navigation. Wait for the real router rather
  // than assuming the module and its OAuth effects mount within 200 ms.
  await act(async () => { render(); await router.load(); }); await settle();
  return { client, remount: async () => {
    await act(() => { root!.unmount(); }); root = createRoot(mount); await settle(render); await settle();
  } };
}

it('finishes automatically once and keeps server/backend progress through remounts', async () => {
  const response = callback();
  const finish = deferred<{ state: string }>(); const probe = deferred<typeof discovered>();
  mocks.post.mockImplementation((_path: string, input: { operation: string }) => input.operation === 'auth_finish' ? finish.promise : probe.promise);
  const page = await settings();
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Connecting oauth_mock');
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Development');
  expect(document.body.textContent).toContain('Finishing sign-in automatically');
  expect(document.body.textContent).not.toContain('Complete MCP sign-in');
  expect(window.location.search).toBe('');
  expect(sessionStorage.getItem('threavia-mcp-auth')).toContain('mock-code');
  await page.remount(); expect(operations()).toEqual(['auth_finish']);
  expect(mocks.post).toHaveBeenCalledWith('/api/v1/backends/backend/mcp', { operation: 'auth_finish', serverId: 'mock', state: 'mock-state', response });
  await settle(() => { finish.resolve({ state: 'AUTHENTICATED' }); }); await settle();
  expect(document.body.textContent).toContain('Checking available tools');
  expect(sessionStorage.getItem('threavia-mcp-auth')).toContain('"connected":true');
  expect(sessionStorage.getItem('threavia-mcp-auth')).not.toContain('mock-code');
  await page.remount(); expect(operations()).toEqual(['auth_finish', 'probe']);
  await settle(() => { probe.resolve(discovered); }); await settle();
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('oauth_mock account connected');
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('5 tools available');
  expect(sessionStorage.getItem('threavia-mcp-auth')).toBeNull();
  await settle(() => { void page.client.invalidateQueries({ queryKey: ['mcp'] }); });
  expect(operations()).toEqual(['auth_finish', 'probe']);
  await settle(() => { button('Review tools').click(); });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(document.querySelector('[aria-label="Discovered MCP tools"]')?.textContent).toContain('mock_slow_read');
  expect(document.activeElement).toBe(document.querySelector('[aria-label="Discovered MCP tools"]'));
});

it('only checks tools after a reload following a successful exchange', async () => {
  window.history.replaceState(null, '', '/settings/mcp');
  sessionStorage.setItem('threavia-mcp-auth', JSON.stringify({ ...pending, connected: true }));
  await settings(); await settle();
  expect(operations()).toEqual(['probe']);
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('5 tools available');
  expect(sessionStorage.getItem('threavia-mcp-auth')).toBeNull();
});

it('offers a fresh connection after exchange failure without automatically replaying the code', async () => {
  callback(); mocks.post.mockRejectedValue(new Error('Authorization response expired'));
  const page = await settings(); await settle();
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Sign-in could not be confirmed');
  expect(document.body.textContent).toContain('Authorization response expired');
  expect(button('Connect again…')).toBeDefined();
  expect(sessionStorage.getItem('threavia-mcp-auth')).toBeNull();
  mocks.post.mockRejectedValue(new Error('Backend is offline'));
  await settle(() => { button('Connect again…').click(); });
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Backend is offline');
  await page.remount(); expect(operations()).toEqual(['auth_finish', 'auth_start', 'probe']);
});

it('keeps account success separate from failed discovery and retries only the read', async () => {
  callback(); let probes = 0;
  mocks.post.mockImplementation((_path: string, input: { operation: string }) => {
    if (input.operation === 'auth_finish') return Promise.resolve({ state: 'AUTHENTICATED' });
    return ++probes === 1 ? Promise.reject(new Error('Server unavailable')) : Promise.resolve(discovered);
  });
  await settings(); await settle();
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('oauth_mock account connected');
  expect(document.body.textContent).toContain('its tools could not be loaded');
  await settle(() => { button('Check connection again').click(); }); await settle();
  expect(operations()).toEqual(['auth_finish', 'probe', 'probe']);
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('5 tools available');
});

it.each([
  ['error=access_denied&state=mock-state', 'Connection cancelled'],
  ['error=provider_failed&state=mock-state', 'provider did not complete sign-in'],
  ['code=unused&state=other', 'no longer matches'],
  ['code=unused', 'no longer matches'],
])('explains refused or invalid returns without exchanging them: %s', async (query, message) => {
  callback(query); await settings();
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain(message);
  expect(mocks.post).not.toHaveBeenCalled(); expect(window.location.search).toBe('');
});

it.each([
  ['a different state', JSON.stringify({ ...pending, response: 'http://localhost:3000/settings/mcp?state=other&code=unused' })],
  ['a different origin', JSON.stringify({ ...pending, response: 'https://other.example/settings/mcp?state=mock-state&code=unused' })],
  ['malformed storage', '{invalid'],
])('does not exchange stored data with %s', async (_label, stored) => {
  window.history.replaceState(null, '', '/settings/mcp'); sessionStorage.setItem('threavia-mcp-auth', stored);
  await settings(); expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(operations()).toEqual(['probe']); expect(button('View tools')).toBeDefined();
});

it('rechecks the real connection after a full reload and remembers the sign-in backend', async () => {
  callback(); await settings(); await settle();
  expect(sessionStorage.getItem('threavia-mcp-auth')).toBeNull();
  await act(() => { root!.unmount(); }); root = undefined;
  history!.destroy(); history = undefined;
  clients.splice(0).forEach(client => client.clear());
  mocks.post.mockClear();
  await settings(); await settle();
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(operations()).toEqual(['probe']);
  expect(mocks.post).toHaveBeenCalledWith('/api/v1/backends/backend/mcp', { operation: 'probe', serverId: 'mock' });
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connected');
  expect(document.body.textContent).toContain('Connection on Development · 5 tools available');
  expect([...document.querySelectorAll('button')].some(b => b.textContent === 'Connect account…')).toBe(false);
  await settle(() => { button('View tools').click(); });
  expect(document.querySelector('[aria-label="Discovered MCP tools"]')?.textContent).toContain('mock_slow_read');
  expect(operations()).toEqual(['probe']);
});

it('shows checking while the backend is still verifying a reloaded connection', async () => {
  const probe = deferred<typeof discovered>(); mocks.post.mockReturnValue(probe.promise);
  await settings();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Checking connection…');
  expect(button('Checking…').disabled).toBe(true);
  await settle(() => { probe.resolve(discovered); }); await settle();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connected');
  expect(operations()).toEqual(['probe']);
});

it('keeps verification failures distinct from disconnection and retries only the read', async () => {
  mocks.post.mockRejectedValueOnce(new Error('Server is unreachable'));
  await settings();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connection unverified');
  expect(document.body.textContent).toContain('Server is unreachable');
  expect(document.body.textContent).not.toContain('Disconnected');
  expect(button('Connect account…')).toBeDefined();
  await settle(() => { button('Check again').click(); }); await settle();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connected');
  expect(operations()).toEqual(['probe', 'probe']);
});

it('does not reuse one backend’s success for another and preserves the selected backend', async () => {
  sessionStorage.setItem('threavia-mcp-backend', 'backend');
  mocks.post.mockImplementation((path: string) => path.includes('/other/') ? Promise.reject(new Error('Sign-in is needed on this backend')) : Promise.resolve(discovered));
  await settings();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connected');
  const select = document.querySelector<HTMLSelectElement>('select')!;
  await settle(() => { select.value = 'other'; select.dispatchEvent(new Event('change', { bubbles: true })); }); await settle();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connection unverified');
  expect(document.body.textContent).toContain('Connection on Other backend');
  expect(sessionStorage.getItem('threavia-mcp-backend')).toBe('other');
  expect([...document.querySelectorAll('button')].some(b => b.textContent === 'View tools')).toBe(false);
  await settle(() => { select.value = 'backend'; select.dispatchEvent(new Event('change', { bubbles: true })); }); await settle();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connected');
  expect(document.body.textContent).toContain('Connection on Development');
});

it('hides a cached success when that backend goes offline without sending a probe', async () => {
  const page = await settings();
  expect(button('View tools')).toBeDefined();
  mocks.post.mockClear();
  mocks.backends = mocks.backends.map(backend => ({ ...backend, operationalStatus: 'OFFLINE' }));
  await page.remount();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Backend unavailable');
  expect(button('Connect account…').disabled).toBe(true);
  expect(mocks.post).not.toHaveBeenCalled();
});

it.each([
  { ...discovered, state: 'AUTHENTICATED' },
  { ...discovered, serverId: 'another-server' },
])('does not show a connection that the backend did not confirm: %s', async result => {
  mocks.post.mockResolvedValue(result); await settings();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connection unverified');
  expect(document.body.textContent).toContain('The backend did not confirm this connection');
});

it('never starts a stdio process merely by opening the settings page', async () => {
  mocks.get.mockResolvedValue({ items: [{ id: 'local', name: 'local', transport: 'stdio', command: 'npx', oauth: false, enabled: true, tools: [] }] });
  await settings();
  expect(mocks.post).not.toHaveBeenCalled();
  expect(button('Test connection')).toBeDefined();
});

it('checks an edited declaration again instead of retaining its previous success', async () => {
  const page = await settings();
  expect(button('View tools')).toBeDefined();
  mocks.get.mockResolvedValue({ items: [{ id: 'mock', revision: 2, name: 'oauth_mock', transport: 'http', oauth: true, enabled: true, tools: names }] });
  mocks.post.mockRejectedValue(new Error('The new endpoint is unavailable'));
  await settle(() => { void page.client.invalidateQueries({ queryKey: ['mcp'] }); }); await settle();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connection unverified');
  expect(document.body.textContent).toContain('The new endpoint is unavailable');
  expect(operations()).toEqual(['probe', 'probe']);
});

it('clears a verified connection after an explicit disconnect and checks it again', async () => {
  let disconnected = false;
  mocks.post.mockImplementation((_path: string, input: { operation: string }) => {
    if (input.operation === 'logout') { disconnected = true; return Promise.resolve({ state: 'DISCONNECTED' }); }
    return disconnected ? Promise.reject(new Error('Authentication required on this backend')) : Promise.resolve(discovered);
  });
  await settings();
  const menu = document.querySelector<HTMLButtonElement>('[aria-label="Actions for oauth_mock"]')!;
  await settle(() => { menu.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); });
  const disconnect = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(item => item.textContent === 'Disconnect account on this backend…');
  expect(disconnect).toBeDefined();
  await settle(() => { disconnect!.click(); });
  await settle(() => { button('Disconnect account').click(); }); await settle();
  expect(document.querySelector('[data-tour="mcp"] [role="status"]')?.textContent).toBe('Connection unverified');
  expect(document.body.textContent).toContain('Authentication required on this backend');
  expect(operations()).toEqual(['probe', 'logout', 'probe']);
  expect(button('Connect account…')).toBeDefined();
});
