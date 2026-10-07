import { Navigate, useParams } from '@tanstack/react-router';
import { CircleDot, Lock, Plus, Trash2, X } from 'lucide-react';
import { useState } from 'react';

import {
  useAddTaskDependency,
  useCreateTask,
  useDeleteTask,
  useReadyTasks,
  useRemoveTaskDependency,
  useTasks,
  useUpdateTask,
} from '@/api/queries';
import type { Task, TaskStatus } from '@/api/types';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useSelectedProject } from '@/use-project';
import { cn, humanise } from '@/lib/utils';

/** The moves that make sense from here. Blocked is derived, never set by hand. */
function nextStatuses(status: TaskStatus): TaskStatus[] {
  switch (status) {
    case 'TODO':
      return ['IN_PROGRESS', 'DONE'];
    case 'IN_PROGRESS':
      return ['DONE', 'TODO'];
    default:
      return ['TODO'];
  }
}

/**
 * Everything the Project still owes, and what can be done about it.
 *
 * It has a route of its own rather than a tab among project memory: a tab only
 * opens when the view around it is already on screen, so asking for tasks while
 * reading decisions did nothing at all. It is also what a client lands on,
 * because work still owed is the thing worth seeing before choosing what to do
 * next.
 */
export function TasksView() {
  // The route may name the Project, or the sidebar may be the only one who
  // knows which one is meant. Both lead here.
  const params = useParams({ strict: false });
  const selected = useSelectedProject();
  const projectId = params.projectId ?? selected;

  const [includeDone, setIncludeDone] = useState(false);
  const [title, setTitle] = useState('');

  const tasks = useTasks(projectId, includeDone);
  // The graph spans finished work too: a Task waiting on one that is done is
  // the normal way of being ready, and its title has to be readable either way.
  const everyTask = useTasks(projectId, true);
  const ready = useReadyTasks(projectId);
  const create = useCreateTask(projectId ?? '');

  if (!projectId) {
    return (
      <div className="text-muted flex flex-1 items-center justify-center p-6 text-sm">
        Create a project to begin.
      </div>
    );
  }

  // Landing on the bare path settles on the Project's own: one view reached by
  // two URLs leaves the sidebar unable to say which entry you are looking at.
  if (!params.projectId) {
    return <Navigate to="/projects/$projectId/tasks" params={{ projectId }} replace />;
  }

  const listed = tasks.data ?? [];
  const open = listed.filter((task) => task.status !== 'DONE');
  // Ready is derived from the dependency graph by Core and never stored: a Task
  // is ready when every Task it waits on is done. Asking is the only way to
  // know; working it out here would be a second opinion on the same question.
  const readyIds = new Set((ready.data ?? []).map((task) => task.id));

  const groups: { title: string; tasks: Task[]; blocked?: boolean }[] = [
    { title: 'In progress', tasks: open.filter((task) => task.status === 'IN_PROGRESS') },
    { title: 'Ready to start', tasks: open.filter((task) => readyIds.has(task.id)) },
    {
      title: 'Waiting on other work',
      tasks: open.filter((task) => task.status === 'TODO' && !readyIds.has(task.id)),
      blocked: true,
    },
    { title: 'Done', tasks: listed.filter((task) => task.status === 'DONE') },
  ];

  return (
    <div className="mx-auto flex min-h-0 w-full max-w-page flex-1 flex-col gap-5 overflow-y-auto p-6">
      <header>
        <h2 className="text-lg font-semibold">Tasks</h2>
        <p className="text-muted text-sm">
          {open.length === 0
            ? 'Nothing open. Ask an agent for something, or file a task.'
            : `${open.length} open · ${readyIds.size} ready to start`}
        </p>
      </header>

      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (!title.trim()) return;
          create.mutate(title.trim());
          setTitle('');
        }}
      >
        <Input
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          placeholder="What needs doing"
        />
        <Button variant="primary" type="submit" disabled={create.isPending}>
          Add
        </Button>
      </form>

      <CheckboxField checked={includeDone} onCheckedChange={setIncludeDone}>
        show finished
      </CheckboxField>

      {groups.map(
        (group) =>
          group.tasks.length > 0 && (
            <Group
              key={group.title}
              title={group.title}
              tasks={group.tasks}
              everyTask={everyTask.data ?? []}
              blocked={group.blocked}
            />
          ),
      )}
    </div>
  );
}

function Group({
  title,
  tasks,
  everyTask,
  blocked = false,
}: {
  title: string;
  tasks: Task[];
  everyTask: Task[];
  blocked?: boolean;
}) {
  const update = useUpdateTask();

  return (
    <section className="space-y-2">
      <h3 className="text-muted text-xs tracking-wide uppercase">{title}</h3>
      <ul className="border-border divide-border divide-y rounded-[--radius-card] border">
        {tasks.map((task) => (
          <li key={task.id} className="px-3 py-2.5">
            <div className="flex items-start gap-3">
              {blocked ? (
                <Lock className="text-muted mt-0.5 size-4 shrink-0" />
              ) : (
                <CircleDot
                  className={cn(
                    'mt-0.5 size-4 shrink-0',
                    task.status === 'DONE' ? 'text-ok' : 'text-accent',
                  )}
                />
              )}
              <div className="min-w-0 flex-1">
                <p className={cn('text-sm', task.status === 'DONE' && 'text-muted line-through')}>
                  {task.title}
                </p>
                {task.description && (
                  <p className="text-muted mt-0.5 text-xs whitespace-pre-wrap">
                    {task.description}
                  </p>
                )}
              </div>
              <span className="flex shrink-0 items-center gap-3">
                {nextStatuses(task.status).map((status) => (
                  <Button
                    key={status}
                    variant="link"
                    onClick={() => update.mutate({ id: task.id, status })}
                  >
                    {humanise(status)}
                  </Button>
                ))}
                <DeleteTask task={task} />
              </span>
            </div>
            <div className="pl-7">
              <Dependencies task={task} tasks={everyTask} />
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * Taking a Task out of the Project.
 *
 * It asks first, in place: deleting is not undoable, and the row it would take
 * away is right there to be read again. What was done about the Task stays in
 * the timeline either way.
 */
function DeleteTask({ task }: { task: Task }) {
  const remove = useDeleteTask();
  const [asking, setAsking] = useState(false);

  if (asking) {
    return (
      <span className="text-muted flex items-center gap-2 text-xs">
        Delete?
        <Button variant="ghost" size="sm" onClick={() => setAsking(false)}>
          No
        </Button>
        <Button
          variant="danger"
          size="sm"
          disabled={remove.isPending}
          onClick={() => remove.mutate(task.id)}
        >
          Delete
        </Button>
      </span>
    );
  }

  return (
    <Button
      variant="ghost"
      size="icon"
      className="size-7 [&_svg]:size-3.5"
      title="Delete this task"
      onClick={() => setAsking(true)}
    >
      <Trash2 />
    </Button>
  );
}

/**
 * Whether making `task` wait on `blocker` would close a loop.
 *
 * Core refuses such an edge, because nothing on a cycle is ever ready. The
 * whole graph is already on screen, so the choice can simply not be offered
 * rather than be offered and refused.
 */
function wouldCycle(tasks: Task[], task: Task, blocker: Task): boolean {
  const byId = new Map(tasks.map((entry) => [entry.id, entry]));
  const seen = new Set<string>();
  const walk = (id: string): boolean => {
    if (id === task.id) return true;
    if (seen.has(id)) return false;
    seen.add(id);
    return (byId.get(id)?.dependsOn ?? []).some(walk);
  };
  return walk(blocker.id);
}

/**
 * What a Task waits on, and the means to change it.
 *
 * Dependencies were only settable when a Task was filed, which is the one
 * moment nobody knows the shape of the work yet. They are named rather than
 * counted: "depends on 2 tasks" says nothing a person can act on.
 */
function Dependencies({ task, tasks }: { task: Task; tasks: Task[] }) {
  const add = useAddTaskDependency();
  const remove = useRemoveTaskDependency();
  const [choosing, setChoosing] = useState(false);

  const blockers = (task.dependsOn ?? []).map((id) => ({
    id,
    task: tasks.find((entry) => entry.id === id),
  }));

  const candidates = tasks.filter(
    (entry) =>
      entry.id !== task.id &&
      !(task.dependsOn ?? []).includes(entry.id) &&
      !wouldCycle(tasks, task, entry),
  );

  const error = add.error ?? remove.error;

  return (
    <div className="mt-1.5 space-y-1.5">
      {blockers.length > 0 && (
        <ul className="flex flex-wrap items-center gap-1.5">
          <li className="text-muted text-xs">Waits on</li>
          {blockers.map((blocker) => (
            <li key={blocker.id}>
              <span className="bg-surface-2 border-border inline-flex items-center gap-1 rounded-md border py-0.5 pr-0.5 pl-2 text-xs">
                <span
                  className={blocker.task?.status === 'DONE' ? 'text-muted line-through' : undefined}
                >
                  {blocker.task?.title ?? 'a task of another project'}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-5 [&_svg]:size-3"
                  title="Stop waiting on this one"
                  disabled={remove.isPending}
                  onClick={() => remove.mutate({ id: task.id, dependsOn: blocker.id })}
                >
                  <X />
                </Button>
              </span>
            </li>
          ))}
        </ul>
      )}

      {choosing ? (
        <Select
          value=""
          onValueChange={(dependsOn) => {
            setChoosing(false);
            add.mutate({ id: task.id, dependsOn });
          }}
          onOpenChange={(opened) => {
            if (!opened) setChoosing(false);
          }}
          open
        >
          <SelectTrigger className="h-7 max-w-sm text-xs">
            <SelectValue placeholder="Which task must come first?" />
          </SelectTrigger>
          <SelectContent>
            {candidates.map((entry) => (
              <SelectItem key={entry.id} value={entry.id}>
                {entry.title}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        candidates.length > 0 && (
          <Button
            variant="ghost"
            size="sm"
            className="h-6 gap-1.5 px-1.5 text-xs [&_svg]:size-3"
            disabled={add.isPending}
            onClick={() => setChoosing(true)}
          >
            <Plus />
            Add a dependency
          </Button>
        )
      )}

      {error && <p className="text-danger text-xs">{error.message}</p>}
    </div>
  );
}
