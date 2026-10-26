import { ApiError } from '@/api/client';
import type { components } from '@/api/schema';
type Access = components['schemas']['ControlAccess'];
let accesses: Access[] = [{ id: '91000000-0000-4000-8000-000000000001', label: 'Development test controller', scopes: ['read', 'manage', 'answer'], createdAt: '2026-10-20T08:00:00Z', bootstrap: false }];
let next = 2;
export function demoControl(action: string, id: string, body: Record<string, unknown>): unknown {
  if (action === 'list') return { items: structuredClone(accesses) };
  if (action === 'create') {
    const granted = body.scopes as Access['scopes'];
    if (typeof body.label !== 'string' || !body.label.trim() || !Array.isArray(granted) || !granted.length || new Set(granted).size !== granted.length || granted.some(s => !['read', 'manage', 'answer', 'approve'].includes(s))) throw new ApiError(400, 'invalid', 'Choose a name and explicit rights');
    const access: Access = { id: `91000000-0000-4000-8000-${String(next++).padStart(12, '0')}`, label: body.label, scopes: [...granted], createdAt: new Date().toISOString(), bootstrap: false, ...(typeof body.expiresAt === 'string' ? { expiresAt: body.expiresAt } : {}) };
    accesses = [...accesses, access]; return { access: structuredClone(access), token: 'fictional-demo-token-not-a-real-credential' };
  }
  const access = accesses.find(a => a.id === id);
  if (!access) throw new ApiError(404, 'not_found', 'This access was not found');
  if (action === 'revoke') { access.revokedAt ||= new Date().toISOString(); return; }
  throw new ApiError(400, 'invalid', 'Unknown MCP access operation');
}
