import { expect, it, vi } from 'vitest';
import { QueryClient, QueryObserver } from '../../../web/ui/node_modules/@tanstack/react-query';
import { api, setTransport } from '../../../web/ui/src/api/client';

it('rechecks MCP reads without recursively invalidating their own demo query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const result = { state: 'CONNECTED', serverId: 'mock', tools: [] };
  const request = vi.fn(() => {
    if (request.mock.calls.length > 5) throw new Error('Recursive invalidation');
    return Promise.resolve(result);
  });
  const changed = vi.fn(() => { void client.invalidateQueries(); });
  setTransport({ request, changed, upload: () => Promise.resolve(undefined), blob: () => Promise.resolve(new Blob()) });
  const options = { queryKey: ['mcp-connection', 'backend', 'mock'], staleTime: 30_000,
    queryFn: () => api.query('/api/v1/backends/backend/mcp', { operation: 'probe', serverId: 'mock' }) };
  await client.fetchQuery(options);
  const observer = new QueryObserver(client, options);
  const unsubscribe = observer.subscribe(() => {});
  try {
    await observer.refetch();
    expect(request).toHaveBeenCalledTimes(2);
    expect(changed).not.toHaveBeenCalled();
    // Real changes still notify the demo and recheck the observed connection.
    await api.post('/api/v1/mcp/mock', { enabled: false });
    await vi.waitFor(() => expect(observer.getCurrentResult().fetchStatus).toBe('idle'));
    expect(changed).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledTimes(4);
  } finally { unsubscribe(); client.clear(); }
});
