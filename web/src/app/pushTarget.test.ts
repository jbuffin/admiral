import { describe, expect, it, vi } from 'vitest';
import { clickUrl } from './swPush';
import { pushTargetOf } from './pushTarget';

// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

const through = (data: Record<string, unknown>) => pushTargetOf(new URL(clickUrl(data), 'https://admiral.test').search);

describe('the page reads back the target a tapped push opened it with', () => {
  it('gets every field the worker carried, byte for byte, hostile text included', () => {
    const data = { target: 'peek', fleet: 'zulu', session: 's1', item: 'a/b?c=d#e', crew: '../x y&z=1', merge: true };
    expect(through(data)).toEqual(data);
  });

  it('gets only the fields the payload had', () => {
    expect(through({ session: 's1' })).toEqual({ session: 's1' });
  });

  it('gets a routine target, which names no ship', () => {
    expect(through({ target: 'routine', fleet: 'zulu', routine: 'r1' })).toEqual({ target: 'routine', fleet: 'zulu', routine: 'r1' });
    expect(through({ target: 'routine', fleet: 'zulu', routine: '../x y&z=1' })).toMatchObject({ routine: '../x y&z=1' });
  });

  it('finds no target in an ordinary page address', () => {
    expect(pushTargetOf('')).toBeNull();
    expect(pushTargetOf('?fleet=zulu')).toBeNull();
  });
});
