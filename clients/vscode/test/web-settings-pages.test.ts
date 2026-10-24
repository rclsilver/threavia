// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';
import { createMemoryHistory, RouterProvider } from '../../../web/ui/node_modules/@tanstack/react-router';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
vi.mock('@/components/app-shell', () => ({ AppShell: ({ children }: { children: unknown }) => children }));
vi.mock('@/demo/mode', () => ({ DEMO: false }));
vi.mock('@/api/client', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../../web/ui/src/api/client')>(),
  api: { get: (path: string) => Promise.resolve(path === '/api/v1/me'
    ? { userId: 'test-owner', authMode: 'none' }
    : { items: [] }) },
}));

import { ACCOUNT_TABS } from '../../../web/ui/src/lib/account-tabs';

let router: typeof import('../../../web/ui/src/router')['router'];
let root: ReturnType<typeof createRoot>;
let client: QueryClient;
let history: ReturnType<typeof createMemoryHistory>;
beforeEach(async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  window.matchMedia = vi.fn().mockImplementation(() => ({
    matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  }));
  router = (await import('../../../web/ui/src/router')).router;
  history = createMemoryHistory({ initialEntries: ['/settings'] });
  router.update({ history });
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const mount = document.createElement('div');
  document.body.append(mount);
  root = createRoot(mount);
});
afterEach(async () => {
  await act(() => { root.unmount(); });
  client.clear();
  history.destroy();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});

async function show() {
  await act(async () => {
    root.render(h(QueryClientProvider, { client }, h(RouterProvider, { router })));
    await router.load();
  });
  await vi.waitFor(() => expect(document.querySelector('h1')).not.toBeNull());
}
const page = () => document.querySelector('h1')?.textContent;
const link = (href: string) => document.querySelector<HTMLAnchorElement>(`a[href="${href}"]`)!;

it('opens the settings index, follows a page and returns through real navigation history', async () => {
  await show();
  expect(page()).toBe('Your settings');
  for (const { tab, label } of ACCOUNT_TABS) {
    expect(link(`/settings/${tab}`)?.textContent).toContain(label);
  }
  await act(async () => { link('/settings/appearance').click(); await router.load(); });
  expect(page()).toBe('Appearance');
  expect(link('/settings/appearance').getAttribute('aria-current')).toBe('page');
  await act(async () => { link('/settings').click(); await router.load(); });
  expect(page()).toBe('Your settings');
  await act(async () => { history.back(); await router.load(); });
  expect(page()).toBe('Appearance');
});

it('keeps direct section URLs and returns an unknown section to the settings index', async () => {
  await show();
  await act(async () => { await router.navigate({ to: '/settings/$tab', params: { tab: 'backends' } }); });
  expect(page()).toBe('Backends');
  expect(history.location.pathname).toBe('/settings/backends');
  await act(async () => { await router.navigate({ to: '/settings/$tab', params: { tab: 'unknown' } }); });
  expect(page()).toBe('Your settings');
  expect(history.location.pathname).toBe('/settings');
});
