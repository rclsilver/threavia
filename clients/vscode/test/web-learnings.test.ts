// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';
import { LearningCapture } from '../../../web/ui/src/components/learning-capture';
import type { Snapshot } from '../../../web/ui/src/api/types';
import { learningPrompt } from '../../shared/learnings';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/api/client', () => ({ api: mocks }));
let root: ReturnType<typeof createRoot>;
let queries: QueryClient;
let snapshot: Snapshot;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  snapshot = { session: { id: 'session', status: 'ACTIVE' }, jobs: [{ id: 'source', status: 'COMPLETED', createdAt: '2026-10-27T10:00:00Z' }] } as Snapshot;
  mocks.get.mockReset().mockImplementation(() => Promise.resolve(snapshot));
  mocks.post.mockReset().mockResolvedValue({ id: 'capture' });
  queries = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const container = document.createElement('div'); document.body.append(container); root = createRoot(container);
});
afterEach(async () => {
  await act(() => root.unmount()); queries.clear(); document.body.replaceChildren(); vi.unstubAllGlobals();
});
async function mount() {
  await act(() => root.render(h(QueryClientProvider, { client: queries }, h(LearningCapture, { snapshot }))));
}
async function click(text: string) {
  const button = [...document.querySelectorAll('button')].find(b => b.textContent === text);
  expect(button, text).toBeDefined();
  await act(async () => { button!.click(); await new Promise(resolve => setTimeout(resolve, 20)); });
}

it('asks before starting and Cancel creates no turn or memory mutation', async () => {
  await mount(); await click('Capture learnings');
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('subscription quota');
  expect(mocks.get).not.toHaveBeenCalled(); expect(mocks.post).not.toHaveBeenCalled();
  await click('Cancel');
  expect(document.querySelector('[role="dialog"]')).toBeNull(); expect(mocks.post).not.toHaveBeenCalled();
});

it('sends only a guided turn for the verified source and prevents duplicate clicks', async () => {
  let finish!: (job: { id: string }) => void;
  mocks.post.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  await mount(); await click('Capture learnings');
  const button = [...document.querySelectorAll('button')].find(b => b.textContent === 'Propose learnings')!;
  await act(async () => { button.click(); button.click(); await Promise.resolve(); });
  await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));
  expect(mocks.post).toHaveBeenCalledWith('/api/v1/sessions/session/messages', {
    message: learningPrompt('source'), delivery: 'QUEUE', afterJobId: 'source',
  }, { 'Idempotency-Key': 'capture-learnings:source' });
  await act(async () => { finish({ id: 'capture' }); await new Promise(resolve => setTimeout(resolve, 20)); });
  await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull());
});

it('keeps an actionable error and reuses the same key after an uncertain response', async () => {
  mocks.post.mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValueOnce({ id: 'capture' });
  await mount(); await click('Capture learnings'); await click('Propose learnings');
  await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain('Connection lost'));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Check the conversation');
  await click('Propose learnings');
  expect(mocks.post.mock.calls[0]).toEqual(mocks.post.mock.calls[1]);
});

it('refuses a source changed by another client and hides unfinished resolutions', async () => {
  await mount(); await click('Capture learnings');
  mocks.get.mockResolvedValue({ ...snapshot, jobs: [...snapshot.jobs, { id: 'new', status: 'RUNNING' }] });
  await click('Propose learnings');
  await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain('session changed'));
  expect(mocks.post).not.toHaveBeenCalled();
  await click('Cancel');
  snapshot = { ...snapshot, jobs: [{ ...snapshot.jobs[0], status: 'FAILED' }] };
  await mount();
  expect(document.querySelector('[data-tour="capture-learnings"]')).toBeNull();
});
