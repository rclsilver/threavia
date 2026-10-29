import { DEMO } from '@/demo/mode';

interface Navigation {
  projectId?: string;
  sessionId?: string;
  sessions: Record<string, string>;
}

// Only identifiers, scoped to the signed-in account on this Core. Demo visits
// must never replace the place left in the real application.
const key = (userId: string) => `threavia.navigation.${DEMO ? 'demo.' : ''}${userId}`;
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value);

export function lastNavigation(userId: string | undefined): Navigation {
  const empty: Navigation = { sessions: {} };
  if (!userId) return empty;
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key(userId)) ?? 'null');
    if (!value || typeof value !== 'object') return empty;
    const record = value as Record<string, unknown>;
    const sessions = Object.fromEntries(Object.entries(record.sessions && typeof record.sessions === 'object' ? record.sessions : {})
      .filter(([project, session]) => identifier(project) && identifier(session))) as Record<string, string>;
    const projectId = identifier(record.projectId) ? record.projectId : undefined;
    return { projectId, sessionId: projectId ? sessions[projectId] : undefined, sessions };
  } catch {
    return empty;
  }
}

export function rememberProject(userId: string | undefined, projectId: string, sessionId?: string) {
  if (!userId || !identifier(projectId) || (sessionId !== undefined && !identifier(sessionId))) return;
  const previous = lastNavigation(userId);
  try {
    localStorage.setItem(key(userId), JSON.stringify({
      projectId,
      sessions: { ...previous.sessions, ...(sessionId ? { [projectId]: sessionId } : {}) },
    }));
  } catch {
    // Private browsing or full storage must not prevent navigation.
  }
}

export function forgetSession(userId: string | undefined, sessionId: string) {
  if (!userId) return;
  const previous = lastNavigation(userId);
  const sessions = Object.fromEntries(Object.entries(previous.sessions).filter(([, id]) => id !== sessionId));
  try {
    localStorage.setItem(key(userId), JSON.stringify({ projectId: previous.projectId, sessions }));
  } catch { /* Navigation still works without persistence. */ }
}
