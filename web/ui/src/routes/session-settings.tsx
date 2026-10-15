import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { Archive, ArrowLeft, CalendarClock, Cpu, Server, ShieldCheck, type LucideIcon } from 'lucide-react';
import { useCallback, useState } from 'react';

import {
  useArchiveSession,
  useBackends,
  useDeleteSession,
  useMoveSession,
  useHandoffPreview,
  useSessionArtifacts,
  useSnapshot,
} from '@/api/queries';
import { SessionModelPanel } from '@/components/session-model-panel';
import { PolicyPanel } from '@/components/policy-panel';
import { SchedulesPanel } from '@/components/schedules-panel';
import { ConfirmLine } from '@/components/record-list';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { BackendIcon } from '@/components/backend-info';
import { cn, humanise } from '@/lib/utils';
import { useShortcut } from '@/use-shortcut';

export type SettingsTab = 'permissions' | 'schedules' | 'backend' | 'session' | 'model';

const TABS: { tab: SettingsTab; label: string; Icon: LucideIcon; detail: string }[] = [
  { tab: 'model', label: 'Model & effort', Icon: Cpu, detail: 'The model and reasoning effort used by this session. Changes apply when the next job starts.' },
  {
    tab: 'permissions',
    label: 'Permissions',
    Icon: ShieldCheck,
    detail: 'What the agent may do in this session. Enforced by Core and by the backend, never by the prompt alone.',
  },
  {
    tab: 'schedules',
    label: 'Schedules',
    Icon: CalendarClock,
    detail:
      'Messages sent to this session at set times, as if you typed them. One is skipped, and the conversation says so, when the previous job is still running or the backend is away: nothing is sent late or piled up.',
  },
  {
    tab: 'backend',
    label: 'Backend',
    Icon: Server,
    detail: 'The machine that runs this session’s work.',
  },
  {
    tab: 'session',
    label: 'Archive & delete',
    Icon: Archive,
    detail: 'Taking the session out of the list, or out of the project for good.',
  },
];

/**
 * Everything about a Session that is not the conversation, as a page of its own.
 *
 * It was a dialog holding all of it at once — permissions, schedules, the
 * backend, archiving, deleting — one under the other in a box a third of the
 * window wide. Each part now has its tab and its URL, and the whole width; the
 * conversation is one step back, by the arrow or Escape.
 */
export function SessionSettingsView() {
  const { sessionId, tab } = useParams({ from: '/sessions/$sessionId/settings/$tab' });
  const snapshot = useSnapshot(sessionId);
  const navigate = useNavigate();
  const current = TABS.find((entry) => entry.tab === tab) ?? TABS[0];

  // Escape goes back to the conversation, as it closed the dialog this was.
  useShortcut(
    'Escape',
    useCallback(() => void navigate({ to: '/sessions/$sessionId', params: { sessionId } }), [navigate, sessionId]),
  );

  const session = snapshot.data?.session;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="border-border/70 border-b px-3 pt-3 sm:px-5">
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="icon" className="size-11 sm:size-8" title="Back to the conversation (Esc)">
            <Link to="/sessions/$sessionId" params={{ sessionId }} aria-label="Back to the conversation">
              <ArrowLeft />
            </Link>
          </Button>
          <div className="min-w-0">
            <h2 className="truncate text-[0.9375rem] font-semibold">{session?.title || 'Untitled session'}</h2>
            <p className="text-muted text-xs">Session settings</p>
          </div>
          {session?.status === 'ARCHIVED' && <Badge className="ml-auto">Archived</Badge>}
        </div>
        {/* Tabs as links: each part has an address, and the back button
            walks back through them as it would through pages. */}
        <nav aria-label="Settings" className="-mb-px mt-3 flex gap-1 overflow-x-auto">
          {TABS.map(({ tab: value, label, Icon }) => (
            <Link
              key={value}
              to="/sessions/$sessionId/settings/$tab"
              params={{ sessionId, tab: value }}
              aria-current={value === current.tab ? 'page' : undefined}
              className={cn(
                'flex min-h-11 shrink-0 items-center gap-1.5 border-b-2 px-3 text-sm whitespace-nowrap sm:min-h-10',
                value === current.tab
                  ? 'border-accent text-text font-medium'
                  : 'text-muted hover:text-text border-transparent',
              )}
            >
              <Icon className="size-4" />
              {label}
            </Link>
          ))}
        </nav>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl space-y-5 p-4 sm:p-6">
          <p className="text-muted text-sm">{current.detail}</p>
          {current.tab === 'model' && <SessionModelPanel sessionId={sessionId} />}
          {current.tab === 'permissions' && <PolicyPanel sessionId={sessionId} />}
          {current.tab === 'schedules' && <SchedulesPanel sessionId={sessionId} />}
          {current.tab === 'backend' && <MoveSession sessionId={sessionId} />}
          {current.tab === 'session' && (
            <ArchiveAndDelete
              sessionId={sessionId}
              projectId={session?.projectId}
              archived={session?.status === 'ARCHIVED'}
            />
          )}
        </div>
      </div>
    </div>
  );
}

/**
 * Moving a Session to another backend, and saying what it costs.
 *
 * The move is explicit and the timeline stays continuous, but the native
 * provider session does not travel: it belongs to the machine holding it
 * (spec section 34).
 */
function MoveSession({ sessionId }: { sessionId: string }) {
  const backends = useBackends();
  const snapshot = useSnapshot(sessionId);
  const move = useMoveSession(sessionId);
  const [target, setTarget] = useState('');

  const runs = [...(snapshot.data?.runs ?? [])].sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  const currentId = runs.at(-1)?.backendInstanceId;
  const preview = useHandoffPreview(sessionId, target === currentId ? '' : target);
  const destination = preview.data;
  const current = backends.data?.find((backend) => backend.id === currentId);
  const usable = (backends.data ?? []).filter(
    (backend) => backend.ownershipStatus !== 'REVOKED' && backend.id !== currentId,
  );

  return (
    <div className="space-y-5">
      <div className="bg-surface border-border flex items-center gap-3 rounded-(--radius-card) border p-3">
        <Server className="text-muted size-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{current?.name ?? 'No backend yet'}</p>
          <p className="text-muted text-xs">
            {current ? `Runs this session now · ${humanise(current.operationalStatus)}` : 'The first message picks one.'}
          </p>
        </div>
      </div>

      <div className="space-y-2">
        <h3 className="text-sm font-medium">Move it to another backend</h3>
        <p className="text-muted text-sm">
          The timeline stays continuous. The provider session does not travel, so the next message
          starts a fresh one there.
        </p>
        {usable.length === 0 ? (
          <p className="text-muted text-sm">No other backend is available.</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            <Select value={target} onValueChange={(value) => { setTarget(value); move.reset(); }}>
              <SelectTrigger className="h-11 min-w-56 flex-1 sm:h-9">
                <SelectValue placeholder="Choose a backend" />
              </SelectTrigger>
              <SelectContent>
                {usable.map((backend) => (
                  <SelectItem key={backend.id} value={backend.id} disabled={backend.capacity.maxConcurrentRuns <= 0 || backend.capacity.activeRuns >= backend.capacity.maxConcurrentRuns}>
                    <span className="flex items-center gap-2"><BackendIcon provider={backend.backend} />{backend.name} ({backend.capacity.maxConcurrentRuns <= 0 ? 'Draining' : backend.capacity.activeRuns >= backend.capacity.maxConcurrentRuns ? 'Full' : humanise(backend.operationalStatus)})</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        {target && preview.isPending && <p className="text-muted text-sm">Checking the destination…</p>}
        <ActionError error={preview.error} outcome="Destination could not be checked" recovery="Choose the backend again or reload." />
        {destination && target !== currentId && (
          <div className="bg-surface border-border space-y-3 rounded-(--radius-card) border p-4 text-sm">
            <h4 className="font-medium">Destination: {destination.backend.name}</h4>
            <p className="text-muted">{destination.backend.backend || 'Provider not reported'} · {humanise(destination.backend.operationalStatus)} · {destination.backend.capacity.activeRuns}/{destination.backend.capacity.maxConcurrentRuns} runs</p>
            <p>Capabilities: {destination.backend.capabilities.map(humanise).join(', ') || 'None reported'}. Features: {destination.backend.features?.map(humanise).join(', ') || 'Queued messages only'}.</p>
            <p>Working directory: {destination.workingDirectory.resolution === 'bound'
              ? <><span>{destination.workingDirectory.name}</span> · <code className="break-all">{destination.workingDirectory.path}</code></>
              : destination.workingDirectory.resolution === 'resolve'
                ? `${destination.workingDirectory.name} — the backend must find or confirm its path before work starts.`
                : 'The destination backend’s default directory, selected when the job starts.'}</p>
            {destination.missingSkills.length > 0 && <p className="text-warn-text">Local skills missing there: {destination.missingSkills.join(', ')}.</p>}
            <p className="text-muted">Recent exchanges, work summaries and changed-file names will accompany the next message. The provider session starts fresh. Workspace files must already exist on the destination.</p>
            {destination.warnings.map((warning) => <p key={warning} className="text-warn-text">{warning}</p>)}
            {destination.reasons.map((reason) => <p key={reason} className="text-danger">{reason}</p>)}
            <Button size="lg" disabled={!destination.canMove || Boolean(preview.error) || preview.isFetching || move.isPending} onClick={() => move.mutate({ backendInstanceId: target, expectedRunId: destination.fromRunId }, { onSuccess: () => setTarget('') })}>
              {move.isPending ? 'Moving…' : `Move to ${destination.backend.name}`}
            </Button>
          </div>
        )}
        <ActionError error={move.error} outcome="Not moved" recovery="It stays where it was; try again." />
        {move.data?.missingSkills?.length ? (
          <p className="text-warn-text text-sm">
            Moved. These local skills are not on the new backend: {move.data.missingSkills.join(', ')}.
          </p>
        ) : null}
      </div>
    </div>
  );
}

/** The two ways out of the list: one that keeps everything, one that keeps nothing. */
function ArchiveAndDelete({
  sessionId,
  projectId,
  archived,
}: {
  sessionId: string;
  projectId?: string;
  archived: boolean;
}) {
  const archive = useArchiveSession();
  const remove = useDeleteSession();
  const files = useSessionArtifacts(projectId, sessionId);
  const navigate = useNavigate();
  const [asking, setAsking] = useState(false);
  const [artifacts, setArtifacts] = useState<'keep' | 'delete'>('keep');
  const made = files.data ?? [];

  return (
    <ul className="border-border divide-border divide-y rounded-(--radius-card) border">
      <li className="flex flex-wrap items-center gap-3 p-4">
        <div className="min-w-0 flex-1 basis-64">
          <p className="text-sm font-medium">{archived ? 'Archived' : 'Archive'}</p>
          <p className="text-muted text-sm">
            Keeps everything — the timeline, what it cost, what was decided — and only takes the
            session out of the list of what is being worked on.
          </p>
        </div>
        <Button
          size="lg"
          disabled={archive.isPending}
          onClick={() => archive.mutate({ sessionId, archived: !archived })}
        >
          {archived ? 'Restore' : 'Archive'}
        </Button>
        <ActionError error={archive.error} className="basis-full" />
      </li>
      <li className="flex flex-wrap items-center gap-3 p-4">
        <div className="min-w-0 flex-1 basis-64">
          <p className="text-danger text-sm font-medium">Delete</p>
          <p className="text-muted text-sm">
            The timeline, what it cost and what was asked go with it. Archiving is the move that keeps
            all of it.
            {made.length > 0 &&
              ` It made ${made.length} ${made.length === 1 ? 'file' : 'files'}; you choose what becomes of ${made.length === 1 ? 'it' : 'them'}.`}
          </p>
        </div>
        {!asking && (
          <Button size="lg" variant="ghost" className="text-danger" onClick={() => setAsking(true)}>
            Delete…
          </Button>
        )}
        {asking && (
          <div className="basis-full space-y-3">
            {/* The files are the one thing that may outlive the session, so
                what becomes of them is asked, named, before anything goes. */}
            {made.length > 0 && (
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">
                  Its {made.length === 1 ? 'file' : `${made.length} files`}:{' '}
                  <span className="text-muted figures font-mono text-xs font-normal">
                    {made.slice(0, 3).map((file) => file.filename).join(', ')}
                    {made.length > 3 && ` and ${made.length - 3} more`}
                  </span>
                </legend>
                <div role="radiogroup" aria-label="Its files" className="grid gap-2 sm:grid-cols-2">
                  {(
                    [
                      { value: 'keep', label: 'Keep them', detail: 'They stay in the project’s artifacts.' },
                      { value: 'delete', label: 'Delete them too', detail: 'They are gone for good, with the session.' },
                    ] as const
                  ).map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      role="radio"
                      aria-checked={artifacts === option.value}
                      onClick={() => setArtifacts(option.value)}
                      className={cn(
                        'rounded-(--radius-card) border p-3 text-left',
                        artifacts === option.value
                          ? 'border-accent bg-accent/5 ring-accent/30 ring-1'
                          : 'border-border hover:bg-surface-2',
                      )}
                    >
                      <span className="block text-sm font-medium">{option.label}</span>
                      <span className="text-muted block text-sm">{option.detail}</span>
                    </button>
                  ))}
                </div>
              </fieldset>
            )}
            <ConfirmLine
              question={
                made.length > 0 && artifacts === 'delete'
                  ? `Delete this session and its ${made.length === 1 ? 'file' : `${made.length} files`} for good?`
                  : 'Delete this session for good?'
              }
              confirm="Delete"
              pending={remove.isPending}
              onCancel={() => setAsking(false)}
              onConfirm={() =>
                remove.mutate(
                  { sessionId, artifacts: made.length > 0 ? artifacts : 'keep' },
                  {
                    // Nothing is left to look at, so the view goes with it.
                    onSuccess: () => void navigate({ to: '/' }),
                  },
                )
              }
            />
          </div>
        )}
        <ActionError error={remove.error} className="basis-full" />
      </li>
    </ul>
  );
}
