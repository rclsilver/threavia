export { quotaLabel, quotaPercent, quotaReset } from '../../../../clients/shared/quotas';

export function quotaNumber(value: number) {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(value);
}
