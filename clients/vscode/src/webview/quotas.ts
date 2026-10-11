import type { BackendQuotas } from '../api/types';
import { quotaLabel, quotaPercent, quotaReset, quotaSummary } from '../conversation/backend';
import { humanise } from '../conversation/format';
import { h } from './dom';

export function quotaPanel(snapshot?: BackendQuotas): HTMLElement {
  if (!snapshot) return h('p', {}, 'No quota report received yet.');
  return h('div', {},
    h('p', { class: 'quota-observed' }, `Quotas · ${humanise(snapshot.availability)} · observed ${new Date(snapshot.observedAt).toLocaleString()}`),
    snapshot.message && h('p', {}, snapshot.message),
    snapshot.limits.map((limit) => {
      const percentage = quotaPercent(limit);
      const label = quotaLabel(limit);
      return h('div', { class: 'quota-window' },
        h('div', { class: 'quota-heading' }, h('strong', {}, label), h('span', {}, quotaSummary(limit))),
        percentage !== undefined && h('progress', { class: `quota-progress${percentage >= 100 ? ' exhausted' : percentage >= 80 ? ' warning' : ''}`, max: 100, value: Math.max(0, Math.min(100, percentage)), 'aria-label': `${label} usage`, 'aria-valuetext': `${percentage}% used` }),
        limit.resetsAt && h('p', { class: 'quota-reset' }, 'Resets ', h('time', { datetime: limit.resetsAt, title: new Date(limit.resetsAt).toLocaleString() }, quotaReset(limit.resetsAt))),
        ...(['used', 'limit', 'remaining'] as const).flatMap((field) => limit[field] === undefined ? [] : [h('p', {}, `${humanise(field)}: ${limit[field]} ${limit.unit ?? ''}`)]),
        ...(limit.thresholds ?? []).map((threshold) => h('p', {}, `${threshold.label}: ${threshold.value ?? 'not reported'} ${threshold.unit ?? ''}${threshold.status ? ` · ${humanise(threshold.status)}` : ''}`)),
      );
    }),
  );
}
