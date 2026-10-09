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
