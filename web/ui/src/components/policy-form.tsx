import { Plus, Trash2 } from 'lucide-react';

import type {
  ExecutionMode,
  ExecutionPolicy,
  PermissionCapability,
  PermissionEffect,
  PermissionRule,
} from '@/api/types';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

const MODES: { value: ExecutionMode; label: string }[] = [
  { value: 'INTERACTIVE', label: 'interactive — ask before acting' },
  { value: 'GUARDED', label: 'guarded — act within the allowances below' },
  { value: 'SUPERVISED', label: 'supervised — a reviewer answers in your place' },
  { value: 'AUTONOMOUS', label: 'autonomous — act until a limit is reached' },
];

const EFFECTS: { value: PermissionEffect; label: string }[] = [
  { value: 'DENY', label: 'refuse' },
  { value: 'ASK', label: 'ask' },
  { value: 'ALLOW', label: 'allow' },
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

/**
 * The mode, the switches and the limits, shared by the Project panel and the
 * Session one.
 *
 * They are the same object at both levels on purpose: a Project default a
 * Session can loosen is only honest if the two are written in the same terms.
 */
export function PolicyForm({
  value,
  onChange,
  idPrefix,
}: {
  value: ExecutionPolicy;
  onChange: (next: ExecutionPolicy) => void;
  idPrefix: string;
}) {
  const set = (patch: Partial<ExecutionPolicy>) => onChange({ ...value, ...patch });

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <Label htmlFor={`${idPrefix}-mode`}>Mode</Label>
        <Select value={value.mode} onValueChange={(mode) => set({ mode: mode as ExecutionMode })}>
          <SelectTrigger id={`${idPrefix}-mode`} className="flex-1">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {MODES.map((mode) => (
              <SelectItem key={mode.value} value={mode.value}>
                {mode.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex flex-wrap gap-4">
        <CheckboxField
          checked={value.allowFilesystemWrite}
          onCheckedChange={(allowFilesystemWrite) => set({ allowFilesystemWrite })}
        >
          write files
        </CheckboxField>
        <CheckboxField
          checked={value.allowGitCommit}
          onCheckedChange={(allowGitCommit) => set({ allowGitCommit })}
        >
          git commit
        </CheckboxField>
        <CheckboxField
          checked={value.allowGitPush}
          onCheckedChange={(allowGitPush) => set({ allowGitPush })}
        >
          git push
        </CheckboxField>
        <CheckboxField
          checked={value.allowNetwork}
          onCheckedChange={(allowNetwork) => set({ allowNetwork })}
        >
          network
        </CheckboxField>
      </div>

      {/* Only under a reviewer, because only there does anyone read it. The
          other modes answer from the rules above, and a box nobody reads is a
          box that invites writing a policy that does nothing. */}
      {value.mode === 'SUPERVISED' && (
        <div className="space-y-1.5">
          <Label htmlFor={`${idPrefix}-supervision`}>What the reviewer should know</Label>
          <Textarea
            id={`${idPrefix}-supervision`}
            rows={4}
            value={value.supervision ?? ''}
            onChange={(event) => set({ supervision: event.target.value })}
            placeholder="This cluster is shared and runs in production. Never deploy or migrate without asking. Reading anything is fine."
          />
          <p className="text-muted text-xs">
            Said to the reviewer before every message, in your words. It is not the project
            instructions, which speak to the agent and which the reviewer never reads. A statement,
            not a guarantee: write a refusal above for what has to hold regardless.
          </p>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        <Label htmlFor={`${idPrefix}-duration`}>Stop after</Label>
        <Input
          id={`${idPrefix}-duration`}
          type="number"
          min={0}
          className="w-28"
          placeholder="seconds"
          value={value.maxDurationSeconds ?? ''}
          onChange={(event) => set({ maxDurationSeconds: Number(event.target.value) || 0 })}
        />
        <Label htmlFor={`${idPrefix}-actions`}>or after</Label>
        <Input
          id={`${idPrefix}-actions`}
          type="number"
          min={0}
          className="w-28"
          placeholder="actions"
          value={value.maxActions ?? ''}
          onChange={(event) => set({ maxActions: Number(event.target.value) || 0 })}
        />
      </div>
    </div>
  );
}

/**
 * The exceptions to the switches above.
 *
 * Rules read deny, then ask, then allow, and the first match decides — which is
 * what lets a policy say "everything but this one command" instead of choosing
 * between all of the shell and none of it.
 */
export function RulesEditor({
  rules,
  onChange,
}: {
  rules: PermissionRule[];
  onChange: (next: PermissionRule[]) => void;
}) {
  const replace = (index: number, patch: Partial<PermissionRule>) =>
    onChange(rules.map((rule, at) => (at === index ? { ...rule, ...patch } : rule)));

  return (
    <div className="space-y-2">
      {rules.map((rule, index) => (
        <div key={index} className="flex flex-wrap items-center gap-2">
          <Select
            value={rule.effect}
            onValueChange={(effect) => replace(index, { effect: effect as PermissionEffect })}
          >
            <SelectTrigger className="w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {EFFECTS.map((effect) => (
                <SelectItem key={effect.value} value={effect.value}>
                  {effect.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select
            value={rule.capability}
            onValueChange={(capability) =>
              replace(index, { capability: capability as PermissionCapability })
            }
          >
            <SelectTrigger className="w-44">
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
            className="min-w-40 flex-1 font-mono text-xs"
            placeholder={hintOf(rule.capability) || 'everything of that kind'}
            value={rule.match ?? ''}
            onChange={(event) => replace(index, { match: event.target.value })}
          />
          <Input
            className="min-w-32 flex-1"
            placeholder="why"
            value={rule.note ?? ''}
            onChange={(event) => replace(index, { note: event.target.value })}
          />
          <Button
            variant="ghost"
            size="icon"
            title="Remove this rule"
            onClick={() => onChange(rules.filter((_, at) => at !== index))}
          >
            <Trash2 />
          </Button>
        </div>
      ))}

      <Button
        variant="ghost"
        size="sm"
        className="gap-1.5"
        onClick={() => onChange([...rules, { effect: 'DENY', capability: 'SHELL', match: '' }])}
      >
        <Plus className="size-3.5" />
        Add a rule
      </Button>

      <p className="text-muted text-xs">
        Read in order: refuse, then ask, then allow. <code>*</code> stands for any text.
      </p>
    </div>
  );
}
