import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './client';
import type { List, Monitor } from './types';

export const monitorActive = (m: Pick<Monitor, 'state'>) => ['STARTING', 'RUNNING', 'STOPPING'].includes(m.state);
export function useMonitors(kind: 'sessions' | 'projects', id: string) {
 return useQuery({ queryKey: ['monitors', kind, id], queryFn: () => (kind === 'sessions' ? api.get<List<Monitor>>(`/api/v1/sessions/${id}/monitors`) : api.get<List<Monitor>>(`/api/v1/projects/${id}/monitors`)).then((r) => r.items), enabled: Boolean(id), refetchInterval: 15_000 });
}
export function useStopMonitor() {
 const cache = useQueryClient();
 return useMutation({ mutationFn: (id: string) => api.post(`/api/v1/monitors/${id}/stop`), onSuccess: () => cache.invalidateQueries({ queryKey: ['monitors'] }) });
}
