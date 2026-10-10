import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { ArrowLeft, Bell, Copy, KeyRound, Monitor, Moon, Server, Sun, Trash2, type LucideIcon } from 'lucide-react';
import { useCallback, useState } from 'react';

import { useBackends, useClaimBackend, useIssueBackendToken, useMe, useRevokeBackend } from '@/api/queries';
import type { BackendInstance } from '@/api/types';
import { NotificationsPane } from '@/components/notifications-pane';
import { ConfirmLine, RecordList, RecordRow } from '@/components/record-list';
import { ActionError } from '@/components/ui/action-error';
import { Avatar } from '@/components/ui/avatar';
import { Badge, type BadgeTone } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { EmptyState } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { MenuItem } from '@/components/ui/menu';
import { cn, humanise, when } from '@/lib/utils';
import { useWideLayout } from '@/use-layout';
import { useTheme, type Theme } from '@/use-theme';
import { useShortcut } from '@/use-shortcut';

export type AccountTab = 'appearance' | 'notifications' | 'backends';

const TABS: { tab: AccountTab; label: string; Icon: LucideIcon; detail: string }[] = [
  { tab: 'appearance', label: 'Appearance', Icon: Monitor, detail: 'How this client looks and lays itself out, on this device.' },
  {
    tab: 'notifications',
    label: 'Notifications',
    Icon: Bell,
    detail: 'An approval or a question waiting, or work that ended while you were not watching. Never every event.',
  },
  { tab: 'backends', label: 'Backends', Icon: Server, detail: 'The machines that run the work, and what can be done about them.' },
];

/**
 * Everything about the person and their machines, as a page of its own.
 *
 * It was a dialog behind the name at the foot of the sidebar, holding the
 * layout, the notifications and every backend one under the other. Each part
 * now has its tab, its URL and the whole width.
 */
export function SettingsView() {
  const { tab } = useParams({ from: '/settings/$tab' });
  const navigate = useNavigate();
  const me = useMe();
  const current = TABS.find((entry) => entry.tab === tab) ?? TABS[0];

  // Escape goes back to where the person was, as it closed the dialog this was.
  useShortcut('Escape', useCallback(() => window.history.length > 1 ? window.history.back() : void navigate({ to: '/' }), [navigate]));

  // Authenticated or not is not a detail to smooth over: in ModeNone nobody was
  // asked to prove anything, and the id is whatever Core was configured with.
  const anonymous = !me.data || me.data.authMode === 'none';
  // In mode none nobody signed in, but Core still names the one user it
  // attributes everything to; that name is what the person knows themselves by.
  const name = me.data?.name || me.data?.email || me.data?.userId || 'anonymous';

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="border-border/70 border-b px-3 pt-3 sm:px-5">
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="icon"
            className="size-11 sm:size-8"
            title="Back (Esc)"
            aria-label="Back"
            onClick={() => (window.history.length > 1 ? window.history.back() : void navigate({ to: '/' }))}
          >
            <ArrowLeft />
          </Button>
          <Avatar name={name} />
          <div className="min-w-0">
            <h2 className="truncate text-[0.9375rem] font-semibold">{name}</h2>
            <p className="text-muted truncate text-xs">
              {anonymous
                ? 'No authentication is configured: everything here is attributed to one user.'
                : `Signed in with ${me.data?.authMode} as ${me.data?.userId}`}
            </p>
          </div>
        </div>
        <nav aria-label="Settings" className="-mb-px mt-3 flex gap-1 overflow-x-auto">
          {TABS.map(({ tab: value, label, Icon }) => (
            <Link
              key={value}
              to="/settings/$tab"
              params={{ tab: value }}
              replace
              aria-current={value === current.tab ? 'page' : undefined}
              className={cn(
                'flex min-h-11 shrink-0 items-center gap-1.5 border-b-2 px-3 text-sm whitespace-nowrap sm:min-h-10',
                value === current.tab ? 'border-accent text-text font-medium' : 'text-muted hover:text-text border-transparent',
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
          {current.tab === 'appearance' && <AppearancePane />}
          {current.tab === 'notifications' && <NotificationsPane />}
          {current.tab === 'backends' && <BackendsPane />}
        </div>
      </div>
    </div>
  );
}


const THEMES: { value: Theme; label: string; Icon: LucideIcon }[] = [
  { value: 'system', label: 'System', Icon: Monitor },
  { value: 'light', label: 'Light', Icon: Sun },
  { value: 'dark', label: 'Dark', Icon: Moon },
];

function AppearancePane() {
  const [wide, setWide] = useWideLayout();
  const [theme, setTheme] = useTheme();
  return (
    <ul className="border-border divide-border divide-y rounded-(--radius-card) border">
      <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 p-4">
        <div className="min-w-0 flex-1 basis-64">
          <p className="font-medium">Theme</p>
          <p className="text-muted mt-1 text-sm">
            System follows this device and changes when it does. Light and Dark stay put, whatever the
            device says.
          </p>
        </div>
        <div role="radiogroup" aria-label="Theme" className="border-border bg-surface-2 flex rounded-md border p-0.5 text-sm">
          {THEMES.map(({ value, label, Icon }) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={theme === value}
              onClick={() => setTheme(value)}
              className={cn(
                'text-muted hover:text-text flex min-h-11 items-center gap-1.5 rounded px-3 transition-colors sm:min-h-8',
                theme === value && 'bg-surface text-text shadow-sm',
              )}
            >
              <Icon className="size-3.5" />
              {label}
            </button>
          ))}
        </div>
      </li>
      <li className="p-4">
        <CheckboxField checked={wide} onCheckedChange={setWide}>
          <span className="text-text font-medium">Use the full width of the window</span>
        </CheckboxField>
        <p className="text-muted mt-1 pl-6 text-sm">
          A conversation is held to a reading width by default, because a line running the whole of
          a wide screen loses the eye on the way back to the left margin. Turn this on and nothing is
          held back.
        </p>
      </li>
    </ul>
  );
}

function backendTone(backend: BackendInstance): BadgeTone {
  if (backend.ownershipStatus === 'REVOKED') return 'danger';
  if (backend.operationalStatus === 'READY') return 'ok';
  if (backend.operationalStatus === 'DEGRADED') return 'warn';
  return 'neutral';
}

/** The machines that can run work, as rows, and the two ways to add one. */
function BackendsPane() {
  const backends = useBackends();
  const list = backends.data ?? [];

  return (
    <div className="space-y-8">
      {backends.isPending ? (
        <p className="text-muted text-sm">Reading the backends…</p>
      ) : list.length === 0 ? (
        <EmptyState>None yet. Issue a registration token below and start a backend with it.</EmptyState>
      ) : (
        <RecordList>
          {list.map((backend) => (
            <BackendRow key={backend.id} backend={backend} />
          ))}
        </RecordList>
      )}
      <AddBackend />
    </div>
  );
}

function BackendRow({ backend }: { backend: BackendInstance }) {
  const revoke = useRevokeBackend();
  const [asking, setAsking] = useState(false);
  const revoked = backend.ownershipStatus === 'REVOKED';

  return (
    <RecordRow
      icon={Server}
      tone={revoked ? 'text-muted' : backend.operationalStatus === 'READY' ? 'text-ok' : 'text-warn-text'}
      title={backend.name}
      muted={revoked}
      badges={
        <>
          <Badge tone={backendTone(backend)}>{humanise(backend.operationalStatus)}</Badge>
          {backend.ownershipStatus !== 'CLAIMED' && <Badge tone="warn">{humanise(backend.ownershipStatus)}</Badge>}
        </>
      }
      meta={
        <>
          {(backend.capabilities ?? []).map(humanise).join(', ') || 'no capability'} ·{' '}
          {backend.capacity?.activeRuns ?? 0}/{backend.capacity?.maxConcurrentRuns ?? 0} runs · last seen{' '}
          {when(backend.lastHeartbeatAt) || 'never'}
          {(backend.conditions ?? []).map((condition) => (
            <span key={condition.type} className="text-danger block font-sans">
              {condition.message || condition.reason || condition.type}
            </span>
          ))}
        </>
      }
      menuLabel={`More for ${backend.name}`}
      menu={
        !revoked && (
          <MenuItem className="text-danger" onSelect={() => setAsking(true)}>
            <Trash2 className="size-4" />
            Revoke…
          </MenuItem>
        )
      }
      below={
        (asking || revoke.error) && (
          <>
            <ConfirmLine
              question="Revoke its credential? It stops taking work at once."
              confirm="Revoke"
              pending={revoke.isPending}
              onConfirm={() => revoke.mutate(backend.id, { onSuccess: () => setAsking(false) })}
              onCancel={() => {
                setAsking(false);
                revoke.reset();
              }}
            />
            <ActionError error={revoke.error} recovery="It still holds its credential; try again." className="mt-1 text-xs" />
          </>
        )
      }
    />
  );
}

/** A token for a backend to come, or a claim on one that is already running. */
function AddBackend() {
  const issue = useIssueBackendToken();
  const claim = useClaimBackend();
  const [claimCode, setClaimCode] = useState('');
  const [copied, setCopied] = useState(false);

  return (
    <section className="space-y-3">
      <h3 className="text-muted text-xs tracking-wide uppercase">Add a backend</h3>
      <ul className="border-border divide-border divide-y rounded-(--radius-card) border">
        <li className="space-y-3 p-4">
          <div className="flex flex-wrap items-center gap-3">
            <KeyRound className="text-muted size-4 shrink-0" />
            <div className="min-w-0 flex-1 basis-56">
              <p className="text-sm font-medium">Registration token</p>
              <p className="text-muted text-sm">Good for one registration, for an hour. Core shows it once.</p>
            </div>
            <Button
              size="lg"
              disabled={issue.isPending}
              onClick={() => {
                setCopied(false);
                issue.mutate({ label: 'issued from the web client', ttlSeconds: 3600 });
              }}
            >
              {issue.data ? 'Another token' : 'New token'}
            </Button>
          </div>
          {issue.data && (
            <div className="space-y-1 pl-7">
              <div className="flex items-start gap-2">
                <code className="bg-surface-2 border-border min-w-0 flex-1 rounded-md border p-2 font-mono text-xs break-all">
                  {issue.data.token}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-11 sm:size-8"
                  aria-label="Copy the token"
                  title="Copy"
                  onClick={() => {
                    void navigator.clipboard?.writeText(issue.data.token).then(() => setCopied(true)).catch(() => {});
                  }}
                >
                  <Copy />
                </Button>
              </div>
              <p className="text-muted text-xs">
                {copied ? 'Copied. ' : ''}Valid until {when(issue.data.expiresAt)}.
              </p>
            </div>
          )}
          <ActionError error={issue.error} outcome="No token" />
        </li>
        <li className="space-y-3 p-4">
          <div className="flex flex-wrap items-center gap-3">
            <Server className="text-muted size-4 shrink-0" />
            <div className="min-w-0 flex-1 basis-56">
              <p className="text-sm font-medium">Claim an instance</p>
              <p className="text-muted text-sm">
                A backend that registered without a user prints a one-time code. Claiming it makes it
                yours.
              </p>
            </div>
          </div>
          <form
            className="flex gap-2 pl-7"
            onSubmit={(event) => {
              event.preventDefault();
              if (claimCode.trim()) claim.mutate(claimCode.trim(), { onSuccess: () => setClaimCode('') });
            }}
          >
            <Input
              aria-label="Claim code"
              value={claimCode}
              onChange={(event) => setClaimCode(event.target.value)}
              placeholder="Claim code"
              className="h-11 font-mono sm:h-9"
            />
            <Button size="lg" type="submit" disabled={!claimCode.trim() || claim.isPending}>
              Claim
            </Button>
          </form>
          <ActionError error={claim.error} outcome="Not claimed" className="pl-7" />
        </li>
      </ul>
    </section>
  );
}
