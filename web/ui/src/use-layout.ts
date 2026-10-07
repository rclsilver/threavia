import { useState } from 'react';

const WIDE_KEY = 'threavia.layout.wide';

function stored(): boolean {
  try {
    return window.localStorage.getItem(WIDE_KEY) === 'true';
  } catch {
    return false;
  }
}

/**
 * Whether the views take the whole window or hold to a reading width.
 *
 * Line length and screen use pull against each other, and which one matters is
 * the reader's call, not ours: the default is the comfortable line, and anyone
 * with the room to spare can take it back. The choice is carried by one
 * attribute on the document, so the bounds move and no component has to know.
 */
export function useWideLayout(): [boolean, (wide: boolean) => void] {
  const [wide, setWide] = useState(stored);

  // Applied during render rather than in an effect: the layout a reader sees
  // first should already be the one they chose, with no flash of the other.
  document.documentElement.dataset.wide = String(wide);

  const remember = (next: boolean) => {
    setWide(next);
    try {
      window.localStorage.setItem(WIDE_KEY, String(next));
    } catch {
      // Nothing to do: the preference lasts this visit instead of every visit.
    }
  };

  return [wide, remember];
}
