import { api } from '@/api/client';

/**
 * Whether the person is looking at this client right now.
 *
 * Active means the page is visible, has the focus, and was used in the last
 * two minutes. A tab left open on a desk nobody sits at is not active, which
 * is the whole point: while the person is active somewhere, that screen shows
 * what happens and no device rings; once they are not, their phone does.
 */

const IDLE_AFTER = 2 * 60_000;

let lastInput = Date.now();
let reported: boolean | null = null;

export function isActive(): boolean {
  return (
    document.visibilityState === 'visible' &&
    document.hasFocus() &&
    Date.now() - lastInput < IDLE_AFTER
  );
}

function report() {
  const active = isActive();
  if (active === reported) return;
  reported = active;
  // Best effort: a missed report costs one notification too many or too
  // few, and the next change sends the truth again.
  void api.post('/api/v1/me/presence', { active }).catch(() => {
    reported = null;
  });
}

/**
 * Told once a new stream is open: Core learned the presence from the stream's
 * own header, so the next change is what has to be sent.
 */
export function presenceSentWithStream(active: boolean) {
  reported = active;
}

let started = false;

/** Starts following presence. Safe to call more than once. */
export function followPresence() {
  if (started) return;
  started = true;

  const touched = () => {
    const wasIdle = Date.now() - lastInput >= IDLE_AFTER;
    lastInput = Date.now();
    if (wasIdle) report();
  };
  for (const name of ['pointerdown', 'keydown', 'wheel', 'touchstart']) {
    window.addEventListener(name, touched, { passive: true });
  }
  document.addEventListener('visibilitychange', report);
  window.addEventListener('focus', report);
  window.addEventListener('blur', report);
  // Going idle is the absence of an event, so it has to be looked for.
  setInterval(report, 30_000);
}
