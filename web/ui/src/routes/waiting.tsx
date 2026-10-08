import { useEffect, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ArrowRight, CheckCircle2 } from 'lucide-react';

import { useAttention } from '@/api/queries';
import type { UserInputRequest, ValidationRequest } from '@/api/types';
import { AttentionPanel } from '@/components/attention';
import { TasksView } from '@/routes/tasks';

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
          {total === 0
            ? 'Nothing needs a decision.'
            : `${total} ${total === 1 ? 'request' : 'requests'} across your projects, the longest wait first.`}
        </p>
      </header>

      {attention.isPending && <p className="text-muted text-sm">Loading…</p>}

      {total === 0 && !attention.isPending && (
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
 * The landing page: what waits for the person when something does, the work
 * still owed otherwise.
 */
export function HomeView() {
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

  if (landing === 'tasks') return <TasksView />;
  // While the first answer is on its way, the inbox frame says it is loading.
  return <WaitingView />;
}
