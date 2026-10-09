import { Bot, Eye, Hand, Info, Plus, ShieldCheck, Trash2, type LucideIcon } from 'lucide-react';
import type { ReactNode } from 'react';

import type {
  ExecutionMode,
  ExecutionPolicy,
  PermissionCapability,
  PermissionEffect,
  PermissionRule,
} from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input, Textarea } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn } from '@/lib/utils';

/**
 * The four ways an agent can work, each said as what it means for the person.
 *
 * They used to be four sentences squeezed into a list box, which hid the one
 * choice that changes everything else on the page behind a click.
 */
const MODES: { value: ExecutionMode; label: string; detail: string; Icon: LucideIcon }[] = [
  { value: 'INTERACTIVE', label: 'Interactive', detail: 'Asks you before it acts.', Icon: Hand },
  {
    value: 'GUARDED',
    label: 'Guarded',
    detail: 'Acts on its own within what is allowed below, asks for the rest.',
    Icon: ShieldCheck,
  },
  {
    value: 'SUPERVISED',
    label: 'Supervised',
    detail: 'A reviewer answers in your place, from what you tell it.',
    Icon: Eye,
  },
  { value: 'AUTONOMOUS', label: 'Autonomous', detail: 'Acts until a limit is reached.', Icon: Bot },
];

const SWITCHES: { key: 'allowFilesystemWrite' | 'allowGitCommit' | 'allowGitPush' | 'allowNetwork'; label: string; detail: string }[] = [
  { key: 'allowFilesystemWrite', label: 'Write files', detail: 'Create and change files in the working directory.' },
  { key: 'allowGitCommit', label: 'Commit', detail: 'Record its changes with git commit.' },
  { key: 'allowGitPush', label: 'Push', detail: 'Send commits to a remote, where others see them.' },
  { key: 'allowNetwork', label: 'Network', detail: 'Reach other machines: downloads, APIs, package installs.' },
];

const EFFECTS: { value: PermissionEffect; label: string; tone: string }[] = [
  { value: 'DENY', label: 'Refuse', tone: 'text-danger' },
  { value: 'ASK', label: 'Ask', tone: 'text-warn-text' },
  { value: 'ALLOW', label: 'Allow', tone: 'text-ok' },
];

/**
 * What a rule can be about, with the shape of what goes in the box beside it.
 *
 * The vocabulary is Threavia's, not a provider's: the same rule has to mean the
 * same thing on every backend, and each one renders it in its own terms.
 */
const CAPABILITIES: { value: PermissionCapability; label: string; hint: string }[] = [
  { value: 'SHELL', label: 'a command', hint: 'kubectl delete *' },
  { value: 'FILE_READ', label: 'reading a file', hint: 'secrets/**' },
  { value: 'FILE_WRITE', label: 'writing a file', hint: 'infra/**' },
  { value: 'NETWORK', label: 'the network', hint: 'example.com' },
  { value: 'GIT_COMMIT', label: 'committing', hint: '' },
  { value: 'GIT_PUSH', label: 'pushing', hint: '' },
  { value: 'TOOL', label: 'a named tool', hint: 'mcp__server__tool' },
];

function hintOf(capability: PermissionCapability) {
  return CAPABILITIES.find((entry) => entry.value === capability)?.hint ?? '';
}

/** A part of the policy: a heading, what it governs, then its controls. */
export function PolicySection({ title, detail, children }: { title: string; detail?: ReactNode; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <div>
        <h3 className="text-muted text-xs tracking-wide uppercase">{title}</h3>
        {detail && <p className="text-muted mt-0.5 text-xs">{detail}</p>}
      </div>
      {children}
    </section>
  );
}

/**
 * The mode, the switches and the limits, shared by the Project page and the
 * Session panel.
 *
 * They are the same object at both levels on purpose: a Project default a
 * Session can loosen is only honest if the two are written in the same terms.
 */
export function PolicyForm({
  value,
  onChange,
  idPrefix,
  disabled,
}: {
  value: ExecutionPolicy;
  onChange: (next: ExecutionPolicy) => void;
  idPrefix: string;
  /** Showing what applies, rather than editing what this level sets. */
  disabled?: boolean;
}) {
  const set = (patch: Partial<ExecutionPolicy>) => onChange({ ...value, ...patch });

  return (
    <div className="space-y-6">
      <PolicySection title="How it works">
        <div role="radiogroup" aria-label="Mode" className="grid gap-2 sm:grid-cols-2">
          {MODES.map(({ value: mode, label, detail, Icon }) => {
            const chosen = value.mode === mode;
            return (
              <button
                key={mode}
                type="button"
                role="radio"
                aria-checked={chosen}
                disabled={disabled}
                onClick={() => set({ mode })}
                className={cn(
                  'flex items-start gap-3 rounded-(--radius-card) border p-3 text-left transition-colors disabled:cursor-default',
                  chosen
                    ? 'border-accent bg-accent/5 ring-accent/30 ring-1'
                    : 'border-border bg-surface hover:bg-surface-2 disabled:hover:bg-surface',
                  disabled && !chosen && 'opacity-60',
                )}
              >
                <Icon className={cn('mt-0.5 size-4 shrink-0', chosen ? 'text-accent' : 'text-muted')} />
                <span>
                  <span className="block text-sm font-medium">{label}</span>
                  <span className="text-muted block text-sm">{detail}</span>
                </span>
              </button>
            );
          })}
        </div>

        {/* Said under every mode, and for good, because the page otherwise
            reads as a boundary it is not: the gate judges a command by what it
            says, so `bash -c 'git push'` gets through. Where it stops is the
            backend's account, and that is worth knowing before choosing.
            Autonomous gets one more sentence, since there nothing asks first. */}
        <p className="text-muted flex items-start gap-1.5 pt-1 text-xs">
          <Info className="mt-px size-3.5 shrink-0" aria-hidden />
          <span>
            A guard rail, not a sandbox: it reads each command as written, so an agent set on getting
            around it can. The real limit is the account and the machine the backend runs as.
            {value.mode === 'AUTONOMOUS' && " In this mode, the agent acts with all of that account's rights, without asking."}
          </span>
        </p>

        {/* Only under a reviewer, because only there does anyone read it. The
            other modes answer from the rules below, and a box nobody reads is
            a box that invites writing a policy that does nothing. */}
        {value.mode === 'SUPERVISED' && (
          <div className="space-y-1.5 pt-2">
            <label htmlFor={`${idPrefix}-supervision`} className="text-sm font-medium">
              What the reviewer should know
            </label>
            <Textarea
              id={`${idPrefix}-supervision`}
              rows={4}
              value={value.supervision ?? ''}
              onChange={(event) => set({ supervision: event.target.value })}
              disabled={disabled}
              placeholder="This cluster is shared and runs in production. Never deploy or migrate without asking. Reading anything is fine."
            />
            <p className="text-muted text-xs">
              Said to the reviewer before every message, in your words. It is not the project
              instructions, which speak to the agent and which the reviewer never reads. A statement,
              not a guarantee: write a refusal below for what has to hold regardless.
            </p>
          </div>
        )}
      </PolicySection>

      <PolicySection title="On its own" detail="What it may do without asking. Switched off, it has to ask — or a rule below decides.">
        <ul className="border-border divide-border divide-y rounded-(--radius-card) border">
          {SWITCHES.map(({ key, label, detail }) => (
            <li key={key}>
              <label
                htmlFor={`${idPrefix}-${key}`}
                className={cn('flex min-h-14 items-center gap-3 px-3 py-2', !disabled && 'cursor-pointer')}
              >
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{label}</span>
                  <span className="text-muted block text-sm">{detail}</span>
                </span>
                <Switch
                  id={`${idPrefix}-${key}`}
                  checked={value[key]}
                  disabled={disabled}
                  onCheckedChange={(checked) => set({ [key]: checked })}
                />
              </label>
            </li>
          ))}
        </ul>
      </PolicySection>

      <PolicySection title="Limits" detail="A run that goes on too long or takes too many steps is stopped. Empty means no limit.">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span>Stop after</span>
          <Input
            id={`${idPrefix}-duration`}
            aria-label="Minutes before stopping"
            type="number"
            min={0}
            inputMode="numeric"
            className="figures w-24"
            placeholder="—"
            value={value.maxDurationSeconds ? Math.round(value.maxDurationSeconds / 60) : ''}
            onChange={(event) => set({ maxDurationSeconds: Math.max(0, Math.round(Number(event.target.value) * 60)) || 0 })}
            disabled={disabled}
          />
          <span>minutes, or</span>
          <Input
            id={`${idPrefix}-actions`}
            aria-label="Actions before stopping"
            type="number"
            min={0}
            inputMode="numeric"
            className="figures w-24"
            placeholder="—"
            value={value.maxActions || ''}
            onChange={(event) => set({ maxActions: Math.max(0, Number(event.target.value)) || 0 })}
            disabled={disabled}
          />
          <span>actions.</span>
        </div>
      </PolicySection>
    </div>
  );
}

/** On or off, said as a switch because it takes effect as a setting, not a choice in a form. */
function Switch({
  id,
  checked,
  disabled,
  onCheckedChange,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onCheckedChange(!checked)}
      // The hit area reaches a thumb's size; the track stays its own.
      className="relative flex h-11 w-12 shrink-0 items-center justify-center disabled:opacity-50"
    >
      <span
        className={cn(
          'flex h-5 w-9 items-center rounded-full p-0.5 transition-colors',
          checked ? 'bg-accent' : 'bg-border',
        )}
      >
        <span
          className={cn(
            'bg-surface size-4 rounded-full shadow-sm transition-transform',
            checked && 'translate-x-4',
          )}
        />
      </span>
    </button>
  );
}

/**
 * The exceptions to the switches.
 *
 * Rules read refuse, then ask, then allow, and the first match decides — which
 * is what lets a policy say "everything but this one command" instead of
 * choosing between all of the shell and none of it. Each is written as the
 * sentence it stands for: Refuse · a command · kubectl delete *.
 */
export function RulesEditor({
  rules,
  onChange,
  disabled,
  note,
}: {
  rules: PermissionRule[];
  onChange: (next: PermissionRule[]) => void;
  /** Showing what applies, rather than editing what this level sets. */
  disabled?: boolean;
  /** What these rules mean at this level, said under the heading. */
  note?: ReactNode;
}) {
  const replace = (index: number, patch: Partial<PermissionRule>) =>
    onChange(rules.map((rule, at) => (at === index ? { ...rule, ...patch } : rule)));

  return (
    <PolicySection
      title="Rules"
      detail={
        <>
          The exceptions, read in order: refuse, then ask, then allow; the first that matches decides.{' '}
          <code className="font-mono">*</code> stands for any text. {note}
        </>
      }
    >
      {rules.length === 0 ? (
        <p className="border-border text-muted rounded-(--radius-card) border border-dashed p-3 text-sm">
          No rule: the switches above decide everything.
        </p>
      ) : (
        <ul className="border-border divide-border divide-y rounded-(--radius-card) border">
          {rules.map((rule, index) => {
            const effect = EFFECTS.find((entry) => entry.value === rule.effect);
            return (
              <li key={index} className="flex flex-wrap items-center gap-2 px-2 py-2 sm:px-3">
                <Select
                  value={rule.effect}
                  disabled={disabled}
                  onValueChange={(next) => replace(index, { effect: next as PermissionEffect })}
                >
                  <SelectTrigger aria-label="Effect" className={cn('h-11 w-28 font-medium sm:h-9', effect?.tone)}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EFFECTS.map((entry) => (
                      <SelectItem key={entry.value} value={entry.value}>
                        <span className={entry.tone}>{entry.label}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <Select
                  value={rule.capability}
                  disabled={disabled}
                  onValueChange={(capability) => replace(index, { capability: capability as PermissionCapability })}
                >
                  <SelectTrigger aria-label="What it is about" className="h-11 w-40 sm:h-9">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {CAPABILITIES.map((capability) => (
                      <SelectItem key={capability.value} value={capability.value}>
                        {capability.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <Input
                  aria-label="Matching"
                  className="h-11 min-w-40 flex-1 font-mono text-xs max-sm:order-2 max-sm:basis-full sm:h-9"
                  placeholder={hintOf(rule.capability) || 'everything of that kind'}
                  value={rule.match ?? ''}
                  onChange={(event) => replace(index, { match: event.target.value })}
                  disabled={disabled}
                />
                <Input
                  aria-label="Why"
                  className="h-11 min-w-32 flex-1 max-sm:order-2 max-sm:basis-full sm:h-9"
                  placeholder="Why (optional)"
                  value={rule.note ?? ''}
                  onChange={(event) => replace(index, { note: event.target.value })}
                  disabled={disabled}
                />
                {!disabled && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-11 max-sm:ml-auto sm:size-8"
                    aria-label="Remove this rule"
                    title="Remove this rule"
                    onClick={() => onChange(rules.filter((_, at) => at !== index))}
                  >
                    <Trash2 />
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {!disabled && (
        <Button
          variant="ghost"
          size="sm"
          className="h-11 gap-1.5 sm:h-8"
          onClick={() => onChange([...rules, { effect: 'DENY', capability: 'SHELL', match: '' }])}
        >
          <Plus className="size-3.5" />
          Add a rule
        </Button>
      )}
    </PolicySection>
  );
}
