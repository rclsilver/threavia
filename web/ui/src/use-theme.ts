import { useSyncExternalStore } from 'react';

export type Theme = 'system' | 'light' | 'dark';

const THEME_KEY = 'threavia.theme';
const dark = window.matchMedia('(prefers-color-scheme: dark)');

function stored(): Theme {
  try {
    const value = window.localStorage.getItem(THEME_KEY);
    return value === 'light' || value === 'dark' ? value : 'system';
  } catch {
    return 'system';
  }
}

// One value for the whole client, like the layout: the settings page changes
// it and the document carries it.
let theme = stored();
const listeners = new Set<() => void>();

/**
 * The scheme the page is drawn in, on one attribute of the document. The
 * palette is keyed on it rather than on the media query, so a choice made here
 * wins over the system's, and following the system is one choice among three.
 * index.html sets it before the first paint, from the same key, so a page in
 * the dark scheme never flashes white while this module loads.
 */
function apply() {
  const scheme = theme === 'system' ? (dark.matches ? 'dark' : 'light') : theme;
  document.documentElement.dataset.scheme = scheme;
}
apply();
// The system can change under a page left open: evening comes, or a laptop
// follows the sun. Only someone following it sees the change.
dark.addEventListener('change', apply);

function set(next: Theme) {
  theme = next;
  apply();
  try {
    if (next === 'system') window.localStorage.removeItem(THEME_KEY);
    else window.localStorage.setItem(THEME_KEY, next);
  } catch {
    // The choice lasts this visit instead of every visit.
  }
  for (const listener of listeners) listener();
}

/** Light, dark, or whatever the system says, on this device. */
export function useTheme(): [Theme, (theme: Theme) => void] {
  const value = useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => theme,
  );
  return [value, set];
}
