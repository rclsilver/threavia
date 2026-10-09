import { describe, expect, it } from 'vitest';

import { AttentionLedger } from '../src/attention/ledger';

describe('the attention ledger', () => {
  it('announces each request once, however often what waits is re-read', () => {
    const ledger = new AttentionLedger();
    expect(ledger.take(['a', 'b'])).toEqual(['a', 'b']);
    expect(ledger.take(['a', 'b'])).toEqual([]);
    expect(ledger.take(['a', 'b', 'c'])).toEqual(['c']);
  });

  it('does not ring again after a restart for what was already shown', () => {
    const first = new AttentionLedger();
    first.take(['a']);
    const restarted = new AttentionLedger(first.known);
    expect(restarted.take(['a', 'b'])).toEqual(['b']);
  });

  it('forgets what was answered, so it does not grow for ever', () => {
    const ledger = new AttentionLedger(['old']);
    ledger.take(['a']);
    expect(ledger.known).toEqual(['a']);
  });
});
