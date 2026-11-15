import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The hook's effects run at once, so a test drives its document listener without a DOM.
vi.mock('preact/hooks', () => ({
  useLayoutEffect: (fn: () => void | (() => void)) => void fn(),
  useEffect: () => {},
  useRef: () => ({ current: null }),
}));

import { useListKeys } from './Sheet';

type Handler = (e: unknown) => void;
let keydown: Handler | undefined;
let focused: unknown;

const item = () => ({
  tagName: 'A',
  isContentEditable: false,
  getAttribute: () => null,
  focus() {
    focused = this;
  },
});
const target = (tagName: string, editable = false) => ({
  tagName,
  isContentEditable: editable,
  getAttribute: (n: string) => (n === 'contenteditable' && editable ? 'true' : null),
  closest: (sel: string) => (sel.split(',').some((s) => s.trim().toLowerCase() === tagName.toLowerCase() || (s.includes('contenteditable') && editable)) ? target(tagName, editable) : null),
});
const press = (key: string, from: unknown) => {
  const e = { key, target: from, preventDefault: vi.fn() };
  keydown?.(e);
  return e;
};

beforeEach(() => {
  keydown = undefined;
  focused = undefined;
  vi.stubGlobal('document', {
    activeElement: null,
    addEventListener: (type: string, h: Handler) => {
      if (type === 'keydown') keydown = h;
    },
    removeEventListener: () => {},
  });
  const first = item();
  const second = item();
  useListKeys({ current: { querySelectorAll: () => [first, second] } as unknown as HTMLElement }, true, () => {});
});
afterEach(() => vi.unstubAllGlobals());

describe('useListKeys', () => {
  it('leaves an arrow key from a text field, a textarea, a select or an editable element alone', () => {
    for (const from of [target('INPUT'), target('TEXTAREA'), target('SELECT'), target('DIV', true)]) {
      expect(press('ArrowDown', from).preventDefault, `ArrowDown from ${from.tagName}`).not.toHaveBeenCalled();
      expect(press('ArrowUp', from).preventDefault, `ArrowUp from ${from.tagName}`).not.toHaveBeenCalled();
    }
  });

  it('still takes an arrow key from anywhere else and moves focus', () => {
    const e = press('ArrowDown', target('BUTTON'));
    expect(e.preventDefault).toHaveBeenCalled();
    expect(focused).toBeDefined();
  });
});
