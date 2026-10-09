/**
 * Following the conversation as it arrives, which is what a chat does, the
 * way the web client's timeline does it.
 *
 * Only the reader stops it. A scroll event alone cannot say who scrolled: the
 * timeline scrolls itself to the bottom, then a row that grows — a burst of
 * output, a picture decoded — moves the bottom away again, and read as "the
 * reader scrolled up" that would stop the following a little short of the
 * end. So following stops only on what a person does — a wheel, a finger, a
 * key, the scrollbar — and resumes when they come back to the bottom.
 */
export class Follower {
  private stick = true;
  private lastIntent = 0;

  constructor(
    private readonly scroller: HTMLElement,
    content: HTMLElement,
    /** Told after every scroll, to update what depends on the position. */
    private readonly onMove: () => void,
  ) {
    const intent = (up: boolean) => {
      this.lastIntent = Date.now();
      if (up) this.stick = false;
    };
    scroller.addEventListener('wheel', (event) => intent(event.deltaY < 0), { passive: true });
    let touchY = 0;
    scroller.addEventListener('touchstart', (event) => (touchY = event.touches[0]?.clientY ?? 0), { passive: true });
    // A finger moving down the screen pulls the page up, to older lines.
    scroller.addEventListener(
      'touchmove',
      (event) => {
        const y = event.touches[0]?.clientY ?? touchY;
        intent(y > touchY);
        touchY = y;
      },
      { passive: true },
    );
    // The scrollbar is the element itself being pressed, not a row in it.
    scroller.addEventListener('pointerdown', (event) => {
      if (event.target === scroller) intent(true);
    });
    scroller.addEventListener('keydown', (event) => {
      if (['ArrowUp', 'PageUp', 'Home'].includes(event.key)) intent(true);
      else if (['ArrowDown', 'PageDown', 'End', ' '].includes(event.key)) intent(false);
    });
    scroller.addEventListener(
      'scroll',
      () => {
        if (this.distance() <= 8) this.stick = true;
        else if (Date.now() - this.lastIntent < 600) this.stick = false;
        this.onMove();
      },
      { passive: true },
    );
    // The window the timeline is read through changes size under it: a card
    // waiting for an answer opens above the composer, the composer grows with
    // a long message, a row unfolds. A reader at the bottom stays there.
    const observer = new ResizeObserver(() => this.settle());
    observer.observe(scroller);
    observer.observe(content);
  }

  private distance(): number {
    return this.scroller.scrollHeight - this.scroller.scrollTop - this.scroller.clientHeight;
  }

  /** Whether the way back down is worth offering: there is a way down, and the reader left. */
  get away(): boolean {
    return !this.stick && this.distance() > 8;
  }

  /** After the timeline changed: back to the bottom when following. */
  settle() {
    // With nothing to scroll there is nothing to have scrolled away from.
    if (this.scroller.scrollHeight <= this.scroller.clientHeight + 8) this.stick = true;
    if (this.stick && this.distance() > 1) this.scroller.scrollTop = this.scroller.scrollHeight;
    this.onMove();
  }

  toLatest() {
    this.stick = true;
    this.scroller.scrollTop = this.scroller.scrollHeight;
    this.onMove();
  }

  /** Opening something is reading it: following would pull it out of sight as it grows. */
  holdStill() {
    this.stick = false;
  }

  /**
   * Runs a change that adds rows above what is being read, keeping the line
   * the reader is on where it was: the older rows grow above, out of sight
   * until scrolled to.
   */
  keepPlace(change: () => void) {
    const before = this.scroller.scrollHeight;
    const top = this.scroller.scrollTop;
    change();
    if (!this.stick) this.scroller.scrollTop = top + (this.scroller.scrollHeight - before);
  }
}
