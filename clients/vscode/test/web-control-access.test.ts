// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as { createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void } };
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }));
vi.mock('@/api/client', () => ({ api: mocks }));
let root: ReturnType<typeof createRoot> | undefined;
let client: QueryClient | undefined;
afterEach(async () => { if (root) await act(() => root!.unmount()); root = undefined; client?.clear(); document.body.replaceChildren(); vi.unstubAllGlobals(); });
async function settle(work?: () => void) { await act(async () => { work?.(); await new Promise(resolve => setTimeout(resolve, 30)); }); }
function button(label: string) { const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === label || b.title === `${label} (N)`); expect(node).toBeDefined(); return node!; }

it('defaults to read access and keeps the one-time token out of shared caches', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn(); });
  mocks.get.mockResolvedValue({ items: [] });
  mocks.post.mockResolvedValue({ access: { id: 'fixture', scopes: ['read'] }, token: 'fictional-one-time-token' });
  const { ControlAccessPane } = await import('../../../web/ui/src/components/control-access-pane');
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const mount = document.createElement('div'); document.body.append(mount); root = createRoot(mount);
  await settle(() => root!.render(h(QueryClientProvider, { client }, h(ControlAccessPane))));
  await settle(() => button('Create MCP access').click());
  const boxes = [...document.querySelectorAll<HTMLElement>('[role="checkbox"]')];
  expect(boxes.map(b => b.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false', 'false']);
  const name = document.querySelector<HTMLInputElement>('input[maxlength="120"]')!;
  await settle(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'Development controller');
    name.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await settle(() => document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(mocks.post).toHaveBeenCalledWith('/api/v1/me/control-accesses', expect.objectContaining({ label: 'Development controller', scopes: ['read'] }));
  const secret = document.querySelector<HTMLInputElement>('input[type="password"]');
  expect(secret?.value).toBe('fictional-one-time-token');
  expect(JSON.stringify(client.getQueryCache().getAll().map(q => q.state.data))).not.toContain('fictional-one-time-token');
  expect(client.getMutationCache().getAll().every(m => m.state.data === undefined)).toBe(true);
  await settle(() => button('I saved the token').click());
  expect(document.querySelector('input[type="password"]')).toBeNull();
});
