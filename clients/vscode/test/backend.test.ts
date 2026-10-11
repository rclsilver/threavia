import { expect, it } from 'vitest';
import { quotaLabel, quotaPercent, quotaReset, quotaText } from '../src/conversation/backend';

it('formats every quota and threshold without displaying provider JSON', () => {
  const text = quotaText({ availability: 'UNAVAILABLE', observedAt: '2026-10-10T12:00:00Z', message: 'Last report is stale', limits: [
    { id: 'five_hour', label: 'five hour', percentUsed: 0, thresholds: [{ label: 'Custom warning', value: 0.87, unit: 'utilization' }] },
    { id: 'weekly', label: 'Weekly model', status: 'restricted', details: { secretDebugField: true } },
  ], details: { overageStatus: 'allowed' } });
  expect(text).toContain('5h: 0% used');
  expect(text).toContain('Weekly model: Usage not reported');
  expect(text).toContain('Custom warning: 0.87 utilization');
  expect(text).toContain('Last report is stale');
  expect(text).not.toContain('overageStatus');
  expect(text).not.toContain('secretDebugField');
});

it('formats legacy and special quota periods identically for both clients', () => {
  expect(quotaLabel({ id: 'codex/primary', label: 'codex · 300 min', windowSeconds: 18000 })).toBe('5h');
  expect(quotaLabel({ id: 'codex/secondary', label: 'codex · 10080 min', windowSeconds: 604800 })).toBe('7d');
  expect(quotaLabel({ id: 'seven_day', label: 'seven day' })).toBe('7d');
  expect(quotaLabel({ id: 'fable', label: 'Fable', windowSeconds: 604800 })).toBe('Fable · 7d');
  expect(quotaLabel({ id: 'reserve/secondary', label: 'Luna Reserve', windowSeconds: 604800 })).toBe('Luna Reserve · 7d');
  expect(quotaPercent({ id: 'a', label: 'a', percentUsed: 0 })).toBe(0);
  expect(quotaPercent({ id: 'a', label: 'a' })).toBeUndefined();
  expect(quotaReset('2026-10-17T11:24:00Z')).toMatch(/\d{1,2}:24/);
});


it('renders future quota names and arbitrary periods without provider rules', () => {
  const future = { id: 'future/rolling', label: 'New provider quota · Terminal', windowSeconds: 259200, percentUsed: 0 };
  expect(quotaLabel(future)).toBe('New provider quota · Terminal · 3d');
  expect(quotaLabel({ id: 'unknown', label: 'Unannounced model', windowSeconds: 7200 })).toBe('Unannounced model · 2h');
  const text = quotaText({ availability: 'AVAILABLE', observedAt: '2026-10-11T12:00:00Z', limits: [future, { id: 'new', label: 'Another new quota' }] });
  expect(text).toContain('New provider quota · Terminal · 3d: 0% used');
  expect(text).toContain('Another new quota: Usage not reported');
});
