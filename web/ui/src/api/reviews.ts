import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './client';
import type { components } from './schema';

export type Review = components['schemas']['Review'];
export type ReviewAction = components['schemas']['ReviewAction'];
export type ReviewThread = components['schemas']['ReviewThread'];
export type ReviewFileContent = components['schemas']['ReviewFileContent'];
export const reviewKey = (sessionId: string) => ['review', sessionId] as const;

export function useReview(sessionId: string) {
  const queries = useQueryClient();
  return useQuery({ queryKey: reviewKey(sessionId), queryFn: async () => {
    const result = await api.get<Review>(`/api/v1/sessions/${sessionId}/review`);
    const current = queries.getQueryData<Review>(reviewKey(sessionId));
    return current && current.version > result.version ? current : result;
  } });
}

export function useReviewAction(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: Omit<ReviewAction, 'expectedVersion'>) => {
      const current = queries.getQueryData<Review>(reviewKey(sessionId));
      return api.post<Review>(`/api/v1/sessions/${sessionId}/review`, { ...input, expectedVersion: current?.version ?? 0 });
    },
    onSuccess: (review) => {
      queries.setQueryData<Review>(reviewKey(sessionId), (current) => !current || review.version >= current.version ? review : current);
      void queries.invalidateQueries({ queryKey: ['snapshot', sessionId] });
    },
    onError: () => void queries.invalidateQueries({ queryKey: reviewKey(sessionId) }),
  });
}

export function useReviewComparison(sessionId: string, revisionId: string, compareTo: string) {
  return useQuery({ queryKey: ['review-comparison', sessionId, revisionId, compareTo],
    queryFn: () => api.get<Review>(`/api/v1/sessions/${sessionId}/review?${new URLSearchParams({ revisionId, compareTo })}`),
    enabled: Boolean(revisionId && compareTo), staleTime: Infinity, retry: false });
}

export function useReviewFile(sessionId: string, revisionId: string, repositoryId: string, path: string, compareTo: string) {
  return useQuery({
    queryKey: ['review-file', sessionId, revisionId, repositoryId, path, compareTo],
    queryFn: () => api.get<ReviewFileContent>(`/api/v1/sessions/${sessionId}/review/file?${new URLSearchParams({ revisionId, repositoryId, path, compareTo })}`),
    enabled: Boolean(revisionId && repositoryId && path),
    staleTime: Infinity,
    retry: false,
  });
}
