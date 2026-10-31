import * as DialogPrimitive from '@radix-ui/react-dialog';
import { Check, ChevronDown, LoaderCircle } from 'lucide-react';
import { useState } from 'react';

import type { BackendInstance, Session } from '@/api/types';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '@/components/ui/menu';
import { useMediaQuery } from '@/lib/use-media-query';
import { cn } from '@/lib/utils';
import { modelPill } from '../../../../clients/shared/composer';

type Settings = { model: string; effort: string };

/**
 * The model and effort for new jobs, as one pill in the composer's bar.
 *
 * It says what the next job will use — the backend's default included — and
 * opens the choice on demand: a menu on a desktop, a sheet from the bottom on
 * a phone, where every line is a thumb's width. `/model` and `/effort` change
 * the same thing from the keyboard.
 */
export function ComposerModelPill({ session, backend, disabled, saving, onChange }: {
  session: Session;
  backend?: BackendInstance;
  disabled: boolean;
  saving: boolean;
  onChange: (settings: Settings) => void;
}) {
  const phone = !useMediaQuery('(min-width: 640px)');
  const [sheet, setSheet] = useState(false);
  const catalog = backend?.modelCatalog;
  const models = catalog?.models ?? [];
  const model = session.model || '';
  const effort = session.effort || '';
  const unsupported = !backend?.features?.includes('MODEL_SELECTION');
  const { name, effort: shownEffort, unavailable, efforts } = modelPill(catalog, model, effort);

  const choices = {
    models: [
      { value: '', label: 'Backend default', detail: catalog?.defaultModel },
      ...models.map((option) => ({ value: option.id, label: option.name, detail: option.efforts.length ? undefined : 'no effort' })),
    ],
    efforts: efforts.length ? [{ value: '', label: 'default' }, ...efforts.map((value) => ({ value, label: value }))] : [],
  };
  // Another model starts from its own default effort.
  const pickModel = (value: string) => onChange({ model: value, effort: '' });
  const pickEffort = (value: string) => onChange({ model, effort: value });

  const trigger = (
    <button
      type="button"
      disabled={disabled || unsupported}
      data-tour="session-model"
      title={unsupported ? 'This backend does not support model selection' : 'Model and effort for the next job'}
      aria-label={`Model ${name}${shownEffort ? `, effort ${shownEffort}` : ''}`}
      className={cn(
        'border-border bg-surface-2 hover:text-text data-[state=open]:border-accent inline-flex min-h-[3.125rem] max-w-[14rem] min-w-0 items-center gap-1.5 rounded-md border px-3 text-xs whitespace-nowrap transition-colors disabled:opacity-60 sm:min-h-[2.125rem] sm:px-2.5',
        unavailable && 'text-warn-text',
      )}
    >
      <span className="truncate">{name}</span>
      {shownEffort && <span className="text-muted">· {shownEffort}</span>}
      {saving ? <LoaderCircle className="text-muted size-3 shrink-0 animate-spin" /> : <ChevronDown className="text-muted size-3 shrink-0" />}
    </button>
  );

  if (phone) {
    return (
      <DialogPrimitive.Root open={sheet} onOpenChange={setSheet}>
        <DialogPrimitive.Trigger asChild>{trigger}</DialogPrimitive.Trigger>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-black/50" />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            className="bg-surface border-border fixed inset-x-0 bottom-0 z-50 max-h-[80dvh] overflow-y-auto rounded-t-2xl border-t px-3 pt-2 pb-[max(1rem,env(safe-area-inset-bottom))] shadow-xl"
          >
            <div className="bg-border mx-auto mb-2 h-1 w-9 rounded-full" />
            <DialogPrimitive.Title className="sr-only">Model and effort</DialogPrimitive.Title>
            <p className="text-muted px-2 pt-1 pb-1 text-xs font-medium tracking-wide uppercase">Model</p>
            {choices.models.map((option) => (
              <button
                key={option.value || 'default'}
                type="button"
                onClick={() => { pickModel(option.value); setSheet(false); }}
                className={cn('flex min-h-11 w-full items-center justify-between rounded-md px-2 text-left text-sm', option.value === model && 'bg-surface-2')}
              >
                <span>{option.label}</span>
                <span className="text-muted flex items-center gap-2 text-xs">
                  {option.detail}
                  {option.value === model && <Check className="text-text size-4" />}
                </span>
              </button>
            ))}
            {choices.efforts.length > 0 && (
              <>
                <p className="text-muted px-2 pt-3 pb-1 text-xs font-medium tracking-wide uppercase">Effort</p>
                <div className="flex flex-wrap gap-2 px-1">
                  {choices.efforts.map((option) => (
                    <button
                      key={option.value || 'default'}
                      type="button"
                      aria-pressed={option.value === effort}
                      onClick={() => { pickEffort(option.value); setSheet(false); }}
                      className={cn('border-border min-h-11 rounded-md border px-4 text-sm', option.value === effort ? 'border-accent text-text' : 'text-muted')}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
              </>
            )}
            <p className="text-muted px-2 pt-3 text-xs">For the next job</p>
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    );
  }

  return (
    <Menu>
      <MenuTrigger asChild>{trigger}</MenuTrigger>
      <MenuContent side="top" align="start" className="w-64">
        <MenuLabel className="font-medium tracking-wide uppercase">Model</MenuLabel>
        {choices.models.map((option) => (
          <MenuItem key={option.value || 'default'} onSelect={() => pickModel(option.value)} className={cn('justify-between', option.value === model && 'bg-surface-2')}>
            <span>{option.label}</span>
            <span className="text-muted flex items-center gap-2 text-xs">
              {option.detail}
              {option.value === model && <Check className="text-text size-3.5" />}
            </span>
          </MenuItem>
        ))}
        {choices.efforts.length > 0 && (
          <>
            <MenuSeparator />
            <MenuLabel className="font-medium tracking-wide uppercase">Effort</MenuLabel>
            <div className="flex flex-wrap gap-1 px-1 pb-1">
              {choices.efforts.map((option) => (
                <MenuItem
                  key={option.value || 'default'}
                  onSelect={() => pickEffort(option.value)}
                  className={cn('border-border rounded-md border px-2.5 py-0.5 text-xs', option.value === effort ? 'border-accent text-text' : 'text-muted')}
                >
                  {option.label}
                </MenuItem>
              ))}
            </div>
          </>
        )}
        <MenuSeparator />
        <p className="text-muted px-2 py-1 text-xs">
          For the next job · also <code>/model</code>, <code>/effort</code>
        </p>
      </MenuContent>
    </Menu>
  );
}
