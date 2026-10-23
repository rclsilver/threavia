import { useQueries } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { MCPProbe, MCPServer } from '@/api/mcp';

const backendKey = 'threavia-mcp-backend';
export function savedMCPBackend() {
  try { return sessionStorage.getItem(backendKey) || ''; } catch { return ''; }
}
export function rememberMCPBackend(id: string) {
  try { sessionStorage.setItem(backendKey, id); } catch { /* The selection still works for this visit. */ }
}

export function mcpConnectionKey(backend: string, server: MCPServer) {
  return ['mcp-connection', backend, server.id, server.revision] as const;
}

/** Read the real HTTP connection again after a page reload. Never start stdio
 * software, exchange an OAuth code or treat a saved browser flag as proof.
 */
export function useMCPConnections(servers: MCPServer[], backend: string, enabled: boolean) {
  const remote = servers.filter(server => server.id && server.oauth && server.transport === 'http');
  const queries = useQueries({ queries: remote.map(server => ({
    queryKey: mcpConnectionKey(backend, server),
    queryFn: async () => {
      const result = await api.query<MCPProbe>(`/api/v1/backends/${backend}/mcp`, { operation: 'probe', serverId: server.id });
      if (result.state !== 'CONNECTED' || result.serverId !== server.id) throw new Error('The backend did not confirm this connection.');
      return result;
    },
    enabled: !!backend && enabled,
    retry: false, staleTime: 30_000,
    refetchOnMount: 'always' as const,
    refetchOnWindowFocus: true,
  })) });
  return new Map(remote.map((server, index) => [server.id!, queries[index]]));
}
