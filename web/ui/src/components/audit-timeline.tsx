import { Link } from '@tanstack/react-router';
import { ArrowRight, Server, ShieldCheck, ShieldX, SlidersHorizontal, Circle, type LucideIcon } from 'lucide-react';
import { useState, type ReactNode } from 'react';

import { useAudit, useBackends, useSessions } from '@/api/queries';
import type { AuditEntry, Session } from '@/api/types';
import { ActionError } from '@/components/ui/action-error';
import { EmptyState } from '@/components/ui/card';
import { clock, cn, humanise } from '@/lib/utils';

/**
 * Who changed what an agent may do, and who answered what it asked — read as a
 * log, newest first, a day at a time.
 *
 * It used to be a stack of cards each dumping the entry's raw detail: a whole
 * policy, every switch, for a change of one. An entry now says what happened
 * as a sentence, and a change of permissions says what changed, read against
 * the save before it.
 */
export function AuditTimeline({ projectId }: { projectId: string }) {
  const audit = useAudit(projectId);
  const sessions = useSessions(projectId, true);
  const backends = useBackends();
  const [kind, setKind] = useState<Kind | 'ALL'>('ALL');

  if (audit.isPending) return <p className="text-muted text-sm">Reading the audit…</p>;
  if (audit.error) {
    return <ActionError error={audit.error} outcome="The audit could not be read" recovery="Reload the page." />;
  }

  const entries = audit.data ?? [];
  if (entries.length === 0) {
    return (
      <EmptyState>
        Nothing recorded yet. Every approval, refusal and change of permissions in this project will
        be written here, with who did it and from where.
      </EmptyState>
    );
  }

  const context: Context = {
    session: (id) => sessions.data?.find((session) => session.id === id),
    backendName: (id) => backends.data?.find((backend) => backend.id === id)?.name ?? 'another backend',
    previous: previousPolicies(entries),
  };
  const counts = new Map<Kind, number>();
  for (const entry of entries) counts.set(kindOf(entry), (counts.get(kindOf(entry)) ?? 0) + 1);
  const shown = kind === 'ALL' ? entries : entries.filter((entry) => kindOf(entry) === kind);

  return (
    <div className="max-w-3xl space-y-6">
      <div role="radiogroup" aria-label="Show" className="flex flex-wrap gap-1.5">
        {(['ALL', ...KINDS.map((entry) => entry.kind)] as const).map((value) => {
          const count = value === 'ALL' ? entries.length : (counts.get(value) ?? 0);
          if (value !== 'ALL' && count === 0) return null;
          const chosen = kind === value;
          return (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={chosen}
              onClick={() => setKind(value)}
              className={cn(
                'flex min-h-11 items-center gap-1.5 rounded-full border px-3 text-sm sm:min-h-8',
                chosen ? 'border-accent bg-accent/10 text-text' : 'border-border text-muted hover:text-text hover:bg-surface-2',
              )}
            >
              {value === 'ALL' ? 'Everything' : KINDS.find((entry) => entry.kind === value)?.label}
              <span className="figures text-muted text-xs">{count}</span>
            </button>
          );
        })}
      </div>

      {byDay(shown).map(([day, list]) => (
        <section key={day} className="space-y-3">
          <h3 className="bg-canvas/90 text-muted sticky top-0 z-10 py-1 text-xs tracking-wide uppercase backdrop-blur">
            {day}
          </h3>
          <ol>
            {list.map((entry, index) => (
              <Entry key={entry.id} entry={entry} context={context} last={index === list.length - 1} />
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}

type Kind = 'APPROVAL' | 'PERMISSIONS' | 'BACKEND' | 'OTHER';

const KINDS: { kind: Kind; label: string }[] = [
  { kind: 'APPROVAL', label: 'Approvals' },
  { kind: 'PERMISSIONS', label: 'Permissions' },
  { kind: 'BACKEND', label: 'Backends' },
  { kind: 'OTHER', label: 'Other' },
];

function kindOf(entry: AuditEntry): Kind {
  if (entry.action === 'validation.resolved') return 'APPROVAL';
  if (entry.action === 'execution_policy.set') return 'PERMISSIONS';
  if (entry.action === 'session.backend_changed') return 'BACKEND';
  return 'OTHER';
}

interface Context {
  /** The Session an entry happened in, if it still exists. */
  session: (id: string) => Session | undefined;
  backendName: (id: string) => string;
  /** The policy each permissions entry replaced, when the log still holds it. */
  previous: Map<string, Policy>;
}

/** One entry: its time, its mark on the spine, and the sentence it stands for. */
function Entry({ entry, context, last }: { entry: AuditEntry; context: Context; last: boolean }) {
  const { Icon, tone, title, body } = describe(entry, context);
  return (
    <li className="grid grid-cols-[2.75rem_2rem_minmax(0,1fr)] gap-x-2 sm:grid-cols-[3.25rem_2rem_minmax(0,1fr)]">
      <time dateTime={entry.createdAt} className="text-muted figures pt-1.5 text-right font-mono text-[0.6875rem]">
        {clock(entry.createdAt)}
      </time>
      {/* The spine runs from one mark to the next, so a day reads as one line. */}
      <span
        className={cn(
          'relative flex justify-center',
          !last && "before:bg-border before:absolute before:top-8 before:bottom-0 before:w-px before:content-['']",
        )}
      >
        <span className="bg-surface border-border relative flex size-7 items-center justify-center rounded-full border">
          <Icon className={cn('size-3.5', tone)} />
        </span>
      </span>
      <div className="min-w-0 pt-1 pb-6">
        <p className="text-sm">{title}</p>
        {body && <div className="mt-1.5 space-y-1.5">{body}</div>}
        <p className="text-muted mt-1.5 text-xs">
          {entry.actorId}
          {entry.channel && <> · from {CHANNELS[entry.channel] ?? humanise(entry.channel)}</>}
          {entry.sessionId && entry.action !== 'execution_policy.set' && entry.action !== 'session.backend_changed' && (
            <>
              {' · in '}
              <SessionLink id={entry.sessionId} context={context} />
            </>
          )}
        </p>
      </div>
    </li>
  );
}

const CHANNELS: Record<string, string> = { web: 'the web', api: 'the API', mobile: 'the phone', cli: 'the command line' };

function SessionLink({ id, context }: { id: string; context: Context }) {
  const session = context.session(id);
  // The record outlives what it describes; a deleted Session is named, not linked.
  if (!session) return <span>a session since deleted</span>;
  return (
    <Link to="/sessions/$sessionId" params={{ sessionId: id }} className="hover:text-text underline underline-offset-2">
      {session.title || 'an untitled session'}
    </Link>
  );
}

const TONES = { ok: 'text-ok', danger: 'text-danger', accent: 'text-accent', muted: 'text-muted' };

function describe(entry: AuditEntry, context: Context): { Icon: LucideIcon; tone: string; title: ReactNode; body?: ReactNode } {
  const detail = (entry.detail ?? {}) as Record<string, unknown>;
  const text = (value: unknown) => (typeof value === 'string' ? value : '');

  if (entry.action === 'validation.resolved') {
    const approved = detail.approved === true;
    // "Bash: go test" — the tool in words, the command as a command.
    const [tool, ...rest] = text(detail.title).split(': ');
    const what = rest.join(': ');
    const note = typeof detail.note === 'string' && detail.note.trim();
    return {
      Icon: approved ? ShieldCheck : ShieldX,
      tone: approved ? TONES.ok : TONES.danger,
      title: (
        <>
          <span className={cn('font-medium', approved ? 'text-ok' : 'text-danger')}>{approved ? 'Allowed' : 'Refused'}</span>{' '}
          {tool.replace(/^mcp__threavia__/, '') || 'an action'}
        </>
      ),
      body: (
        <>
          {what && (
            <code className="bg-surface-2 border-border block overflow-x-auto rounded-md border px-2.5 py-1.5 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">
              {what}
            </code>
          )}
          {note && <p className="border-border text-muted border-l-2 pl-2.5 text-sm">{note}</p>}
        </>
      ),
    };
  }

  if (entry.action === 'execution_policy.set') {
    const after = detail as Policy;
    const before = context.previous.get(entry.id);
    const changes = before ? diff(before, after) : null;
    return {
      Icon: SlidersHorizontal,
      tone: TONES.accent,
      title: entry.sessionId ? (
        <>
          Changed the permissions of <SessionLink id={entry.sessionId} context={context} />
        </>
      ) : (
        "Changed the project's permissions"
      ),
      body: changes ? (
        changes.length === 0 ? (
          <p className="text-muted text-sm">Saved without a change.</p>
        ) : (
          <ul className="space-y-1">
            {changes.map((change, index) => (
              <li key={index} className="flex items-baseline gap-2 text-sm">
                <span className={cn('size-1.5 shrink-0 translate-y-[-1px] rounded-full', change.tone)} />
                <span className="min-w-0">{change.text}</span>
              </li>
            ))}
          </ul>
        )
      ) : (
        // The first save the log still holds: what it set, said in one line.
        <p className="text-muted text-sm">Set to {summary(after)}.</p>
      ),
    };
  }

  if (entry.action === 'session.backend_changed') {
    const missing = Array.isArray(detail.missingSkills) ? (detail.missingSkills as string[]) : [];
    return {
      Icon: Server,
      tone: TONES.muted,
      title: (
        <>
          Moved <SessionLink id={entry.sessionId ?? ''} context={context} /> to{' '}
          <span className="font-medium">{context.backendName(text(detail.to) || (entry.subjectId ?? ''))}</span>
        </>
      ),
      body: (
        <>
          {typeof detail.from === 'string' && detail.from && (
            <p className="text-muted flex items-center gap-1.5 text-sm">
              {context.backendName(detail.from)} <ArrowRight className="size-3.5" /> {context.backendName(text(detail.to))}
            </p>
          )}
          {missing.length > 0 && (
            <p className="text-warn-text text-sm">Skills it does not have: {missing.join(', ')}.</p>
          )}
        </>
      ),
    };
  }

  return { Icon: Circle, tone: TONES.muted, title: humanise(entry.action.replace(/[._]/g, ' ')) };
}

// ------------------------------------------------------------------- policies

interface Rule {
  effect: string;
  capability: string;
  match?: string;
}

interface Policy {
  mode?: string;
  allowFilesystemWrite?: boolean;
  allowGitCommit?: boolean;
  allowGitPush?: boolean;
  allowNetwork?: boolean;
  rules?: Rule[];
  maxDurationSeconds?: number;
  maxActions?: number;
  supervision?: string;
}

const SWITCHES: [keyof Policy, string][] = [
  ['allowFilesystemWrite', 'Writing files'],
  ['allowGitCommit', 'Committing'],
  ['allowGitPush', 'Pushing'],
  ['allowNetwork', 'The network'],
];

const EFFECTS: Record<string, string> = { DENY: 'Refuse', ASK: 'Ask', ALLOW: 'Allow' };
const MODES: Record<string, string> = {
  INTERACTIVE: 'Interactive',
  GUARDED: 'Guarded',
  SUPERVISED: 'Supervised',
  AUTONOMOUS: 'Autonomous',
};
const modeName = (mode = '') => MODES[mode] ?? humanise(mode);
const CAPABILITIES: Record<string, string> = {
  SHELL: 'a command',
  FILE_READ: 'reading a file',
  FILE_WRITE: 'writing a file',
  NETWORK: 'the network',
  GIT_COMMIT: 'committing',
  GIT_PUSH: 'pushing',
  TOOL: 'a named tool',
};

/**
 * For every permissions entry, the policy the same Project or Session had just
 * before. The log is newest first, so it is walked from the oldest end.
 */
function previousPolicies(entries: AuditEntry[]): Map<string, Policy> {
  const last = new Map<string, Policy>();
  const previous = new Map<string, Policy>();
  for (const entry of [...entries].reverse()) {
    if (entry.action !== 'execution_policy.set') continue;
    const subject = entry.sessionId || entry.projectId || entry.subjectId || '';
    const before = last.get(subject);
    if (before) previous.set(entry.id, before);
    last.set(subject, (entry.detail ?? {}) as Policy);
  }
  return previous;
}

function ruleText(rule: Rule): ReactNode {
  return (
    <>
      {EFFECTS[rule.effect] ?? humanise(rule.effect)} {CAPABILITIES[rule.capability] ?? humanise(rule.capability)}
      {rule.match && (
        <>
          {' '}
          <code className="font-mono text-xs">{rule.match}</code>
        </>
      )}
    </>
  );
}

const ruleKey = (rule: Rule) => `${rule.effect}|${rule.capability}|${rule.match ?? ''}`;

function minutes(seconds: number | undefined) {
  return seconds ? `${Math.round(seconds / 60)} min` : 'no limit';
}

/** What changed between two saves, each as a line a person would say. */
function diff(before: Policy, after: Policy): { text: ReactNode; tone: string }[] {
  const out: { text: ReactNode; tone: string }[] = [];
  if (before.mode !== after.mode) {
    out.push({
      text: (
        <>
          Mode: {modeName(before.mode)} <ArrowRight className="inline size-3.5" />{' '}
          <span className="font-medium">{modeName(after.mode)}</span>
        </>
      ),
      tone: 'bg-accent',
    });
  }
  for (const [key, label] of SWITCHES) {
    if (Boolean(before[key]) === Boolean(after[key])) continue;
    out.push(
      after[key]
        ? { text: `${label} allowed without asking`, tone: 'bg-ok' }
        : { text: `${label} no longer allowed without asking`, tone: 'bg-danger' },
    );
  }
  const had = new Map((before.rules ?? []).map((rule) => [ruleKey(rule), rule]));
  const has = new Map((after.rules ?? []).map((rule) => [ruleKey(rule), rule]));
  for (const [key, rule] of has) {
    if (!had.has(key)) out.push({ text: <>Rule added: {ruleText(rule)}</>, tone: 'bg-accent' });
  }
  for (const [key, rule] of had) {
    if (!has.has(key)) out.push({ text: <>Rule removed: {ruleText(rule)}</>, tone: 'bg-muted' });
  }
  if ((before.maxDurationSeconds ?? 0) !== (after.maxDurationSeconds ?? 0)) {
    out.push({ text: `Time limit: ${minutes(before.maxDurationSeconds)} → ${minutes(after.maxDurationSeconds)}`, tone: 'bg-accent' });
  }
  if ((before.maxActions ?? 0) !== (after.maxActions ?? 0)) {
    out.push({
      text: `Action limit: ${before.maxActions || 'no limit'} → ${after.maxActions || 'no limit'}`,
      tone: 'bg-accent',
    });
  }
  if ((before.supervision ?? '') !== (after.supervision ?? '')) {
    out.push({ text: 'What the reviewer is told was rewritten', tone: 'bg-accent' });
  }
  return out;
}

/** A whole policy in one line, for a save with nothing before it to compare. */
function summary(policy: Policy): string {
  const allowed = SWITCHES.filter(([key]) => policy[key]).map(([, label]) => label.toLowerCase());
  const rules = policy.rules?.length ?? 0;
  return [
    modeName(policy.mode),
    allowed.length ? `${allowed.join(', ')} allowed` : 'nothing allowed without asking',
    rules ? `${rules} ${rules === 1 ? 'rule' : 'rules'}` : '',
  ]
    .filter(Boolean)
    .join(' · ');
}

// ----------------------------------------------------------------------- days

/** Entries grouped by the day they happened, named the way a person would. */
function byDay(entries: AuditEntry[]): [string, AuditEntry[]][] {
  const groups = new Map<string, AuditEntry[]>();
  const today = new Date().toDateString();
  const yesterday = new Date(Date.now() - 86_400_000).toDateString();
  for (const entry of entries) {
    const date = new Date(entry.createdAt);
    const key = date.toDateString();
    const label =
      key === today
        ? 'Today'
        : key === yesterday
          ? 'Yesterday'
          : date.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' });
    groups.set(label, [...(groups.get(label) ?? []), entry]);
  }
  return [...groups];
}
