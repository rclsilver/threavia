import { expect, it } from 'vitest';
import { backendIcon, quotaText } from '../src/conversation/backend';

it('formats every quota, a provider threshold and absent usage without inventing percentages', () => {
  const text = quotaText({ availability: 'UNAVAILABLE', observedAt: '2026-10-10T12:00:00Z', message: 'Last report is stale', limits: [
    { id: 'primary', label: 'Five hours', percentUsed: 0, thresholds: [{ label: 'Custom warning', value: 0.87, unit: 'utilization' }] },
    { id: 'weekly', label: 'Weekly model', status: 'restricted' },
  ], details: { overageStatus: 'allowed' } });
  expect(text).toContain('Five hours: 0% used');
  expect(text).toContain('Weekly model: Usage not reported');
  expect(text).toContain('Custom warning: 0.87 utilization');
  expect(text).toContain('overageStatus');
  expect(text).toContain('Last report is stale');
  expect(backendIcon('claude')).toBe('sparkle');
  expect(backendIcon('codex')).toBe('terminal');
  expect(backendIcon('future-provider')).toBe('server');
});
