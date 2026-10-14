import { describe, expect, it } from 'vitest';

import { sendsMessage } from '../../../web/ui/src/lib/message-keyboard';

const enter = { key: 'Enter', shiftKey: false, ctrlKey: false, metaKey: false, nativeEvent: { isComposing: false } };

describe('message Enter handling', () => {
  it('keeps Enter for multiline touch messages and retains the desktop shortcut', () => {
    expect(sendsMessage(enter, true)).toBe(false);
    expect(sendsMessage(enter, false)).toBe(true);
    expect(sendsMessage({ ...enter, shiftKey: true }, false)).toBe(false);
    expect(sendsMessage({ ...enter, ctrlKey: true }, true)).toBe(true);
  });
  it('never sends while an input method is composing, including Android keyCode 229', () => {
    expect(sendsMessage({ ...enter, nativeEvent: { isComposing: true } }, false)).toBe(false);
    expect(sendsMessage({ ...enter, nativeEvent: { isComposing: false, keyCode: 229 } }, false)).toBe(false);
  });
});
