/**
 * Which pending requests the person has already been told about.
 *
 * A notification is owed once per request: when it arrives, or when the editor
 * starts and finds it already waiting. Everything that makes the attention
 * route be re-read — another request, a reconnection, a refresh — must not
 * announce the same one again, and a request that was answered is forgotten so
 * the ledger does not grow for ever.
 *
 * The ledger survives a restart (the extension saves `known`), so reopening the
 * editor does not ring again for what was already shown.
 */
export class AttentionLedger {
  private readonly announced: Set<string>;

  constructor(known: Iterable<string> = []) {
    this.announced = new Set(known);
  }

  /**
   * Given every request pending now, returns the ones never announced, in the
   * order given, and remembers them. Ids no longer pending are dropped: Core
   * never reopens a resolved request, so they can never come back.
   */
  take(pending: readonly string[]): string[] {
    const now = new Set(pending);
    for (const id of this.announced) {
      if (!now.has(id)) this.announced.delete(id);
    }
    const fresh = pending.filter((id) => !this.announced.has(id));
    for (const id of fresh) this.announced.add(id);
    return fresh;
  }

  /** What to save, so a restart remembers what was already shown. */
  get known(): string[] {
    return [...this.announced];
  }
}

/** What the ledgers save: what was announced, by Core id. */
export type SavedLedgers = Record<string, string[]>;

/**
 * One ledger per Core. Request ids are only unique within a Core, and what
 * waits is read from each Core on its own, so a read of one must never make
 * another forget what it announced.
 */
export class AttentionLedgers {
  private readonly ledgers = new Map<string, AttentionLedger>();

  constructor(saved: SavedLedgers = {}) {
    for (const [coreId, known] of Object.entries(saved)) {
      if (Array.isArray(known)) this.ledgers.set(coreId, new AttentionLedger(known));
    }
  }

  /** The ledger's `take`, for one Core. */
  take(coreId: string, pending: readonly string[]): string[] {
    let ledger = this.ledgers.get(coreId);
    if (!ledger) {
      ledger = new AttentionLedger();
      this.ledgers.set(coreId, ledger);
    }
    return ledger.take(pending);
  }

  /** Forgets a Core that was removed, or moved to another address. */
  forget(coreId: string) {
    this.ledgers.delete(coreId);
  }

  /**
   * The ledger an earlier version saved, which knew one Core, handed to the
   * Core it became. Ignored when that Core already has one.
   */
  adopt(coreId: string, known: readonly string[]) {
    if (!this.ledgers.has(coreId)) this.ledgers.set(coreId, new AttentionLedger(known));
  }

  get saved(): SavedLedgers {
    return Object.fromEntries([...this.ledgers].map(([coreId, ledger]) => [coreId, ledger.known]));
  }
}
