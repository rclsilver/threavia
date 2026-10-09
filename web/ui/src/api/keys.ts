/** Query keys, in one place so an invalidation cannot miss a view by typo. */
export const keys = {
  projects: (includeArchived = false) => ['projects', { includeArchived }] as const,
  project: (projectId: string) => ['project', projectId] as const,
  sessions: (projectId: string, includeArchived = false) =>
    ['sessions', projectId, { includeArchived }] as const,
  // Under 'sessions', so whatever refreshes the lists refreshes this one too.
  pinned: () => ['sessions', 'pinned'] as const,
  snapshot: (sessionId: string) => ['snapshot', sessionId] as const,
  repository: (sessionId: string) => ['repository', sessionId] as const,
  policy: (sessionId: string) => ['policy', sessionId] as const,
  projectPolicy: (projectId: string) => ['project-policy', projectId] as const,
  attention: () => ['attention'] as const,
  audit: () => ['audit'] as const,
  me: () => ['me'] as const,
  backends: () => ['backends'] as const,
  backendSkills: (backendId: string) => ['backend-skills', backendId] as const,
  directories: (projectId: string) => ['directories', projectId] as const,
  bindings: (directoryId: string) => ['bindings', directoryId] as const,
  tasks: (projectId: string, includeDone: boolean) => ['tasks', projectId, { includeDone }] as const,
  readyTasks: (projectId: string) => ['tasks', projectId, 'ready'] as const,
  decisions: (projectId: string) => ['decisions', projectId] as const,
  artifacts: (projectId: string) => ['artifacts', projectId] as const,
  skills: (projectId: string) => ['skills', projectId] as const,
  schedules: (sessionId: string) => ['schedules', sessionId] as const,
  push: () => ['push'] as const,
};
