import { useSyncExternalStore } from 'react';

/** Touch keyboards need their Enter key for newlines; sending has its button. */
export function useTouchKeyboard(): boolean {
  return useSyncExternalStore(
    (listener) => {
      const query = window.matchMedia('(pointer: coarse)');
      query.addEventListener('change', listener);
      return () => query.removeEventListener('change', listener);
    },
    () => window.matchMedia('(pointer: coarse)').matches,
    () => false,
  );
}

export function sendsMessage(event: { key: string; shiftKey: boolean; ctrlKey: boolean; metaKey: boolean; nativeEvent: { isComposing: boolean; keyCode?: number } }, touch: boolean): boolean {
  return event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && event.nativeEvent.keyCode !== 229 && (!touch || event.ctrlKey || event.metaKey);
}
