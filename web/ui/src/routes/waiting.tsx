import { lazy, Suspense, useEffect, useMemo, useState } from 'react';
import { Link, Navigate } from '@tanstack/react-router';
import { ArrowRight, CheckCircle2 } from 'lucide-react';

import { useAttention, useMe, useProjects, useSnapshot } from '@/api/queries';
import { ApiError } from '@/api/client';
import type { UserInputRequest, ValidationRequest } from '@/api/types';
import { AttentionPanel } from '@/components/attention';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { forgetSession, lastNavigation } from '@/lib/navigation';

const TasksView = lazy(() => import('@/routes/tasks').then((module) => ({ default: module.TasksView })));

/**
 * Everything waiting for the person, across every project, answerable here.
 *
 * Deciding an approval away from the desk is the job the product exists for,
 * and it must not begin by hunting through projects for the session that
 * asked. Each request says which session of which project it comes from, and
 * opens it for the full context.
 */
export function WaitingView() {
  const attention = useAttention();
  const validations = attention.data?.validations ?? [];
  const inputs = attention.data?.userInputs ?? [];
  const total = validations.length + inputs.length;

  // One group per session, in the order its oldest request arrived: the
  // longest wait first.
  const groups = new Map<string, { validations: ValidationRequest[]; inputs: UserInputRequest[] }>();
  for (const request of validations) {
    const group = groups.get(request.scope.sessionId) ?? { validations: [], inputs: [] };
    group.validations.push(request);
    groups.set(request.scope.sessionId, group);
  }
  for (const request of inputs) {
    const group = groups.get(request.scope.sessionId) ?? { validations: [], inputs: [] };
    group.inputs.push(request);
    groups.set(request.scope.sessionId, group);
  }

  return (
    <div className="mx-auto flex min-h-0 w-full max-w-reading flex-1 flex-col gap-6 overflow-y-auto p-4 sm:p-6">
      <header className="space-y-1">
        <h1 className="text-lg font-semibold">Waiting for you</h1>
        <p className="text-muted text-sm">
          {attention.isPending ? 'Checking what needs a decision…' : attention.isError ? 'Pending requests could not be checked.' : total === 0
            ? 'Nothing needs a decision.'
            : `${total} ${total === 1 ? 'request' : 'requests'} across your projects, the longest wait first.`}
        </p>
      </header>

      {attention.isPending && <p className="text-muted text-sm">Loading…</p>}
      {attention.isError && <div><ActionError error={attention.error} outcome="Pending requests did not load" /><Button onClick={() => void attention.refetch()}>Try again</Button></div>}

      {total === 0 && attention.isSuccess && (
        <div className="text-muted flex items-center gap-2 text-sm">
          <CheckCircle2 className="text-ok size-4" />
          The agents are not waiting on anything.
        </div>
      )}

      {[...groups.entries()].map(([sessionId, group]) => {
        const context = group.validations[0]?.context ?? group.inputs[0]?.context;
        return (
          <section key={sessionId} className="space-y-2" aria-label={context?.sessionTitle ?? 'Session'}>
            <Link
              to="/sessions/$sessionId"
              params={{ sessionId }}
              className="group hover:text-accent flex items-baseline gap-2"
            >
              <h2 className="min-w-0 truncate text-sm font-semibold">
                {context?.sessionTitle || 'Untitled session'}
              </h2>
              <span className="text-muted shrink-0 text-xs">{context?.projectName}</span>
              <ArrowRight className="text-muted group-hover:text-accent ml-auto size-4 shrink-0" />
            </Link>
            <AttentionPanel validations={group.validations} userInputs={group.inputs} />
          </section>
        );
      })}
    </div>
  );
}

/**
 * Resume this account's workspace on a root visit. Explicit links never pass
 * here; first visits retain the waiting/tasks dashboard.
 */
export function HomeView() {
  const me = useMe();
  const projects = useProjects();
  const saved = useMemo(() => lastNavigation(me.data?.userId), [me.data?.userId]);
  if (me.isError || projects.isError) return <div className="p-6"><ActionError error={me.error ?? projects.error} outcome="Your last workspace did not open" /><Button onClick={() => { void me.refetch(); void projects.refetch(); }}>Try again</Button></div>;
  if (!me.data || !projects.data) return <p role="status" className="text-muted p-6 text-sm">Opening your workspace…</p>;
  if (saved.projectId && projects.data.some((project) => project.id === saved.projectId)) {
    return saved.sessionId
      ? <ResumeSession userId={me.data.userId} projectId={saved.projectId} sessionId={saved.sessionId} />
      : <Navigate to="/sessions/new" search={{ projectId: saved.projectId }} replace />;
  }
  return <DashboardView />;
}

function ResumeSession({ userId, projectId, sessionId }: { userId: string; projectId: string; sessionId: string }) {
  const snapshot = useSnapshot(sessionId);
  const missing = snapshot.error instanceof ApiError && [403, 404].includes(snapshot.error.status);
  useEffect(() => {
    if (missing) forgetSession(userId, sessionId);
  }, [missing, userId, sessionId]);
  if (missing) return <Navigate to="/sessions/new" search={{ projectId }} replace />;
  if (snapshot.isError) return <div className="p-6"><ActionError error={snapshot.error} outcome="Your last session did not open" /><Button onClick={() => void snapshot.refetch()}>Try again</Button></div>;
  if (!snapshot.data) return <p role="status" className="text-muted p-6 text-sm">Opening your last session…</p>;
  return <Navigate to="/sessions/$sessionId" params={{ sessionId }} replace />;
}

function DashboardView() {
  const attention = useAttention();
  // Decided once, from the first answer: answering the last request on this
  // page must leave the page and its receipt where they are, not swap to the
  // tasks under the person's thumb.
  const [landing, setLanding] = useState<'waiting' | 'tasks' | null>(null);
  useEffect(() => {
    if (landing || !attention.data) return;
    const waiting = attention.data.validations.length + attention.data.userInputs.length;
    setLanding(waiting > 0 ? 'waiting' : 'tasks');
  }, [attention.data, landing]);

  if (landing === 'tasks') return <Suspense fallback={<p role="status" className="text-muted p-6 text-sm">Loading tasks…</p>}><TasksView /></Suspense>;
  // While the first answer is on its way, the inbox frame says it is loading.
  return <WaitingView />;
}
