import type { SessionView } from '../conversation/protocol';
import { h } from './dom';

/** Keep the native selects in place while session activity updates arrive. */
export function createModelSettings(onChange: (model: string, effort: string) => void) {
  const model = h('select', { 'aria-label': 'Model' });
  const effort = h('select', { 'aria-label': 'Reasoning effort' });
  const status = h('span', { class: 'muted', role: 'status' });
  const error = h('p', { class: 'error', role: 'alert', hidden: true });
  const controls = h('div', { class: 'model-controls' }, h('label', {}, 'Model', model), h('label', {}, 'Effort', effort), status);
  const element = h('div', { class: 'model-settings', hidden: true }, controls, error);
  let signature = '';
  model.addEventListener('change', () => onChange(model.value, ''));
  effort.addEventListener('change', () => onChange(model.value, effort.value));
  return { element, update(settings: SessionView['modelSettings'] | undefined, busy: boolean, running: boolean) {
    element.hidden = !settings;
    if (!settings) return;
    const catalog = settings.catalog;
    const models = catalog?.models ?? [];
    const selected = models.find((option) => option.id === (settings.model || catalog?.defaultModel));
    const efforts = selected?.efforts ?? [];
    const defaultEffort = settings.model ? selected?.defaultEffort : catalog?.defaultEffort || selected?.defaultEffort;
    const next = JSON.stringify([catalog, settings.model, settings.effort]);
    if (next !== signature) {
      model.replaceChildren(h('option', { value: '' }, `Backend default${catalog?.defaultModel ? ` · ${catalog.defaultModel}` : ''}`),
        ...(settings.model && !models.some((option) => option.id === settings.model) ? [h('option', { value: settings.model, disabled: true }, `${settings.model} (unavailable)`)] : []),
        ...models.map((option) => h('option', { value: option.id }, option.name)));
      effort.replaceChildren(h('option', { value: '' }, `Default effort${defaultEffort ? ` · ${defaultEffort}` : ''}`),
        ...(settings.effort && !efforts.includes(settings.effort) ? [h('option', { value: settings.effort, disabled: true }, `${settings.effort} (unavailable)`)] : []),
        ...efforts.map((value) => h('option', { value }, value)));
      signature = next;
    }
    // Restore the confirmed selection after a refused save, even when the
    // catalog and persisted values did not change.
    model.value = settings.model;
    effort.value = settings.effort;
    model.disabled = busy || !settings.supported;
    effort.disabled = busy || !settings.supported || efforts.length === 0;
    model.title = settings.supported ? 'Model for new jobs' : 'This backend does not support model selection';
    effort.title = efforts.length ? 'Reasoning effort for new jobs' : 'This model does not offer configurable effort';
    status.textContent = settings.saving ? 'Saving…' : running ? 'Applies to new jobs' : '';
    error.hidden = !settings.error;
    error.textContent = settings.error ?? '';
  } };
}
