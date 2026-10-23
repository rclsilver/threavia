import { ApiError } from '@/api/client';
import type { MCPBinding, MCPRestriction, MCPServer } from '@/api/mcp';

const initial: MCPServer = { id: '90000000-0000-4000-8000-000000000001', revision: 1, name: 'documentation', transport: 'http', url: 'https://docs.example.test/mcp', enabled: true, oauth: false, tools: ['search_docs'], readTools: ['search_docs'], resources: true, prompts: false };
let servers: MCPServer[] = [initial];
const bindings: Record<string, MCPBinding[]> = {};
const restrictions: Record<string, string[] | null> = {};
let next = 2;
type Body = Record<string, unknown>;
const missing = () => { throw new ApiError(404, 'not_found', 'This MCP server was not found'); };
export function demoMCP(action: string, id: string, body: Body): unknown {
  switch (action) {
    case 'list': return { items: servers };
    case 'create': {
      const v = { ...body, id: `90000000-0000-4000-8000-${String(next++).padStart(12, '0')}`, revision: 1 } as unknown as MCPServer;
      if (!v.name || servers.some(s => s.name === v.name)) throw new ApiError(409, 'conflict', 'Choose a unique server name');
      servers.push(v); return v;
    }
    case 'update': {
      const old = servers.find(s => s.id === id); if (!old) return missing();
      if (old.revision !== body.revision) throw new ApiError(409, 'conflict', 'Reload the latest declaration before saving');
      const updated = { ...body, id, revision: (old.revision || 0) + 1 } as unknown as MCPServer; servers = servers.map(s => s.id === id ? updated : s); return updated;
    }
    case 'delete': if (!servers.some(s => s.id === id)) return missing(); servers = servers.filter(s => s.id !== id); for (const project of Object.keys(bindings)) bindings[project] = bindings[project].filter(b => b.serverId !== id); return;
    case 'bindings': return { items: bindings[id] || [] };
    case 'bind': { const v = body as unknown as MCPBinding; if (!servers.some(s => s.id === v.serverId)) return missing(); bindings[id] = [...(bindings[id] || []).filter(b => b.serverId !== v.serverId), v]; return; }
    case 'restriction': return { restricted: restrictions[id] != null, serverIds: restrictions[id] ?? null } satisfies MCPRestriction;
    case 'restrict': restrictions[id] = body.serverIds as string[] | null; return;
    case 'operation':
      if (body.operation === 'import') return { preview: true, servers: [{ declaration: { ...initial, id: undefined, revision: undefined, name: 'imported_docs', tools: [], readTools: [], enabled: false }, notes: ['Fictitious import preview; no backend files were read.'] }] };
      if (body.operation === 'probe') { if (!servers.some(s => s.id === body.serverId)) return missing(); return { state: 'CONNECTED', serverId: body.serverId, tools: [{ name: 'search_docs', description: 'Fictitious documentation search for trying explicit tool selection.' }, { name: 'publish_note', description: 'Fictitious write tool requiring approval.' }], resources: true, prompts: false }; }
      throw new ApiError(503, 'unavailable', 'OAuth sign-in is unavailable in the browser-only demo');
  }
  throw new ApiError(400, 'invalid_request', 'Unknown MCP demo action');
}
