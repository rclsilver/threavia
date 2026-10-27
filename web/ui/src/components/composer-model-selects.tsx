import type { BackendInstance, Session } from '@/api/types';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/select';

/** Session preferences, saved immediately so new queued jobs use them. */
export function ComposerModelSelects({ session, backend, disabled, onChange }: {
  session: Session;
  backend?: BackendInstance;
  disabled: boolean;
  onChange: (settings: { model: string; effort: string }) => void;
}) {
  const catalog = backend?.modelCatalog;
  const models = catalog?.models ?? [];
  const model = session.model || '';
  const effort = session.effort || '';
  const selected = models.find((option) => option.id === (model || catalog?.defaultModel));
  const efforts = selected?.efforts ?? [];
  const defaultEffort = model ? selected?.defaultEffort : catalog?.defaultEffort || selected?.defaultEffort;
  const unsupported = !backend?.features?.includes('MODEL_SELECTION');
  return <div className="flex min-w-0 flex-wrap items-center gap-2" data-tour="session-model">
    <Select value={model || 'backend-default'} disabled={disabled || unsupported} onValueChange={(value) => onChange({ model: value === 'backend-default' ? '' : value, effort: '' })}>
      <SelectTrigger aria-label="Model" title={unsupported ? 'This backend does not support model selection' : 'Model for new jobs'} className="min-h-11 w-auto min-w-0 max-w-full text-xs sm:min-h-8 [&>span]:truncate"><SelectValue /></SelectTrigger>
      <SelectContent>
        <SelectItem value="backend-default">Backend default{catalog?.defaultModel ? ` · ${catalog.defaultModel}` : ''}</SelectItem>
        {model && !models.some((option) => option.id === model) && <SelectItem value={model} disabled>{model} (unavailable)</SelectItem>}
        {models.map((option) => <SelectItem key={option.id} value={option.id}>{option.name}</SelectItem>)}
      </SelectContent>
    </Select>
    <Select value={effort || 'backend-default'} disabled={disabled || unsupported || efforts.length === 0} onValueChange={(value) => onChange({ model, effort: value === 'backend-default' ? '' : value })}>
      <SelectTrigger aria-label="Reasoning effort" title={efforts.length ? 'Reasoning effort for new jobs' : 'This model does not offer configurable effort'} className="min-h-11 w-auto min-w-0 max-w-full text-xs sm:min-h-8 [&>span]:truncate"><SelectValue /></SelectTrigger>
      <SelectContent>
        <SelectItem value="backend-default">Default effort{defaultEffort ? ` · ${defaultEffort}` : ''}</SelectItem>
        {effort && !efforts.includes(effort) && <SelectItem value={effort} disabled>{effort} (unavailable)</SelectItem>}
        {efforts.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}
      </SelectContent>
    </Select>
  </div>;
}
