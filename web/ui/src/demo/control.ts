import { ApiError } from '@/api/client';
import type { components } from '@/api/schema';
type Access = components['schemas']['ControlAccess'];
let accesses: Access[] = [{ id: '91000000-0000-4000-8000-000000000001', label: 'Development test controller', scopes: ['read', 'manage', 'answer'], createdAt: '2026-10-20T08:00:00Z', bootstrap: false }];
let next = 2;
export function demoControl(action: string, id: string, body: Record<string, unknown>): unknown {
  if (action === 'list') return { items: structuredClone(accesses) };
  if (action === 'create') {
    const granted = body.scopes as Access['scopes'];
    if (typeof body.label !== 'string' || !body.label.trim() || !Array.isArray(granted) || !granted.length || new Set(granted).size !== granted.length || granted.some(s => !['read', 'manage', 'delete', 'answer', 'approve'].includes(s))) throw new ApiError(400, 'invalid', 'Choose a name and explicit rights');
    const access: Access = { id: `91000000-0000-4000-8000-${String(next++).padStart(12, '0')}`, label: body.label, scopes: [...granted], createdAt: new Date().toISOString(), bootstrap: false, ...(typeof body.expiresAt === 'string' ? { expiresAt: body.expiresAt } : {}) };
    accesses = [...accesses, access]; return { access: structuredClone(access), token: 'fictional-demo-token-not-a-real-credential' };
  }
  const access = accesses.find(a => a.id === id);
  if (!access) throw new ApiError(404, 'not_found', 'This access was not found');
  if (action === 'revoke') { access.revokedAt ||= new Date().toISOString(); return; }
  throw new ApiError(400, 'invalid', 'Unknown MCP access operation');
}

// One fictional client registered itself, as ChatGPT would before consent.
const oauthClient: components['schemas']['OAuthClient'] = { id: '92000000-0000-4000-8000-000000000001', name: 'ChatGPT', redirectUris: ['https://chatgpt.com/connector_platform_oauth_redirect'], createdAt: '2026-11-04T08:00:00Z' };

export function demoOAuth(action: 'client' | 'authorize', id: string, body: Record<string, unknown>): unknown {
  if (action === 'client') {
    if (id !== oauthClient.id) throw new ApiError(404, 'not_found', 'This client was not found');
    return structuredClone(oauthClient);
  }
  if (body.clientId !== oauthClient.id || !oauthClient.redirectUris.includes(String(body.redirectUri))) throw new ApiError(400, 'invalid', 'Unknown client or redirect URI');
  const back = new URL(String(body.redirectUri));
  if (typeof body.state === 'string' && body.state) back.searchParams.set('state', body.state);
  if (!body.approved) {
    back.searchParams.set('error', 'access_denied');
    return { redirectTo: back.toString() };
  }
  const granted = body.scopes as Access['scopes'];
  accesses = [...accesses, { id: `91000000-0000-4000-8000-${String(next++).padStart(12, '0')}`, label: oauthClient.name, scopes: [...granted], createdAt: new Date().toISOString(), bootstrap: false, oauthClient: oauthClient.name }];
  back.searchParams.set('code', 'fictional-demo-code');
  return { redirectTo: back.toString() };
}
