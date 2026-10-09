import { useState } from 'react';

/**
 * What someone was writing, kept until they send it.
 *
 * Going to answer a question in another Session, or to look at a task, must
 * not cost the message half typed. It is kept per place — a Session, or the
 * first message of a new one in a Project — in this browser, and survives a
 * reload; sending it, or emptying the field, forgets it.
 */
const keyOf = (place: string) => `threavia.draft.${place}`;

function read(place: string) {
  try {
    return window.localStorage.getItem(keyOf(place)) ?? '';
  } catch {
    return '';
  }
}

export function useDraft(place: string): [string, (value: string) => void] {
  const [value, setValue] = useState(() => read(place));
  // Another place, another draft.
  const [seen, setSeen] = useState(place);
  if (seen !== place) {
    setSeen(place);
    setValue(read(place));
  }

  const keep = (next: string) => {
    setValue(next);
    // Written on every change: leaving a moment after the last keystroke
    // must not lose it.
    try {
      if (next.trim()) window.localStorage.setItem(keyOf(place), next);
      else window.localStorage.removeItem(keyOf(place));
    } catch {
      // Kept for as long as the page stays open instead.
    }
  };

  return [value, keep];
}
