import { modelPill } from '../../../shared/composer';
import type { SessionView } from '../conversation/protocol';
import { h, icon } from './dom';

/**
 * The model and effort of the next job, as one pill in the composer's bar.
 * A click asks the extension for the editor's own Quick Pick, where both are
 * chosen; `/model` and `/effort` change the same thing from the field.
 */
export function createModelSettings(onPick: () => void) {
  const label = h('span', { class: 'pill-label' });
  const effort = h('span', { class: 'muted' });
  const marker = h('span', {});
  const pill = h('button', { type: 'button', class: 'model-pill', 'data-model-pill': true }, label, effort, marker);
  pill.addEventListener('click', () => onPick());
  const error = h('p', { class: 'error', role: 'alert', hidden: true });
  const element = h('span', { class: 'model-settings', hidden: true }, pill);
  return { element, error, update(settings: SessionView['modelSettings'] | undefined, busy: boolean) {
    element.hidden = !settings;
    error.hidden = !settings?.error;
    error.textContent = settings?.error ?? '';
    if (!settings) return;
    const shown = modelPill(settings.catalog, settings.model, settings.effort);
    label.textContent = shown.name;
    effort.textContent = shown.effort ? `· ${shown.effort}` : '';
    pill.classList.toggle('tone-warn', shown.unavailable);
    marker.replaceChildren(icon(settings.saving ? 'loading' : 'chevron-down', settings.saving ? 'codicon-modifier-spin' : ''));
    pill.disabled = busy || !settings.supported;
    pill.title = settings.supported ? 'Model and effort for the next job · also /model, /effort' : 'This backend does not support model selection';
    pill.setAttribute('aria-label', `Model ${shown.name}${shown.effort ? `, effort ${shown.effort}` : ''}`);
  } };
}
