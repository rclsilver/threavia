import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useState } from 'react';
import type { QueryClient } from '@tanstack/react-query';

import { api, idempotencyKey } from './client';
import { dropResolvedAttention } from './attention-cache';
import { keys } from './keys';
import type {
  Artifact,
  AuditEntry,
  BackendInstance,
  BackendSkill,
  Decision,
  Event,
  ExecutionPolicy,
  PushConfig,
  FileDiff,
  SessionPolicy,
  Handoff,
  Job,
  KnownDirectory,
  KnownDirectoryBinding,
  List,
  Me,
  Project,
  Schedule,
  Session,
  Skill,
  SkillFile,
  SkillSource,
  Snapshot,
  Task,
  UserInputRequest,
  TaskStatus,
  ValidationRequest,
} from './types';

const items = <T>(list: List<T>) => list.items ?? [];

// ------------------------------------------------------------------ projects

export function useProjects() {
  return useQuery({
    queryKey: keys.projects(),
    queryFn: () => api.get<List<Project>>('/api/v1/projects').then(items),
  });
}

export function useProject(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.project(projectId ?? ''),
    queryFn: () => api.get<Project>(`/api/v1/projects/${projectId}`),
    enabled: Boolean(projectId),
  });
}

export function useCreateProject() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.post<Project>('/api/v1/projects', { name }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['projects'] }),
  });
}

export function useUpdateProject(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<Pick<Project, 'name' | 'description' | 'instructions'>>) =>
      api.patch<Project>(`/api/v1/projects/${projectId}`, patch),
    onSuccess: (project) => {
      queries.setQueryData(keys.project(projectId), project);
      void queries.invalidateQueries({ queryKey: ['projects'] });
    },
  });
}

// --------------------------------------------------------------- directories

export function useDirectories(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.directories(projectId ?? ''),
    queryFn: () =>
      api.get<List<KnownDirectory>>(`/api/v1/projects/${projectId}/directories`).then(items),
    enabled: Boolean(projectId),
  });
}

export function useCreateDirectory(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; description?: string; gitRemote?: string | null }) =>
      api.post<KnownDirectory>(`/api/v1/projects/${projectId}/directories`, input),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.directories(projectId) }),
  });
}

export function useBindings(directoryId: string | undefined) {
  return useQuery({
    queryKey: keys.bindings(directoryId ?? ''),
    queryFn: () =>
      api
        .get<List<KnownDirectoryBinding>>(`/api/v1/directories/${directoryId}/bindings`)
        .then(items),
    enabled: Boolean(directoryId),
  });
}

// ------------------------------------------------------------------ backends

export function useBackends() {
  return useQuery({
    queryKey: keys.backends(),
    queryFn: () => api.get<List<BackendInstance>>('/api/v1/backends').then(items),
  });
}

/** Who Core thinks it is talking to, and whether anyone proved it. */
export function useMe() {
  return useQuery({
    queryKey: keys.me(),
    queryFn: () => api.get<Me>('/api/v1/me'),
    staleTime: Infinity,
  });
}

/**
 * A one-shot token a new backend registers with.
 *
 * Core returns it once and never again, so the result is held by the caller
 * rather than refetched: a token nobody copied in time is replaced by issuing
 * another, not by asking for the same one.
 */
export function useIssueBackendToken() {
  return useMutation({
    mutationFn: (input: { label: string; ttlSeconds: number }) =>
      api.post<{ token: string; expiresAt: string }>('/api/v1/backend-tokens', input),
  });
}

/**
 * Revoking the persistent credential of a backend.
 *
 * The record stays, because Sessions point at it; what goes is the credential,
 * and a returning backend must register anew.
 */
export function useRevokeBackend() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (backendId: string) => api.post<void>(`/api/v1/backends/${backendId}/revoke`, {}),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.backends() }),
  });
}

/** Taking ownership of an instance that registered without a user. */
export function useClaimBackend() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (claimCode: string) =>
      api.post<BackendInstance>('/api/v1/backends/claim', { claimCode }),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.backends() }),
  });
}

export function useBackendSkills(backendId: string | undefined) {
  return useQuery({
    queryKey: keys.backendSkills(backendId ?? ''),
    queryFn: () =>
      api.get<List<BackendSkill>>(`/api/v1/backends/${backendId}/skills`).then(items),
    enabled: Boolean(backendId),
  });
}

// ------------------------------------------------------------------ sessions

export function useSessions(projectId: string | undefined, includeArchived = false) {
  return useQuery({
    queryKey: keys.sessions(projectId ?? '', includeArchived),
    queryFn: () =>
      api
        .get<List<Session>>(
          `/api/v1/projects/${projectId}/sessions?includeArchived=${includeArchived}`,
        )
        .then(items),
    enabled: Boolean(projectId),
  });
}

/**
 * Archiving a Session, and bringing it back.
 *
 * Archiving is not deleting: the timeline, the costs and the decisions that
 * came out of it stay. It only takes the Session out of the list of what is
 * being worked on, which is the list a person reads twenty times a day.
 */
export function useArchiveSession() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ sessionId, archived }: { sessionId: string; archived: boolean }) =>
      api.post<Session>(`/api/v1/sessions/${sessionId}/${archived ? 'archive' : 'restore'}`, {}),
    onSuccess: (_session, { sessionId }) => {
      void queries.invalidateQueries({ queryKey: ['sessions'] });
      void queries.invalidateQueries({ queryKey: keys.snapshot(sessionId) });
    },
  });
}

export function useSnapshot(sessionId: string | undefined) {
  const queries = useQueryClient();
  return useQuery({
    queryKey: keys.snapshot(sessionId ?? ''),
    queryFn: async () => {
      const fresh = await api.get<Snapshot>(`/api/v1/sessions/${sessionId}`);
      // A snapshot is re-read when an event says something it cannot patch,
      // and it comes back as the recent window only. History the reader
      // already reached past that window stays, or it would vanish from under
      // them in the middle of reading it.
      const cached = queries.getQueryData<Snapshot>(keys.snapshot(sessionId ?? ''));
      const start = fresh.events[0]?.sequence;
      if (!cached || start === undefined) return fresh;
      const older = cached.events.filter((event) => event.sequence < start);
      return older.length ? { ...fresh, events: [...older, ...fresh.events] } : fresh;
    },
    enabled: Boolean(sessionId),
    // The stream keeps it current from here on: refetching on every focus would
    // only reorder what is already correct.
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
}

/**
 * The diff of one file a Job changed, computed by the backend when asked.
 *
 * Kept for the visit once read: the trees it compares never change, so asking
 * again would only cost the backend a git run.
 */
export function useFileDiff(sessionId: string, event: number, path: string, enabled: boolean) {
  return useQuery({
    queryKey: ['diff', sessionId, event, path],
    queryFn: () =>
      api.get<FileDiff>(
        `/api/v1/sessions/${sessionId}/diff?event=${event}&path=${encodeURIComponent(path)}`,
      ),
    enabled,
    staleTime: Infinity,
    retry: false,
  });
}

const EARLIER_PAGE = 200;

/**
 * Reaches past the window a snapshot opened with, one page at a time.
 *
 * Pages are folded into the cached snapshot rather than kept apart, so the
 * timeline has one list to read and the stream keeps appending to the same one.
 * A page shorter than asked for is the beginning of the Session.
 */
export function useEarlierEvents(sessionId: string) {
  const queries = useQueryClient();
  const [exhausted, setExhausted] = useState(false);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    setExhausted(false);
  }, [sessionId]);

  const snapshot = queries.getQueryData<Snapshot>(keys.snapshot(sessionId));
  const first = snapshot?.events[0];
  // The Session's own first event is the start of its history.
  const more = !exhausted && Boolean(first) && first?.type !== 'session.created';

  const load = useCallback(async () => {
    const oldest = queries.getQueryData<Snapshot>(keys.snapshot(sessionId))?.events[0];
    if (!oldest || loading || exhausted) return;
    setLoading(true);
    try {
      const page = items(
        await api.get<List<Event>>(
          `/api/v1/sessions/${sessionId}/events?before=${oldest.sequence}&limit=${EARLIER_PAGE}`,
        ),
      );
      if (page.length < EARLIER_PAGE) setExhausted(true);
      queries.setQueryData<Snapshot>(keys.snapshot(sessionId), (current) => {
        if (!current) return current;
        const known = new Set(current.events.map((event) => event.sequence));
        const earlier = page.filter((event) => !known.has(event.sequence));
        return { ...current, events: [...earlier, ...current.events] };
      });
    } finally {
      setLoading(false);
    }
  }, [queries, sessionId, loading, exhausted]);

  return { more, loading, load };
}

interface StartSession {
  projectId: string;
  backendInstanceId: string;
  workingDirectoryId: string | null;
  message: string;
  nativeSessionId?: string | null;
}

export function useStartSession() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: StartSession) =>
      api.post<{ session: Session; run: unknown; job: Job }>(
        '/api/v1/sessions/start',
        input,
        idempotencyKey(),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['sessions'] }),
  });
}

/** How a message reaches a Session already working (spec section 3.5). */
export type Delivery = 'QUEUE' | 'NEXT' | 'NOW';

export function usePostMessage(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ message, delivery = 'QUEUE' }: { message: string; delivery?: Delivery }) =>
      api.post<Job>(
        `/api/v1/sessions/${sessionId}/messages`,
        delivery === 'QUEUE' ? { message } : { message, delivery },
        delivery === 'QUEUE' ? idempotencyKey() : undefined,
      ),
    onSuccess: (_job, { delivery }) => {
      // An interruption drops what the interrupted turn was waiting on, which
      // Core resolves without an event of its own.
      if (delivery === 'NOW') {
        void queries.invalidateQueries({ queryKey: keys.attention() });
        void queries.invalidateQueries({ queryKey: keys.snapshot(sessionId) });
      }
    },
  });
}

/**
 * Renaming a Session.
 *
 * The title is how a Session is found again in a list of fifty, and the one the
 * agent wrote is a guess made from the first message.
 */
export function useRenameSession(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (title: string) => api.patch<Session>(`/api/v1/sessions/${sessionId}`, { title }),
    onSuccess: () => {
      void queries.invalidateQueries({ queryKey: keys.snapshot(sessionId) });
      void queries.invalidateQueries({ queryKey: ['sessions'] });
    },
  });
}

/**
 * Deleting a Session.
 *
 * Core refuses while a Job is still running, so the error is worth showing
 * rather than swallowing: it names what has to be stopped first.
 */
export function useDeleteSession() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (sessionId: string) => api.delete(`/api/v1/sessions/${sessionId}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['sessions'] }),
  });
}

export function useMoveSession(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (backendInstanceId: string) =>
      api.patch<Handoff>(`/api/v1/sessions/${sessionId}`, { backendInstanceId }),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.snapshot(sessionId) }),
  });
}

export function useCancelJob(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (jobId: string) =>
      api.post<Job>(`/api/v1/jobs/${jobId}/cancel`, { reason: 'cancelled from the web client' }),
    // CANCELLING has no event of its own, so the snapshot and the sidebar are
    // re-read to show it.
    onSuccess: () => {
      void queries.invalidateQueries({ queryKey: ['sessions'] });
      return queries.invalidateQueries({ queryKey: keys.snapshot(sessionId) });
    },
  });
}

// ----------------------------------------------------------------- attention

export function useAttention() {
  return useQuery({
    queryKey: keys.attention(),
    queryFn: () => api.get<{ validations: unknown; userInputs: unknown }>('/api/v1/me/attention'),
    select: (data) => ({
      validations: (data.validations ?? []) as NonNullable<Snapshot['attention']['validations']>,
      userInputs: (data.userInputs ?? []) as NonNullable<Snapshot['attention']['userInputs']>,
    }),
  });
}

export function useResolveValidation() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ id, approved, note }: { id: string; approved: boolean; note?: string }) =>
      api.post<ValidationRequest>(`/api/v1/validations/${id}/resolve`, {
        approved,
        note: note ?? '',
      }),
    onSuccess: (resolved) => {
      void queries.invalidateQueries({ queryKey: keys.attention() });
      // The stream normally does this first. Doing it here too is what makes
      // the card go when this client's own stream is down.
      dropResolvedAttention(queries, resolved.scope.sessionId, { validationId: resolved.id });
    },
  });
}

export function useResolveUserInput() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ id, value }: { id: string; value: string }) =>
      api.post<UserInputRequest>(`/api/v1/user-input/${id}/resolve`, { value }),
    onSuccess: (resolved) => {
      void queries.invalidateQueries({ queryKey: keys.attention() });
      dropResolvedAttention(queries, resolved.scope.sessionId, { userInputId: resolved.id });
    },
  });
}

// -------------------------------------------------------------------- policy

export function usePolicy(sessionId: string | undefined) {
  return useQuery({
    queryKey: keys.policy(sessionId ?? ''),
    queryFn: () => api.get<SessionPolicy>(`/api/v1/sessions/${sessionId}/policy`),
    enabled: Boolean(sessionId),
  });
}

/**
 * Sets a Session policy, or hands the Session back to its Project.
 *
 * Null is how it goes back: an empty body is what Core reads as "follow the
 * Project", and it is a different act from setting a policy that happens to
 * match the Project's today.
 */
export function useSetPolicy(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (policy: ExecutionPolicy | null) =>
      api.put<ExecutionPolicy>(`/api/v1/sessions/${sessionId}/policy`, policy ?? {}),
    onSuccess: () => {
      void queries.invalidateQueries({ queryKey: keys.policy(sessionId) });
      void queries.invalidateQueries({ queryKey: keys.audit() });
    },
  });
}

export function useProjectPolicy(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.projectPolicy(projectId ?? ''),
    queryFn: () => api.get<ExecutionPolicy>(`/api/v1/projects/${projectId}/policy`),
    enabled: Boolean(projectId),
  });
}

export function useSetProjectPolicy(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (policy: ExecutionPolicy) =>
      api.put<ExecutionPolicy>(`/api/v1/projects/${projectId}/policy`, policy),
    onSuccess: (policy) => {
      queries.setQueryData(keys.projectPolicy(projectId), policy);
      // Every Session that never overrode it now reads differently.
      void queries.invalidateQueries({ queryKey: ['policy'] });
      void queries.invalidateQueries({ queryKey: keys.audit() });
    },
  });
}

// ----------------------------------------------------------------- knowledge

export function useTasks(projectId: string | undefined, includeDone: boolean) {
  return useQuery({
    queryKey: keys.tasks(projectId ?? '', includeDone),
    queryFn: () =>
      api
        .get<List<Task>>(`/api/v1/projects/${projectId}/tasks?includeDone=${includeDone}`)
        .then(items),
    enabled: Boolean(projectId),
  });
}

/**
 * The Tasks that can be started now: TODO with every dependency done.
 *
 * Core derives this from the graph and never stores it, so asking is the only
 * way to know; computing it in the client would be a second opinion on the same
 * question.
 */
export function useReadyTasks(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.readyTasks(projectId ?? ''),
    queryFn: () =>
      api.get<List<Task>>(`/api/v1/projects/${projectId}/tasks/ready`).then(items),
    enabled: Boolean(projectId),
  });
}

export function useCreateTask(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (task: { title: string; description?: string; dependsOn?: string[] }) =>
      api.post<Task>(`/api/v1/projects/${projectId}/tasks`, { description: '', ...task }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['tasks'] }),
  });
}

export function useUpdateTask() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: TaskStatus }) =>
      api.patch<Task>(`/api/v1/tasks/${id}`, { status }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['tasks'] }),
  });
}

/**
 * Deleting a Task.
 *
 * An agent files tasks freely, so taking one back has to be as cheap as filing
 * it, or the list of what is left to do fills with work nobody meant to do.
 */
export function useDeleteTask() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (taskId: string) => api.delete(`/api/v1/tasks/${taskId}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['tasks'] }),
  });
}

/**
 * Making a Task wait on another, or stopping it.
 *
 * The graph decides what is ready, so an edge is a change to the whole list and
 * not only to the Task that was edited.
 */
export function useAddTaskDependency() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ id, dependsOn }: { id: string; dependsOn: string }) =>
      api.post<Task>(`/api/v1/tasks/${id}/dependencies`, { dependsOn }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['tasks'] }),
  });
}

export function useRemoveTaskDependency() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ id, dependsOn }: { id: string; dependsOn: string }) =>
      api.delete<Task>(`/api/v1/tasks/${id}/dependencies/${dependsOn}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['tasks'] }),
  });
}

/**
 * Deleting a Decision.
 *
 * For one that should never have been recorded. Superseding is the move when a
 * decision was changed, and Core restores whatever this one had replaced.
 */
export function useDeleteDecision() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (decisionId: string) => api.delete(`/api/v1/decisions/${decisionId}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['decisions'] }),
  });
}

export function useDecisions(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.decisions(projectId ?? ''),
    queryFn: () => api.get<List<Decision>>(`/api/v1/projects/${projectId}/decisions`).then(items),
    enabled: Boolean(projectId),
  });
}

export function useCreateDecision(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: { title: string; content: string; importance: string }) =>
      api.post<Decision>(`/api/v1/projects/${projectId}/decisions`, input),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['decisions'] }),
  });
}

/**
 * Pinning a Decision to every Job, or unpinning it.
 *
 * Shown at once: the row moves between its groups as the mark is pressed, and
 * goes back if Core refuses.
 */
export function useSetDecisionImportance(projectId: string) {
  const queries = useQueryClient();
  const key = keys.decisions(projectId);
  return useMutation({
    mutationFn: ({ id, importance }: { id: string; importance: Decision['importance'] }) =>
      api.patch<Decision>(`/api/v1/decisions/${id}`, { importance }),
    onMutate: async ({ id, importance }) => {
      await queries.cancelQueries({ queryKey: key });
      const before = queries.getQueryData<Decision[]>(key);
      queries.setQueryData<Decision[]>(key, (list) =>
        list?.map((decision) => (decision.id === id ? { ...decision, importance } : decision)),
      );
      return { before };
    },
    onError: (_error, _input, context) => queries.setQueryData(key, context?.before),
    onSettled: () => queries.invalidateQueries({ queryKey: ['decisions'] }),
  });
}

export function useProjectSearch(projectId: string | undefined, query: string) {
  return useQuery({
    queryKey: ['search', projectId, query],
    queryFn: () =>
      api.get<{ tasks: Task[]; decisions: Decision[]; history: Event[] }>(
        `/api/v1/projects/${projectId}/search?q=${encodeURIComponent(query)}`,
      ),
    enabled: Boolean(projectId) && query.trim().length > 1,
  });
}

// ----------------------------------------------------------------- artifacts

export function useArtifacts(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.artifacts(projectId ?? ''),
    queryFn: () => api.get<List<Artifact>>(`/api/v1/projects/${projectId}/artifacts`).then(items),
    enabled: Boolean(projectId),
  });
}

export function useUploadArtifact(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (file: File) =>
      api.upload<Artifact>(`/api/v1/projects/${projectId}/artifacts`, file),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.artifacts(projectId) }),
  });
}

export function useDeleteArtifact(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (artifactId: string) => api.delete(`/api/v1/artifacts/${artifactId}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.artifacts(projectId) }),
  });
}

// -------------------------------------------------------------------- skills

export function useSkills(projectId: string | undefined) {
  return useQuery({
    queryKey: keys.skills(projectId ?? ''),
    queryFn: () => api.get<List<Skill>>(`/api/v1/projects/${projectId}/skills`).then(items),
    enabled: Boolean(projectId),
  });
}

/**
 * What an installed Skill holds, read from the bundle Core stored. Asked for
 * only when someone opens it; a bundle never changes, so it is not asked twice.
 */
export function useSkillFiles(skillId: string | undefined) {
  return useQuery({
    queryKey: ['skill-files', skillId],
    queryFn: () => api.get<List<SkillFile>>(`/api/v1/skills/${skillId}/files`).then(items),
    enabled: Boolean(skillId),
    staleTime: Infinity,
  });
}

export function useInstallSkill(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: { source: SkillSource; file?: File; name?: string }) =>
      input.file
        ? api.upload<Skill>(`/api/v1/projects/${projectId}/skills`, input.file, {
            name: input.name ?? '',
            path: input.source.path ?? '',
          })
        : api.post<Skill>(`/api/v1/projects/${projectId}/skills`, {
            name: input.name,
            source: input.source,
          }),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.skills(projectId) }),
  });
}

export function useUninstallSkill(projectId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (skillId: string) => api.delete(`/api/v1/skills/${skillId}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.skills(projectId) }),
  });
}

// --------------------------------------------------------------------- audit

export function useAudit() {
  return useQuery({
    queryKey: keys.audit(),
    queryFn: () => api.get<List<AuditEntry>>('/api/v1/me/audit?limit=100').then(items),
  });
}

/** The client used by the stream, which writes to the same cache the hooks read. */
export type Cache = QueryClient;

// ------------------------------------------------------------------ schedules

export interface ScheduleInput {
  cron?: string;
  timezone?: string;
  message?: string;
  enabled?: boolean;
}

export function useSchedules(sessionId: string) {
  return useQuery({
    queryKey: keys.schedules(sessionId),
    queryFn: () => api.get<List<Schedule>>(`/api/v1/sessions/${sessionId}/schedules`).then(items),
  });
}

export function useCreateSchedule(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: ScheduleInput) =>
      api.post<Schedule>(`/api/v1/sessions/${sessionId}/schedules`, input),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.schedules(sessionId) }),
  });
}

export function useUpdateSchedule(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...input }: ScheduleInput & { id: string }) =>
      api.patch<Schedule>(`/api/v1/schedules/${id}`, input),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.schedules(sessionId) }),
  });
}

export function useDeleteSchedule(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.delete(`/api/v1/schedules/${id}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.schedules(sessionId) }),
  });
}

export function useRunSchedule(sessionId: string) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.post<Job>(`/api/v1/schedules/${id}/run`),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.schedules(sessionId) }),
  });
}

// ---------------------------------------------------------------------- push

export function usePushConfig() {
  return useQuery({
    queryKey: keys.push(),
    queryFn: () => api.get<PushConfig>('/api/v1/me/push'),
  });
}

export function useSubscribePush() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (input: { endpoint: string; keys: { p256dh: string; auth: string }; label: string }) =>
      api.post<{ id: string }>('/api/v1/me/push/subscriptions', input),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.push() }),
  });
}

export function useUnsubscribePush() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.delete(`/api/v1/me/push/subscriptions/${id}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: keys.push() }),
  });
}

export function useTestPush() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ browsers: number }>('/api/v1/me/push/test'),
    // Delivery is recorded on each subscription, a moment after.
    onSuccess: () => setTimeout(() => void queries.invalidateQueries({ queryKey: keys.push() }), 3000),
  });
}

/** A client instance with a live stream, as Core sees it. */
export interface ConnectedClient {
  id: string;
  name: string;
  channel: string;
  active: boolean;
  since: string;
}

export function useConnectedClients() {
  return useQuery({
    queryKey: ['clients'],
    queryFn: () => api.get<List<ConnectedClient>>('/api/v1/me/clients').then(items),
    // Presence changes without an event; a dialog left open follows it.
    refetchInterval: 15_000,
  });
}
