import { Navigate, useParams } from '@tanstack/react-router';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Circle,
  CircleCheck,
  CircleDot,
  Lock,
  MoreHorizontal,
  Plus,
  Trash2,
  X,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

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
import { EmptyState } from '@/components/ui/card';
import { ActionError } from '@/components/ui/action-error';
import { Input, Textarea } from '@/components/ui/input';
import {
  Menu,
  MenuCheckboxItem,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
} from '@/components/ui/menu';
import { useSelectedProject } from '@/use-project';
import { cn } from '@/lib/utils';

/** Where a Task stands, as the row shows it: ready and waiting are derived. */
type Standing = 'IN_PROGRESS' | 'READY' | 'WAITING' | 'DONE';

const STATUSES: { status: TaskStatus; label: string }[] = [
  { status: 'TODO', label: 'To do' },
  { status: 'IN_PROGRESS', label: 'In progress' },
  { status: 'DONE', label: 'Done' },
];

/** The one move a row offers outright: the next step, named as a verb. */
const NEXT: Partial<Record<Standing, { status: TaskStatus; label: string }>> = {
  READY: { status: 'IN_PROGRESS', label: 'Start' },
  IN_PROGRESS: { status: 'DONE', label: 'Mark done' },
};

/**
 * Everything the Project still owes, and what can be done about it.
 *
 * It has a route of its own rather than a tab among project memory: a tab only
 * opens when the view around it is already on screen, so asking for tasks while
 * reading decisions did nothing at all. It is also what a client lands on,
 * because work still owed is the thing worth seeing before choosing what to do
 * next.
 *
 * Agents file most Tasks, so the page is for steering what exists — what can
 * start, what waits on what — and filing one is a button, not the first and
 * largest thing on the page.
 */
export function TasksView() {
  // The route may name the Project, or the sidebar may be the only one who
  // knows which one is meant. Both lead here.
  const params = useParams({ strict: false });
  const selected = useSelectedProject();
  const projectId = params.projectId ?? selected;

  // One list, finished work included: the graph spans done Tasks too — a Task
  // waiting on one that is done is the normal way of being ready — and the
  // Done section counts them even while folded.
  const tasks = useTasks(projectId, true);
  const ready = useReadyTasks(projectId);

  const [filing, setFiling] = useState(false);
  const [filed, setFiled] = useState<Task>();
  // The mark on a Task just filed fades once it has been seen.
  useEffect(() => {
    if (!filed) return;
    const timer = setTimeout(() => setFiled(undefined), 2500);
    return () => clearTimeout(timer);
  }, [filed]);
  const [showDone, setShowDone] = useDoneShown(projectId);

  // N files a Task, as in the trackers this page is read next to — unless the
  // key was meant for a field, or a menu or dialog has it.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'n' || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement;
      if (target.closest('input, textarea, select, [contenteditable="true"], [role="menu"], [role="dialog"]')) return;
      event.preventDefault();
      setFiling(true);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

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

  const all = tasks.data ?? [];
  const open = all.filter((task) => task.status !== 'DONE');
  const done = all.filter((task) => task.status === 'DONE');
  // Ready is derived from the dependency graph by Core and never stored: a Task
  // is ready when every Task it waits on is done. Asking is the only way to
  // know; working it out here would be a second opinion on the same question.
  const readyIds = new Set((ready.data ?? []).map((task) => task.id));
  const loading = tasks.isPending || ready.isPending;

  const standing = (task: Task): Standing =>
    task.status === 'DONE'
      ? 'DONE'
      : task.status === 'IN_PROGRESS'
        ? 'IN_PROGRESS'
        : readyIds.has(task.id)
          ? 'READY'
          : 'WAITING';

  const groups: { title: string; standing: Standing }[] = [
    { title: 'In progress', standing: 'IN_PROGRESS' },
    { title: 'Ready to start', standing: 'READY' },
    { title: 'Waiting on other work', standing: 'WAITING' },
  ];

  return (
    <div className="mx-auto flex min-h-0 w-full max-w-page flex-1 flex-col gap-5 overflow-y-auto p-4 sm:p-6">
      <header className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold">Tasks</h2>
          <p className="text-muted text-sm">
            {loading
              ? 'Reading the list…'
              : open.length === 0
                ? 'Nothing open.'
                : `${open.length} open · ${readyIds.size} ready to start`}
          </p>
        </div>
        {!filing && (
          <Button
            size="lg"
            className="gap-1.5"
            title="File a task (N)"
            aria-keyshortcuts="N"
            onClick={() => setFiling(true)}
          >
            <Plus />
            New task
            <kbd className="text-muted border-border ml-1 hidden rounded border px-1 font-sans text-[0.6875rem] leading-4 sm:inline">
              N
            </kbd>
          </Button>
        )}
      </header>

      {filing && (
        <NewTask
          projectId={projectId}
          tasks={all}
          onClose={() => setFiling(false)}
          onFiled={(task) => {
            setFiling(false);
            setFiled(task);
          }}
        />
      )}

      {/* Said once a Task is filed, for whoever does not see where it landed. */}
      <p role="status" className="sr-only">
        {filed ? `Filed: ${filed.title}.` : ''}
      </p>

      {/* Readiness is what the groups are made of: until Core has answered,
          every Task would look like it is waiting. */}
      {loading ? null : ready.error || tasks.error ? (
        <ActionError
          error={ready.error ?? tasks.error}
          outcome="The list could not be read"
          recovery="It is tried again when you come back to this page."
        />
      ) : open.length === 0 && !filing ? (
        <EmptyState>
          Nothing open. Ask an agent for something, or file a task with{' '}
          <span className="text-text font-medium">New task</span>.
        </EmptyState>
      ) : null}

      {!loading &&
        groups.map((group) => {
          const members = open.filter((task) => standing(task) === group.standing);
          return (
            members.length > 0 && (
              <section key={group.title} className="space-y-2">
                <h3 className="text-muted text-xs tracking-wide uppercase">{group.title}</h3>
                <TaskList tasks={members} all={all} standing={standing} filed={filed?.id} />
              </section>
            )
          );
        })}

      {/* Finished work stays one click away, where the list ends, and counted
          so the click is not a guess. */}
      {!loading && done.length > 0 && (
        <section className="space-y-2">
          <button
            type="button"
            aria-expanded={showDone}
            onClick={() => setShowDone(!showDone)}
            className="text-muted hover:text-text -mx-1 flex items-center gap-1 rounded px-1 text-xs tracking-wide uppercase"
          >
            {showDone ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            Done · <span className="figures">{done.length}</span>
          </button>
          {showDone && <TaskList tasks={done} all={all} standing={standing} />}
        </section>
      )}
    </div>
  );
}

/** Whether a Project's finished Tasks are shown, remembered per Project. */
function useDoneShown(projectId: string | undefined): [boolean, (shown: boolean) => void] {
  const key = `threavia.tasks.done.${projectId ?? ''}`;
  const read = () => {
    try {
      return window.localStorage.getItem(key) === 'true';
    } catch {
      return false;
    }
  };
  const [shown, setShown] = useState(read);
  // Another Project, another choice.
  const [seenKey, setSeenKey] = useState(key);
  if (seenKey !== key) {
    setSeenKey(key);
    setShown(read());
  }
  const remember = (next: boolean) => {
    setShown(next);
    try {
      window.localStorage.setItem(key, String(next));
    } catch {
      // The choice lasts this visit instead.
    }
  };
  return [shown, remember];
}

/**
 * Filing a Task: a title, what it is about, and what has to happen first.
 *
 * All three at once, because the moment a Task is filed is when the person
 * knows what it waits on — not after hunting for the row it landed in.
 */
function NewTask({
  projectId,
  tasks,
  onClose,
  onFiled,
}: {
  projectId: string;
  tasks: Task[];
  onClose: () => void;
  onFiled: (task: Task) => void;
}) {
  const create = useCreateTask(projectId);
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [dependsOn, setDependsOn] = useState<string[]>([]);


  const submit = () => {
    if (!title.trim()) return;
    create.mutate(
      { title: title.trim(), description: description.trim(), dependsOn },
      { onSuccess: onFiled },
    );
  };

  return (
    <form
      className="bg-surface border-border space-y-3 rounded-xl border p-3 shadow-xs"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
      onKeyDown={(event) => {
        if (event.key === 'Escape' && !event.defaultPrevented) onClose();
      }}
    >
      <Input
        autoFocus
        aria-label="Title"
        value={title}
        onChange={(event) => setTitle(event.target.value)}
        placeholder="What needs doing?"
        className="h-auto border-0 px-1 py-1 text-[0.9375rem] font-medium shadow-none outline-none"
      />
      <Textarea
        aria-label="Details"
        rows={2}
        value={description}
        onChange={(event) => setDescription(event.target.value)}
        placeholder="Details, for whoever picks it up (optional)"
        className="min-h-14 resize-none border-0 px-1 py-0.5 shadow-none outline-none"
        onKeyDown={(event) => {
          // A newline belongs here, so only a modified Enter files.
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            submit();
          }
        }}
      />

      <div className="flex flex-wrap items-center gap-1.5 px-1">
        {dependsOn.length > 0 && <span className="text-muted text-xs">Waits on</span>}
        {dependsOn.map((id) => (
          <Chip
            key={id}
            title={tasks.find((task) => task.id === id)?.title ?? 'a task'}
            onRemove={() => setDependsOn(dependsOn.filter((entry) => entry !== id))}
          />
        ))}
        {/* A new Task cannot close a loop: nothing waits on it yet. Finished
            ones are offered too; waiting on one that is done is harmless and
            sometimes meant. */}
        {tasks.length > 0 && (
          <DependencyPicker
            label={dependsOn.length ? 'Change' : 'Waits on…'}
            options={tasks}
            chosen={dependsOn}
            onToggle={(id, checked) =>
              setDependsOn(checked ? [...dependsOn, id] : dependsOn.filter((entry) => entry !== id))
            }
          />
        )}
      </div>

      <ActionError
        error={create.error}
        outcome="Not filed"
        recovery="What you typed is still here; file it again."
      />

      <div className="border-border flex items-center justify-end gap-2 border-t pt-3">
        <span className="text-muted mr-auto hidden text-xs sm:inline">Enter files · Esc cancels</span>
        <Button type="button" variant="ghost" size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" size="lg" disabled={create.isPending || !title.trim()}>
          File task
        </Button>
      </div>
    </form>
  );
}

function TaskList({
  tasks,
  all,
  standing,
  filed,
}: {
  tasks: Task[];
  all: Task[];
  standing: (task: Task) => Standing;
  filed?: string;
}) {
  return (
    <ul className="border-border divide-border divide-y rounded-(--radius-card) border">
      {tasks.map((task) => (
        <TaskRow key={task.id} task={task} all={all} standing={standing(task)} fresh={task.id === filed} />
      ))}
    </ul>
  );
}

/**
 * One Task: where it stands on the left, what it is in the middle, the next
 * step and everything else on the right.
 *
 * The row used to end in every status it could move to, as lowercase words in
 * the accent colour, and a bin: four controls a row, thirteen rows, and the
 * eye read a column of blue words before any title. Now the status is the mark
 * it already had, which opens onto the others; the one move that usually comes
 * next is a verb; the rest waits behind one menu.
 */
function TaskRow({
  task,
  all,
  standing,
  fresh,
}: {
  task: Task;
  all: Task[];
  standing: Standing;
  fresh: boolean;
}) {
  const update = useUpdateTask();
  const remove = useDeleteTask();
  const add = useAddTaskDependency();
  const unlink = useRemoveTaskDependency();
  const [deleting, setDeleting] = useState(false);
  const [choosing, setChoosing] = useState(false);
  const [unfolded, setUnfolded] = useState(false);

  // A Task just filed is brought into view and marked for a moment, so the
  // person sees where it landed.
  const row = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (fresh) row.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [fresh]);

  // What it already waits on stays in the list, ticked, so it can be unticked
  // there too; a Task that would close a loop is not offered.
  const waitsOn = task.dependsOn ?? [];
  const options = all.filter(
    (entry) => entry.id !== task.id && (waitsOn.includes(entry.id) || !wouldCycle(all, task, entry)),
  );
  const next = NEXT[standing];
  const finished = task.status === 'DONE';

  return (
    <li
      ref={row}
      className={cn('flex flex-wrap items-start gap-2 px-2 py-2 transition-colors duration-1000 sm:gap-3 sm:px-3', fresh && 'bg-accent/5')}
    >
      <StatusControl task={task} standing={standing} onChange={(status) => update.mutate({ id: task.id, status })} />

      <div className="min-w-0 flex-1 py-0.5 sm:py-1">
        <p className={cn('text-sm font-medium break-words', finished && 'text-muted')}>{task.title}</p>
        {task.description && (
          // Two lines in the list; the rest on a click, for whoever wants it.
          <button
            type="button"
            onClick={() => setUnfolded(!unfolded)}
            aria-expanded={unfolded}
            className={cn(
              'text-muted mt-0.5 block w-full text-left text-xs whitespace-pre-wrap',
              !unfolded && 'line-clamp-2',
            )}
          >
            {task.description}
          </button>
        )}

        <Dependencies task={task} tasks={all} />

        {choosing && (
          <div className="mt-1.5">
            <DependencyPicker
              open
              label="Waits on…"
              options={options}
              chosen={waitsOn}
              onToggle={(dependsOn, checked) =>
                (checked ? add : unlink).mutate({ id: task.id, dependsOn })
              }
              onClose={() => setChoosing(false)}
            />
          </div>
        )}

        <ActionError
          error={remove.error ?? update.error ?? add.error ?? unlink.error}
          recovery="The task is unchanged; try again."
          className="mt-1.5 text-xs"
        />
      </div>

      <div className="flex shrink-0 items-center gap-1">
        {next && (
          <Button
            size="sm"
            className="max-sm:h-11 max-sm:px-3"
            disabled={update.isPending}
            onClick={() => update.mutate({ id: task.id, status: next.status })}
            aria-label={`${next.label}: ${task.title}`}
          >
            {next.label}
          </Button>
        )}
        <Menu>
          <MenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="data-[state=open]:bg-surface-2 data-[state=open]:text-text size-11 sm:size-8"
              aria-label={`More for ${task.title}`}
              title="More"
            >
              <MoreHorizontal />
            </Button>
          </MenuTrigger>
          <MenuContent align="end" className="min-w-48">
            {options.length > 0 && (
              <MenuItem onSelect={() => setChoosing(true)}>
                <Lock className="text-muted size-4" />
                Dependencies…
              </MenuItem>
            )}
            {options.length > 0 && <MenuSeparator />}
            <MenuItem className="text-danger" onSelect={() => setDeleting(true)}>
              <Trash2 className="size-4" />
              Delete…
            </MenuItem>
          </MenuContent>
        </Menu>
      </div>

      {/* The question takes the whole width under the Task it is about, so
          the row keeps its shape on any screen. */}
      {(deleting || remove.error) && (
        <div className="flex basis-full flex-wrap items-center gap-2 pb-1 pl-[3.25rem] text-sm sm:pl-11">
          <span className="text-muted mr-1">Delete this task?</span>
          <Button variant="ghost" size="lg" onClick={() => setDeleting(false)}>
            No
          </Button>
          <Button variant="danger" size="lg" disabled={remove.isPending} onClick={() => remove.mutate(task.id)}>
            Delete
          </Button>
        </div>
      )}
    </li>
  );
}

const MARKS: Record<Standing, { Icon: typeof Circle; tone: string; name: string }> = {
  READY: { Icon: Circle, tone: 'text-muted', name: 'Ready to start' },
  IN_PROGRESS: { Icon: CircleDot, tone: 'text-accent', name: 'In progress' },
  WAITING: { Icon: Lock, tone: 'text-muted', name: 'Waiting on other work' },
  DONE: { Icon: CircleCheck, tone: 'text-ok', name: 'Done' },
};

/**
 * The mark at the start of a row, which is also where its status changes.
 *
 * One glyph per standing, so the list scans by shape: an empty circle can
 * start, a lock waits, a dot is under way, a check is done.
 */
function StatusControl({
  task,
  standing,
  onChange,
}: {
  task: Task;
  standing: Standing;
  onChange: (status: TaskStatus) => void;
}) {
  const { Icon, tone, name } = MARKS[standing];
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          type="button"
          aria-label={`Status: ${name}. Change the status of ${task.title}`}
          title={`${name} — change the status`}
          className="hover:bg-surface-2 data-[state=open]:bg-surface-2 flex size-11 shrink-0 items-center justify-center rounded-md sm:size-8"
        >
          <Icon className={cn('size-4', tone)} />
        </button>
      </MenuTrigger>
      <MenuContent align="start" className="min-w-40">
        <MenuLabel>Status</MenuLabel>
        {STATUSES.map(({ status, label }) => (
          <MenuItem key={status} onSelect={() => status !== task.status && onChange(status)}>
            <span className="min-w-0 flex-1">{label}</span>
            {status === task.status && <Check className="text-muted size-3.5" />}
          </MenuItem>
        ))}
      </MenuContent>
    </Menu>
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
 * What a Task waits on, named rather than counted: "depends on 2 tasks" says
 * nothing a person can act on.
 */
function Dependencies({ task, tasks }: { task: Task; tasks: Task[] }) {
  const remove = useRemoveTaskDependency();
  const blockers = (task.dependsOn ?? []).map((id) => ({
    id,
    task: tasks.find((entry) => entry.id === id),
  }));
  if (blockers.length === 0) return null;

  return (
    <div className="mt-1.5 space-y-1">
      <ul className="flex flex-wrap items-center gap-1.5">
        <li className="text-muted text-xs">Waits on</li>
        {blockers.map((blocker) => (
          <li key={blocker.id} className="min-w-0 max-w-full">
            <Chip
              title={blocker.task?.title ?? 'a task outside this project'}
              done={blocker.task?.status === 'DONE'}
              disabled={remove.isPending}
              onRemove={() => remove.mutate({ id: task.id, dependsOn: blocker.id })}
            />
          </li>
        ))}
      </ul>
      <ActionError error={remove.error} recovery="It still waits on it; try again." className="text-xs" />
    </div>
  );
}

/** A Task named as a dependency, removable. */
function Chip({
  title,
  done = false,
  disabled,
  onRemove,
}: {
  title: string;
  done?: boolean;
  disabled?: boolean;
  onRemove: () => void;
}) {
  return (
    <span className="bg-surface-2 border-border inline-flex max-w-full items-center gap-1 rounded-md border py-0.5 pr-0.5 pl-2 text-xs">
      {done && <CircleCheck className="text-ok size-3 shrink-0" />}
      <span className={cn('truncate', done && 'text-muted')}>{title}</span>
      {/* Drawn small, hit large: the target reaches past the chip on a phone. */}
      <button
        type="button"
        title="Stop waiting on this one"
        aria-label={`Stop waiting on ${title}`}
        disabled={disabled}
        onClick={onRemove}
        className="text-muted hover:text-text hover:bg-border/60 relative flex size-5 shrink-0 items-center justify-center rounded before:absolute before:-inset-3 before:content-[''] disabled:opacity-50 sm:before:-inset-1"
      >
        <X className="size-3" />
      </button>
    </span>
  );
}

/**
 * The Tasks to wait on, ticked and unticked in one go.
 *
 * A Task often waits on several others — the partition walls wait on the roof,
 * the windows and the doors — and one choice per opening made that three trips
 * through a menu. Each tick takes effect as it is made; the menu stays open
 * until the person is done.
 */
function DependencyPicker({
  label,
  options,
  chosen,
  onToggle,
  open,
  onClose,
}: {
  label: string;
  options: Task[];
  chosen: readonly string[];
  onToggle: (id: string, checked: boolean) => void;
  /** Opened from elsewhere (a row's menu) rather than by its own button. */
  open?: boolean;
  onClose?: () => void;
}) {
  // Open work first, then what is finished: the one being waited on is
  // usually still to do.
  const ordered = [...options].sort((a, b) => Number(a.status === 'DONE') - Number(b.status === 'DONE'));
  return (
    <Menu {...(open ? { open: true, onOpenChange: (opened: boolean) => !opened && onClose?.() } : {})}>
      <MenuTrigger asChild>
        <Button variant="ghost" size="sm" className="text-muted h-11 gap-1.5 px-2 text-xs sm:h-7 [&_svg]:size-3">
          <Plus />
          {label}
        </Button>
      </MenuTrigger>
      <MenuContent align="start" className="w-80 max-w-[calc(100vw-2rem)]">
        <MenuLabel>Waits on — tick every task that must come first</MenuLabel>
        {ordered.map((entry) => (
          <MenuCheckboxItem
            key={entry.id}
            checked={chosen.includes(entry.id)}
            onCheckedChange={(checked) => onToggle(entry.id, checked)}
          >
            <span className={cn('min-w-0 flex-1 truncate', entry.status === 'DONE' && 'text-muted')}>
              {entry.title}
            </span>
            {entry.status === 'DONE' && <CircleCheck className="text-ok size-3.5 shrink-0" />}
          </MenuCheckboxItem>
        ))}
      </MenuContent>
    </Menu>
  );
}
