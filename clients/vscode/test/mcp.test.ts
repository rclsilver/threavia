import { expect, it } from 'vitest';
import { demoRequest } from '../../../web/ui/src/demo/server';
import { IDS } from '../../../web/ui/src/demo/data';
import type { MCPBinding, MCPServer } from '../../../web/ui/src/api/mcp';
import { effectsOf } from '../src/api/events';
import type { Event } from '../src/api/types';

it('plays MCP import as a preview, applies explicit project activation and refuses stale edits', async () => {
  const before = await demoRequest('GET', '/api/v1/me/mcp', null) as { items: MCPServer[] };
  const imported = await demoRequest('POST', `/api/v1/backends/${IDS.laptop}/mcp`, { operation: 'import', source: 'codex' }) as { preview: boolean; servers: { declaration: MCPServer }[] };
  expect(imported.preview).toBe(true);
  expect((await demoRequest('GET', '/api/v1/me/mcp', null) as { items: MCPServer[] }).items).toHaveLength(before.items.length);
  const created = await demoRequest('POST', '/api/v1/me/mcp', imported.servers[0].declaration) as MCPServer;
  await demoRequest('PUT', `/api/v1/projects/${IDS.homelab}/mcp`, { serverId: created.id, enabled: true, required: true });
  expect((await demoRequest('GET', `/api/v1/projects/${IDS.homelab}/mcp`, null) as { items: MCPBinding[] }).items).toContainEqual({ serverId: created.id, enabled: true, required: true });
  await demoRequest('PUT', `/api/v1/mcp/${created.id}`, { ...created, enabled: true });
  await expect(demoRequest('PUT', `/api/v1/mcp/${created.id}`, created)).rejects.toMatchObject({ status: 409 });
  await demoRequest('DELETE', `/api/v1/mcp/${created.id}`, null);
  expect((await demoRequest('GET', `/api/v1/projects/${IDS.homelab}/mcp`, null) as { items: MCPBinding[] }).items.some(b => b.serverId === created.id)).toBe(false);
});

it('exposes a dedicated cross-client MCP invalidation effect', () => {
  expect(effectsOf({ type: 'mcp.changed', payload: {} } as Event)).toEqual([{ kind: 'mcp' }]);
});
