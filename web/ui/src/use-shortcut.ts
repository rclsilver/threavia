import { useEffect } from 'react';

/**
 * A single key that acts on the page — N makes a new one, E edits — unless the
 * key was meant for a field, or a menu or dialog has it.
 */
export function useShortcut(key: string, action: () => void) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== key || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement;
      if (target.closest('input, textarea, select, [contenteditable="true"], [role="menu"], [role="dialog"]')) return;
      event.preventDefault();
      action();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [key, action]);
}
