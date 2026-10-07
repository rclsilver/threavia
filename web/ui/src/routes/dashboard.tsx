import { Link } from '@tanstack/react-router';
import { ArrowRight, CircleDot, Lock } from 'lucide-react';

import { useReadyTasks, useTasks } from '@/api/queries';
import type { Task } from '@/api/types';
import { useSelectedProject } from '@/use-project';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { humanise } from '@/lib/utils';

/**
 * What the Project still owes, on the page a client lands on.
 *
 * This was a line telling the reader to go and select something. Tasks are the
 * one piece of Project memory that says what is left to do, and they were a tab
 * behind a link: visible only to someone who already knew to look. Landing on
 * them is the difference between memory an agent writes and memory anyone
 * reads.
 */
export function DashboardView() {
  const projectId = useSelectedProject();
  const tasks = useTasks(projectId, false);
  const ready = useReadyTasks(projectId);

  if (!projectId) {
    return (
      <div className="text-muted flex flex-1 items-center justify-center p-6 text-sm">
        Create a project to begin.
      </div>
    );
  }

  const open = tasks.data ?? [];
  // Ready is derived from the dependency graph by Core, never stored: a Task is
  // ready when every Task it waits on is done. The rest are waiting on
  // something, which is worth saying rather than hiding.
  const readyIds = new Set((ready.data ?? []).map((task) => task.id));
  const blocked = open.filter((task) => task.status === 'TODO' && !readyIds.has(task.id));
  const underway = open.filter((task) => task.status === 'IN_PROGRESS');

  return (
    <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col gap-6 overflow-y-auto p-6">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">Open tasks</h2>
          <p className="text-muted text-sm">
            {open.length === 0
              ? 'Nothing open. Ask an agent for something, or file a task.'
              : `${open.length} open · ${readyIds.size} ready to start`}
          </p>
        </div>
        <Button asChild variant="secondary" size="sm">
          <Link to="/projects/$projectId" params={{ projectId }}>
            All project memory
            <ArrowRight />
          </Link>
        </Button>
      </header>

      {underway.length > 0 && <Group title="In progress" tasks={underway} />}
      {readyIds.size > 0 && (
        <Group title="Ready to start" tasks={open.filter((task) => readyIds.has(task.id))} />
      )}
      {blocked.length > 0 && <Group title="Waiting on other work" tasks={blocked} blocked />}
    </div>
  );
}

function Group({
  title,
  tasks,
  blocked = false,
}: {
  title: string;
  tasks: Task[];
  blocked?: boolean;
}) {
  return (
    <section className="space-y-2">
      <h3 className="text-muted text-xs tracking-wide uppercase">{title}</h3>
      <ul className="border-border divide-border divide-y rounded-[--radius-card] border">
        {tasks.map((task) => (
          <li key={task.id} className="flex items-start gap-3 px-3 py-2.5">
            {blocked ? (
              <Lock className="text-muted mt-0.5 size-4 shrink-0" />
            ) : (
              <CircleDot className="text-accent mt-0.5 size-4 shrink-0" />
            )}
            <div className="min-w-0 flex-1">
              <p className="text-sm">{task.title}</p>
              {task.description && (
                <p className="text-muted mt-0.5 line-clamp-2 text-xs">{task.description}</p>
              )}
            </div>
            {task.status === 'IN_PROGRESS' && <Badge tone="warn">{humanise(task.status)}</Badge>}
          </li>
        ))}
      </ul>
    </section>
  );
}
