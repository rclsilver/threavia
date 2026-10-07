/**
 * The Threavia mark: one thread that crosses itself and comes back.
 *
 * Twice as wide as it is tall, so it is served at twice the size it is shown at
 * and never squeezed into a square. The square icon files are the same mark on
 * a transparent canvas, for the slots that only take a square: a browser tab, a
 * launcher.
 *
 * The files come from brand/threavia-icon.png through brand/iconize.
 */
export function Logo({ className }: { className?: string }) {
  return <img src="/mark-64h.png" alt="" width={131} height={64} className={className} />;
}
