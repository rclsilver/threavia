// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';
import { SessionView } from '../../../web/ui/src/routes/session';
import type { BackendInstance, Session, Snapshot } from '../../../web/ui/src/api/types';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
const mocks = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));
vi.mock('@/api/client', () => ({ api: mocks }));
vi.mock('../../../web/ui/node_modules/@tanstack/react-router', () => ({
  useParams: () => ({ sessionId: 'session' }), Link: ({ children }: { children: string }) => h('a', {}, children),
}));
vi.mock('@/use-stream', () => ({ useStream: () => ({ activity: () => undefined }) }));
vi.mock('@/components/timeline', () => ({ Timeline: () => null }));
vi.mock('@/components/attention', () => ({ AttentionPanel: () => null }));
vi.mock('@/components/repository-status', () => ({ RepositoryStatus: () => null }));
vi.mock('@/components/monitors', () => ({ MonitorsPanel: () => null }));
vi.mock('@/components/review-notice', () => ({ ReviewNotice: () => null }));
vi.mock('@/components/backend-info', () => ({ BackendInfo: () => null }));

let root: ReturnType<typeof createRoot>;
let queries: QueryClient;
let session: Session;
const backend = { id: 'backend', features: ['MODEL_SELECTION'], modelCatalog: { defaultModel: 'first', defaultEffort: 'medium', models: [
  { id: 'first', name: 'First', efforts: ['low', 'high'], defaultEffort: 'high' },
  { id: 'second', name: 'Second', efforts: ['low'], defaultEffort: 'low' },
] } } as BackendInstance;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  window.matchMedia = vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })) as unknown as typeof window.matchMedia;
  HTMLElement.prototype.scrollIntoView = vi.fn();
  HTMLElement.prototype.hasPointerCapture = () => false;
  HTMLElement.prototype.setPointerCapture = vi.fn();
  HTMLElement.prototype.releasePointerCapture = vi.fn();
  session = { id: 'session', projectId: 'project', title: 'Model test', status: 'ACTIVE', model: 'first', effort: 'high', createdAt: '2026-10-27T10:00:00Z', updatedAt: '2026-10-27T10:00:00Z', readSequence: 0 };
  mocks.get.mockReset().mockImplementation((path: string) => Promise.resolve(path === '/api/v1/sessions/session'
    ? { session, runs: [{ id: 'run', backendInstanceId: 'backend', createdAt: session.createdAt }], jobs: [], events: [], attention: {}, cursor: 0 } as unknown as Snapshot
    : { items: path === '/api/v1/backends' ? [backend] : [] }));
  mocks.patch.mockReset(); mocks.post.mockReset();
  queries = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const container = document.createElement('div'); document.body.append(container); root = createRoot(container);
});
afterEach(async () => {
  await act(() => root.unmount()); queries.clear();
  document.body.replaceChildren(); window.localStorage.clear(); vi.unstubAllGlobals();
});
async function mount() {
  await act(async () => { root.render(h(QueryClientProvider, { client: queries }, h(SessionView))); await new Promise(resolve => setTimeout(resolve, 50)); });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 50)); });
}
async function choose(label: string, text: string) {
  const trigger = document.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
  await act(() => { trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })); });
  const option = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(element => element.textContent === text);
  expect(option).toBeDefined();
  await act(async () => { option!.click(); await Promise.resolve(); });
}
it('saves inline choices, resets effort for the new model, and waits before sending', async () => {
  let complete!: (value: Session) => void;
  mocks.patch.mockImplementation(() => new Promise<Session>(resolve => { complete = resolve; }));
  await mount();
  expect(document.querySelector('header [data-tour="session-model"]')).toBeNull();
  expect(document.querySelector('form [data-tour="session-model"]')).not.toBeNull();
  const field = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]')!;
  // Use the native setter so React sees a real input change.
  await act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, 'Next task');
    field.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await choose('Model', 'Second');
  expect(mocks.patch).toHaveBeenLastCalledWith('/api/v1/sessions/session', { model: 'second', effort: '' });
  expect(document.querySelector<HTMLButtonElement>('button[aria-label="Send"]')?.disabled).toBe(true);
  await act(() => { field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })); });
  expect(field.value).toBe('Next task'); expect(mocks.post).not.toHaveBeenCalled();
  await act(async () => { session = { ...session, model: 'second', effort: '' }; complete(session); await new Promise(resolve => setTimeout(resolve, 50)); });
  expect(document.querySelector('[aria-label="Model"]')?.textContent).toContain('Second');
  expect(document.querySelector<HTMLButtonElement>('button[aria-label="Send"]')?.disabled).toBe(false);
  mocks.patch.mockImplementation((_path: string, values: { model: string; effort: string }) => { session = { ...session, ...values }; return Promise.resolve(session); });
  await choose('Reasoning effort', 'low');
  expect(mocks.patch).toHaveBeenLastCalledWith('/api/v1/sessions/session', { model: 'second', effort: 'low' });
});
it('reports a refused save and retains the confirmed model', async () => {
  mocks.patch.mockRejectedValue(new Error('Offline'));
  await mount();
  await choose('Model', 'Second');
  expect(document.body.textContent).toContain('Model settings not saved');
  expect(document.querySelector('[aria-label="Model"]')?.textContent).toContain('First');
});
