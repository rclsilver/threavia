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
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/api/client', () => ({ api: mocks }));
let root: ReturnType<typeof createRoot> | undefined;
let client: QueryClient | undefined;
afterEach(async () => { if (root) await act(() => root!.unmount()); root = undefined; client?.clear(); document.body.replaceChildren(); vi.unstubAllGlobals(); window.history.replaceState(null, '', '/'); });
async function settle(work?: () => void) { await act(async () => { work?.(); await new Promise(resolve => setTimeout(resolve, 30)); }); }
function button(label: string) { const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === label); expect(node).toBeDefined(); return node!; }

const redirect = 'https://chatgpt.com/connector_platform_oauth_redirect';
const query = new URLSearchParams({ response_type: 'code', client_id: 'c1', redirect_uri: redirect, state: '0123', code_challenge: 'x'.repeat(43), code_challenge_method: 'S256', scope: 'read manage approve', resource: 'https://threavia.example/mcp' });

async function mount() {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  window.history.replaceState(null, '', `/oauth/consent?${query}`);
  const { OAuthConsentView } = await import('../../../web/ui/src/routes/oauth-consent');
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const element = document.createElement('div'); document.body.append(element); root = createRoot(element);
  await settle(() => root!.render(h(QueryClientProvider, { client }, h(OAuthConsentView))));
  await settle();
}

it('grants read only unless more is ticked, whatever the client asked, and returns the state untouched', async () => {
  mocks.get.mockResolvedValue({ id: 'c1', name: 'ChatGPT', redirectUris: [redirect], createdAt: '2026-11-04T00:00:00Z' });
  mocks.post.mockResolvedValue({ redirectTo: `${redirect}?code=abc&state=0123` });
  await mount();
  expect(document.body.textContent).toContain('Connect ChatGPT to Threavia?');
  expect(document.body.textContent).toContain('chatgpt.com');
  const boxes = [...document.querySelectorAll<HTMLElement>('[role="checkbox"]')];
  expect(boxes.map(b => b.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false', 'false']);
  await settle(() => button('Allow').click());
  expect(mocks.post).toHaveBeenCalledWith('/api/v1/oauth/authorize', expect.objectContaining({ clientId: 'c1', redirectUri: redirect, state: '0123', scopes: ['read'], approved: true }));
});

it('refuses to offer consent for a return address the client did not register', async () => {
  mocks.get.mockResolvedValue({ id: 'c1', name: 'ChatGPT', redirectUris: ['https://elsewhere.example/cb'], createdAt: '2026-11-04T00:00:00Z' });
  await mount();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('not valid');
  expect([...document.querySelectorAll('button')].some(b => b.textContent === 'Allow')).toBe(false);
});
