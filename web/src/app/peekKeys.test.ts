import { describe, expect, it } from 'vitest';
import { keyAction, KEY_HINT } from './peekKeys';

const press = (key: string, extra: Record<string, unknown> = {}) => keyAction({ key, ...extra });

describe('keyAction', () => {
  it('maps J and K (either case) to moving the selection down and up', () => {
    expect(press('j')).toEqual({ kind: 'move', by: 1 });
    expect(press('J')).toEqual({ kind: 'move', by: 1 });
    expect(press('k')).toEqual({ kind: 'move', by: -1 });
  });

  it('maps Enter to the bridge, E to remove, and 1 to 3 to picking that choice', () => {
    expect(press('Enter')).toEqual({ kind: 'bridge' });
    expect(press('e')).toEqual({ kind: 'remove' });
    expect(press('E')).toEqual({ kind: 'remove' });
    expect(press('1')).toEqual({ kind: 'pick', n: 1 });
    expect(press('3')).toEqual({ kind: 'pick', n: 3 });
  });

  it('ignores every other key', () => {
    for (const key of ['4', '0', 'a', 'Escape', ' ', 'ArrowDown']) expect(press(key)).toBeNull();
  });

  it('ignores a key held with Ctrl, Meta or Alt, so the browser keeps its shortcuts', () => {
    for (const mod of ['ctrlKey', 'metaKey', 'altKey']) expect(press('e', { [mod]: true })).toBeNull();
  });

  it('ignores a key typed in a text field, a select or a contenteditable', () => {
    for (const tagName of ['INPUT', 'TEXTAREA', 'SELECT']) expect(keyAction({ key: 'j', target: { tagName } })).toBeNull();
    expect(keyAction({ key: 'e', target: { tagName: 'DIV', isContentEditable: true } })).toBeNull();
    expect(keyAction({ key: 'j', target: { tagName: 'DIV' } })).toEqual({ kind: 'move', by: 1 });
  });
});

describe('KEY_HINT', () => {
  it('is the canvas footer', () => {
    expect(KEY_HINT).toBe('J K move · 1–3 answer · ↵ bridge · E remove');
  });
});
