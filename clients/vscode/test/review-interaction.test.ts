// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ReviewView } from '../../../web/ui/src/routes/review';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '../../../web/ui/node_modules/@tanstack/react-router';

// Use the UI's React instance, just as the components do. The extension itself
// has no React runtime dependency.
const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement, act } = requireUI('react') as {
  createElement: (component: unknown, props?: unknown) => unknown;
  act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
  createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
const fixture = vi.hoisted(() => ({
  review: { sessionId: 'session', version: 1, required: true, status: 'WAITING', threads: [], seen: {},
    repositories: [{ id: 'repo', name: 'Repository', directory: '/repo', baselineSource: 'BEFORE_WORK' }],
    revisions: [{ id: 'revision', repositories: [{ repositoryId: 'repo', headTree: 'tree', files: [{ path: 'code.ts', state: 'MODIFIED' }] }] }],
  },
  file: { before: 'old\ncontext\n', after: 'new\ncontext\n', diff: '@@ -1,2 +1,2 @@\n-old\n+new\n context\n', binary: false, truncated: false },
}));
vi.mock('@/api/reviews', () => ({
  useReview: () => ({ data: fixture.review }),
  useReviewAction: () => ({ isPending: false, mutateAsync: vi.fn().mockResolvedValue(fixture.review) }),
  useReviewComparison: () => ({}),
  useReviewFile: () => ({ data: fixture.file, isPending: false }),
}));
vi.mock('@/api/queries', () => ({ useSnapshot: () => ({ data: { session: { title: 'Review test' } } }) }));

let root: ReturnType<typeof createRoot>;
beforeEach(async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  const mount = document.createElement('div'); document.body.append(mount);
  root = createRoot(mount);
  const routeRoot = createRootRoute();
  const route = createRoute({ getParentRoute: () => routeRoot, path: '/sessions/$sessionId/review', component: ReviewView });
  const router = createRouter({ routeTree: routeRoot.addChildren([route]), history: createMemoryHistory({ initialEntries: ['/sessions/session/review'] }) });
  await act(async () => { await router.load(); root.render(createElement(RouterProvider, { router })); });
});
afterEach(async () => { await act(async () => { root.unmount(); await Promise.resolve(); }); document.body.replaceChildren(); vi.unstubAllGlobals(); });

async function click(selector: string) {
  const element = document.querySelector<HTMLButtonElement>(selector)!;
  expect(element).not.toBeNull();
  await act(async () => { element.focus({ preventScroll: true }); element.click(); await Promise.resolve(); });
}
function button(text: string) { return [...document.querySelectorAll<HTMLButtonElement>('button')].find((element) => element.textContent === text)!; }

it('selects a phone range without opening or focusing an editor, then returns to the same code position', async () => {
  const panel = document.querySelector<HTMLElement>('.review-file-panel')!;
  panel.scrollTop = 240;
  await click('[aria-label="Comment on after line 1"]');
  expect(document.querySelector('textarea')).toBeNull();
  expect(panel.scrollTop).toBe(240);
  await act(async () => { button('Range').click(); await Promise.resolve(); });
  await click('[aria-label="Comment on after line 2"]');
  expect(document.querySelector('textarea')).toBeNull();
  expect(document.querySelector('[aria-label="Comment line selection"]')?.textContent).toContain('after 1–2');
  await act(async () => { button('Comment').focus(); button('Comment').click(); await Promise.resolve(); });
  expect(document.querySelector('[role="dialog"]')).not.toBeNull();
  expect(document.activeElement?.hasAttribute('data-review-editor-heading')).toBe(true);
  expect((document.querySelector('[aria-label="First comment line"]') as HTMLInputElement).value).toBe('1');
  expect((document.querySelector('[aria-label="Last comment line"]') as HTMLInputElement).value).toBe('2');
  expect(document.querySelector('.review-comment-source')?.textContent).toBe('new\ncontext');
  expect(panel.scrollTop).toBe(240);
  await act(async () => { button('Done').click(); await Promise.resolve(); });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(panel.scrollTop).toBe(240);
  await act(async () => { await vi.waitFor(() => expect(document.activeElement?.textContent).toBe('Comment')); });
});

it('opens the phone file picker over the code without replacing or scrolling it', async () => {
  const panel = document.querySelector<HTMLElement>('.review-file-panel')!; panel.scrollTop = 180;
  await click('[aria-label="Choose file"]');
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Files · 1');
  expect(panel.scrollTop).toBe(180);
  expect(document.querySelector('.review-file-panel')).toBe(panel);
});
