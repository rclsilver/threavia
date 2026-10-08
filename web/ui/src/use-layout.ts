import { useSyncExternalStore } from 'react';

const WIDE_KEY = 'threavia.layout.wide';

function stored(): boolean {
  try {
    return window.localStorage.getItem(WIDE_KEY) === 'true';
  } catch {
    return false;
  }
}

// One value for the whole client: the sidebar applies it on every page and the
// settings page changes it, and two copies of it would undo each other.
let wide = stored();
const listeners = new Set<() => void>();

function apply() {
  document.documentElement.dataset.wide = String(wide);
}
apply();

function set(next: boolean) {
  wide = next;
  apply();
  try {
    window.localStorage.setItem(WIDE_KEY, String(next));
  } catch {
    // Nothing to do: the preference lasts this visit instead of every visit.
  }
  for (const listener of listeners) listener();
}

/**
 * Whether the views take the whole window or hold to a reading width.
 *
 * Line length and screen use pull against each other, and which one matters is
 * the reader's call, not ours: the default is the comfortable line, and anyone
 * with the room to spare can take it back. The choice is carried by one
 * attribute on the document, set as soon as this module loads, so the bounds
 * move and no component has to know.
 */
export function useWideLayout(): [boolean, (wide: boolean) => void] {
  const value = useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => wide,
  );
  return [value, set];
}
