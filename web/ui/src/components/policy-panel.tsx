import { useEffect, useState } from 'react';

import { usePolicy, useSetPolicy } from '@/api/queries';
import type { ExecutionMode, ExecutionPolicy } from '@/api/types';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { Input, Label } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

const MODES: { value: ExecutionMode; label: string }[] = [
  { value: 'INTERACTIVE', label: 'interactive — ask before acting' },
  { value: 'GUARDED', label: 'guarded — act within the allowances below' },
  { value: 'AUTONOMOUS', label: 'autonomous — act until a limit is reached' },
];

/**
 * What the agent may do in this Session.
 *
 * Enforced by Core and by the backend, never by the prompt alone (spec section
 * 17), and a guard rail rather than a sandbox: filesystem and process access
 * stay the real permissions of the backend account.
 */
export function PolicyPanel({ sessionId }: { sessionId: string }) {
  const { data } = usePolicy(sessionId);
  const save = useSetPolicy(sessionId);
  const [draft, setDraft] = useState<ExecutionPolicy | null>(null);

  useEffect(() => {
    if (data) setDraft(data);
  }, [data]);

  if (!draft) return null;
  const set = (patch: Partial<ExecutionPolicy>) => setDraft({ ...draft, ...patch });

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <Label htmlFor="policy-mode">Mode</Label>
        <Select value={draft.mode} onValueChange={(mode) => set({ mode: mode as ExecutionMode })}>
          <SelectTrigger id="policy-mode" className="flex-1">
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
          checked={draft.allowFilesystemWrite}
          onCheckedChange={(allowFilesystemWrite) => set({ allowFilesystemWrite })}
        >
          write files
        </CheckboxField>
        <CheckboxField
          checked={draft.allowGitCommit}
          onCheckedChange={(allowGitCommit) => set({ allowGitCommit })}
        >
          git commit
        </CheckboxField>
        <CheckboxField
          checked={draft.allowGitPush}
          onCheckedChange={(allowGitPush) => set({ allowGitPush })}
        >
          git push
        </CheckboxField>
        <CheckboxField
          checked={draft.allowNetwork}
          onCheckedChange={(allowNetwork) => set({ allowNetwork })}
        >
          network
        </CheckboxField>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Label htmlFor="policy-duration">Stop after</Label>
        <Input
          id="policy-duration"
          type="number"
          min={0}
          className="w-28"
          placeholder="seconds"
          value={draft.maxDurationSeconds ?? ''}
          onChange={(event) => set({ maxDurationSeconds: Number(event.target.value) || 0 })}
        />
        <Label htmlFor="policy-actions">or after</Label>
        <Input
          id="policy-actions"
          type="number"
          min={0}
          className="w-28"
          placeholder="actions"
          value={draft.maxActions ?? ''}
          onChange={(event) => set({ maxActions: Number(event.target.value) || 0 })}
        />
      </div>

      <p className="text-muted text-xs">
        A guard rail, not a sandbox: filesystem and process access stay the real permissions of the
        backend account.
      </p>

      {save.error && <p className="text-danger text-sm">{(save.error).message}</p>}

      <Button variant="primary" size="sm" disabled={save.isPending} onClick={() => save.mutate(draft)}>
        Apply
      </Button>
    </div>
  );
}
