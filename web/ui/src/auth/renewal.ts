import { RefusedError, type Tokens } from './oidc';

/** Memory-only tokens, renewed by timer and by requests after a suspended tab wakes. */
export class SessionRenewal {
  private tokens: Tokens | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private pending: Promise<boolean> | undefined;
  private revision = 0;
  private stopped = false;

  private readonly exchange: (refreshToken: string) => Promise<Tokens>;
  private readonly changed: (tokens: Tokens | null) => void;
  private readonly expired: () => void;

  constructor(
    exchange: (refreshToken: string) => Promise<Tokens>,
    changed: (tokens: Tokens | null) => void,
    expired: () => void,
  ) {
    this.exchange = exchange;
    this.changed = changed;
    this.expired = expired;
  }

  hold(tokens: Tokens) {
    if (this.stopped) return;
    this.revision++;
    this.tokens = tokens;
    this.changed(tokens);
    clearTimeout(this.timer);
    if (tokens.refreshToken) {
      this.arm(Math.max(tokens.expiresAt - Date.now() - 60_000, 15_000));
    }
  }

  async prepare(): Promise<void> {
    if (this.tokens && this.tokens.expiresAt - Date.now() < 30_000) await this.recover();
  }

  /** One exchange shared by the timer, requests, reconnects and wake-up events. */
  recover(): Promise<boolean> {
    if (this.stopped || !this.tokens) return Promise.resolve(false);
    if (!this.tokens.refreshToken) {
      this.invalidate();
      return Promise.resolve(false);
    }
    this.pending ??= this.renew().finally(() => { this.pending = undefined; });
    return this.pending;
  }

  private async renew(): Promise<boolean> {
    const tokens = this.tokens!;
    const revision = this.revision;
    try {
      const next = await this.exchange(tokens.refreshToken!);
      if (this.stopped || this.revision !== revision) return false;
      this.hold(next);
      return true;
    } catch (error) {
      if (!this.stopped && this.revision === revision) {
        if (error instanceof RefusedError) this.invalidate();
        else this.arm(30_000);
      }
      throw error;
    }
  }

  private arm(delay: number) {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => { void this.recover().catch(() => {}); }, delay);
  }

  private invalidate() {
    this.revision++;
    this.tokens = undefined;
    clearTimeout(this.timer);
    this.changed(null);
    this.expired();
  }

  dispose() {
    this.stopped = true;
    this.revision++;
    this.tokens = undefined;
    clearTimeout(this.timer);
  }
}
