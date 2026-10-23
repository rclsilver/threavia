// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Timeline } from '../../../web/ui/src/components/timeline';
import type { Event as TimelineEvent } from '../../../web/ui/src/api/types';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};

// Render every row while leaving the real timeline pairing and tool component intact.
vi.mock('../../../web/ui/node_modules/@tanstack/react-virtual', () => ({
  useVirtualizer: (options: { count: number; getItemKey: (index: number) => unknown }) => ({
    getVirtualItems: () => Array.from({ length: options.count }, (_, index) => ({ index, key: options.getItemKey(index), start: index * 48 })),
    getTotalSize: () => options.count * 48,
    getVirtualItemForOffset: () => undefined,
    measurementsCache: [],
    measureElement: vi.fn(),
    scrollToIndex: vi.fn(),
  }),
}));
vi.mock('@/api/queries', () => ({ useFileDiff: () => ({}) }));
vi.mock('@/components/agent-response', () => ({ AgentResponse: () => null }));
vi.mock('@/components/artifact-preview', () => ({ ArtifactCard: () => null }));

const start = { id: 'start', sequence: 1, type: 'tool.started', jobId: 'job', sessionId: 'session',
  timestamp: '2026-10-24T07:15:12Z', payload: { toolCallId: 'slow', name: 'mock_slow_read' } } as TimelineEvent;
const cancelled = { id: 'cancelled', sequence: 2, type: 'job.cancelled', jobId: 'job', sessionId: 'session',
  timestamp: '2026-10-24T07:15:19Z', payload: {} } as TimelineEvent;

let root: ReturnType<typeof createRoot>;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); disconnect = vi.fn(); });
  const mount = document.createElement('div'); document.body.append(mount); root = createRoot(mount);
});
afterEach(async () => {
  await act(async () => { root.unmount(); await Promise.resolve(); });
  document.body.replaceChildren(); vi.unstubAllGlobals();
});
async function render(events: TimelineEvent[]) {
  await act(async () => { root.render(createElement(Timeline, { events, pending: {}, onStop: vi.fn() })); await Promise.resolve(); });
}
async function openTool() {
  const button = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent?.includes('mock_slow_read'));
  expect(button).toBeDefined();
  await act(async () => { button!.click(); await Promise.resolve(); });
}

it('replaces a live spinner with an interrupted state when Stop cancels the job', async () => {
  await render([start]); await openTool();
  expect(document.body.textContent).toContain('Still running');
  expect(document.querySelector('.animate-spin')).not.toBeNull();
  await render([start, cancelled]);
  expect(document.body.textContent).not.toContain('Still running');
  expect(document.body.textContent).toContain('Interrupted when work was cancelled.');
  expect(document.querySelector('.animate-spin')).toBeNull();
  expect(document.querySelector('[aria-label="Interrupted"]')).not.toBeNull();
});

it('shows an interrupted call immediately when reopening cancelled history', async () => {
  await render([start, cancelled]); await openTool();
  expect(document.body.textContent).toContain('Interrupted when work was cancelled.');
  expect(document.body.textContent).not.toContain('Still running');
  expect(document.querySelector('.animate-spin')).toBeNull();
});
