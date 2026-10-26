// @vitest-environment jsdom
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '../../../web/ui/node_modules/@tanstack/react-query';
import type { Monitor } from '../src/api/types';
import { MonitorsPanel } from '../../../web/ui/src/components/monitors';

const requireUI = createRequire(resolve(process.cwd(), '../../web/ui/package.json'));
const { createElement: h, act } = requireUI('react') as {
 createElement: (component: unknown, props?: unknown, ...children: unknown[]) => unknown;
 act: (work: () => void | Promise<void>) => Promise<void>;
};
const { createRoot } = requireUI('react-dom/client') as {
 createRoot: (container: HTMLElement) => { render(node: unknown): void; unmount(): void };
};
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/api/client', () => ({ api: { get: mocks.get, post: mocks.post } }));
let root: ReturnType<typeof createRoot> | undefined;
let cache: QueryClient | undefined;
let values: Monitor[];
beforeEach(() => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
 values = ['one', 'two'].map(id => ({
  id, runId:'run', jobId:'job', sessionId:'session', backendId:'backend',
  description:`Watch ${id}`,command:'watch checks',state:'RUNNING',sequence:1,
  output:'<script>unsafe()</script>',startedAt:new Date().toISOString(),updatedAt:new Date().toISOString(),
  expiresAt:new Date(Date.now()+60_000).toISOString(),lastSeenAt:new Date().toISOString(),
  confirmed:true,notify:false,automaticResumesRemaining:20,
 }));
 mocks.get.mockReset().mockImplementation(() => Promise.resolve({items:structuredClone(values)}));
 mocks.post.mockReset().mockImplementation((path: string) => {
  const id = path.split('/')[4];
  values = values.map(m => m.id === id ? {...m,state:'STOPPED'} : m);
  return Promise.resolve();
 });
});
afterEach(async () => {
 if (root) await act(() => root?.unmount());
 root = undefined; cache?.clear(); cache = undefined;
 document.body.replaceChildren(); vi.unstubAllGlobals();
});
async function mount() {
 cache = new QueryClient({defaultOptions:{queries:{retry:false}}});
 const container=document.createElement('div');document.body.append(container);root=createRoot(container);
 await act(async () => {root?.render(h(QueryClientProvider,{client:cache},h(MonitorsPanel,{sessionId:'session',working:false})));await new Promise(resolve=>setTimeout(resolve,100));});
 await act(async () => {await new Promise(resolve=>setTimeout(resolve,50));});
}
it('shows idle monitoring and stops only the selected watch', async () => {
 await mount();
 expect(document.querySelector('summary')?.textContent).toContain('Waiting · 2 active monitors');
 expect(document.querySelector('script')).toBeNull();
 expect(document.querySelector('pre')?.textContent).toContain('<script>');
 const button=[...document.querySelectorAll('button')].find(b=>b.textContent==='Stop');
 await act(async()=>{button?.click();await new Promise(resolve=>setTimeout(resolve,100));});
 await vi.waitFor(()=>expect(mocks.post).toHaveBeenCalledWith('/api/v1/monitors/one/stop'));
 await act(async()=>{await new Promise(resolve=>setTimeout(resolve,100));});
 expect(document.querySelector('summary')?.textContent).toContain('Waiting · 1 active monitor');
 expect(values.find(m=>m.id==='two')?.state).toBe('RUNNING');
});
it('distinguishes stale activity and an exhausted automatic resumption limit', async () => {
 values=[{...values[0],confirmed:false,automaticResumesRemaining:0}];
 await mount();
 expect(document.querySelector('summary')?.textContent).toContain('1 unconfirmed monitor');
 expect(document.body.textContent).toContain('Automatic resumption limit reached');
});
it('shows a failed state read instead of claiming there are no watches', async () => {
 mocks.get.mockRejectedValue(new Error('Core unavailable'));
 await mount();
 expect(document.body.textContent).toContain('Background monitoring could not be checked');
});
