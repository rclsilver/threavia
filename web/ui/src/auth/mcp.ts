import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { MCPProbe } from '@/api/mcp';

type PendingAuth = { backend: string; serverId: string; state: string; response?: string; connected?: boolean };
type Problem = 'invalid' | 'denied' | 'provider';
type SignIn = { pending: PendingAuth | null; response: string; problem: Problem | null };
const authKey = 'threavia-mcp-auth';

function pendingAuth(): PendingAuth | null {
  try {
    const pending = JSON.parse(sessionStorage.getItem(authKey) || 'null') as PendingAuth | null;
    return pending && typeof pending.backend === 'string' && typeof pending.serverId === 'string' && typeof pending.state === 'string' ? pending : null;
  } catch { return null; }
}

export function rememberMCPAuth(backend: string, serverId: string, state: string) {
  sessionStorage.setItem(authKey, JSON.stringify({ backend, serverId, state } satisfies PendingAuth));
}

function returnedAuth(): SignIn | null {
  const pending = pendingAuth();
  const params = new URLSearchParams(window.location.search);
  const fromURL = ['state', 'code', 'error'].some(key => params.has(key));
  const response = fromURL ? window.location.href : pending?.response;
  if (!response) return pending?.connected === true ? { pending, response: '', problem: null } : null;
  try {
    const url = new URL(response);
    const matches = pending && url.origin === window.location.origin && url.pathname === '/settings/mcp' && url.searchParams.get('state') === pending.state;
    if (!matches) return fromURL ? { pending, response, problem: 'invalid' } : null;
    const error = url.searchParams.get('error');
    return { pending, response, problem: error === 'access_denied' ? 'denied' : error ? 'provider' : url.searchParams.get('code') ? null : 'invalid' };
  } catch { return null; }
}

function clearPending(state: string) {
  if (pendingAuth()?.state === state) sessionStorage.removeItem(authKey);
}

/** OAuth is exchanged once per return, including React StrictMode and remounts.
 * These queries are outside the registry namespace so editing declarations or
 * testing another server cannot replay a one-use authorization code.
 */
export function useMCPSignIn() {
  const [flow, setFlow] = useState(returnedAuth);
  const pending = flow?.pending;
  useEffect(() => {
    if (!flow) return;
    if (pending && flow.response && !flow.problem && pendingAuth()?.state === pending.state && pendingAuth()?.connected !== true) {
      sessionStorage.setItem(authKey, JSON.stringify({ ...pending, response: flow.response }));
    } else if (pending && flow.problem && flow.problem !== 'invalid') {
      clearPending(pending.state);
    }
    window.history.replaceState(window.history.state, '', window.location.pathname);
  }, [flow, pending]);

  const finish = useQuery({
    queryKey: ['mcp-sign-in', pending?.backend, pending?.serverId, pending?.state],
    enabled: !!pending && !!flow?.response && !flow.problem && pending.connected !== true,
    queryFn: async () => {
      if (!pending || !flow) throw new Error('Start a new connection to sign in.');
      const result = await api.post<{ state: string }>(`/api/v1/backends/${pending.backend}/mcp`, { operation: 'auth_finish', serverId: pending.serverId, state: pending.state, response: flow.response });
      if (result.state !== 'AUTHENTICATED') throw new Error('The backend did not confirm sign-in.');
      // A reload after exchange must only repeat discovery, never the exchange.
      if (pendingAuth()?.state === pending.state) {
        sessionStorage.setItem(authKey, JSON.stringify({ backend: pending.backend, serverId: pending.serverId, state: pending.state, connected: true } satisfies PendingAuth));
      }
      return true;
    },
    retry: false, staleTime: Infinity, gcTime: 600_000,
    refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  const connected = !!pending && !flow?.problem && (pending.connected === true || finish.isSuccess);
  const discovery = useQuery({
    queryKey: ['mcp-sign-in-discovery', pending?.backend, pending?.serverId, pending?.state],
    enabled: connected,
    queryFn: () => api.post<MCPProbe>(`/api/v1/backends/${pending!.backend}/mcp`, { operation: 'probe', serverId: pending!.serverId }),
    retry: false, staleTime: Infinity, gcTime: 600_000,
    refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  useEffect(() => {
    if (pending && (finish.isError || discovery.isSuccess || discovery.isError)) clearPending(pending.state);
  }, [pending, finish.isError, discovery.isSuccess, discovery.isError]);

  return { flow, connected, finishing: finish.isFetching, checking: discovery.isFetching,
    finishError: finish.error, discoveryError: discovery.error, discovery: discovery.data,
    recheck: () => { void discovery.refetch(); }, reset: () => setFlow(null) };
}

export type MCPSignIn = ReturnType<typeof useMCPSignIn>;
