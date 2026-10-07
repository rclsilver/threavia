/**
 * The Threavia mark: one thread that crosses itself and comes back.
 *
 * It is drawn rather than loaded, so it inherits the page's own rendering and
 * costs no request. The gradient is the only place in the client where two
 * colours are used for their own sake; everything else that is coloured is
 * saying something.
 */
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden className={className}>
      <defs>
        <linearGradient id="threavia-thread" x1="4" y1="26" x2="28" y2="6" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="var(--color-accent)" />
          <stop offset="100%" stopColor="var(--color-accent-2)" />
        </linearGradient>
      </defs>
      <path
        d="M16 16C13.2 11.4 6.5 11.4 6.5 16s6.7 4.6 9.5 0c2.8-4.6 9.5-4.6 9.5 0s-6.7 4.6-9.5 0Z"
        stroke="url(#threavia-thread)"
        strokeWidth="2.6"
        strokeLinecap="round"
      />
      <circle cx="6.5" cy="16" r="2" fill="var(--color-accent)" />
      <circle cx="25.5" cy="16" r="2" fill="var(--color-accent-2)" />
    </svg>
  );
}
