import { describe, expect, it } from 'vitest';
import { moveTarget } from './remoteTailscale';

describe('moveTarget', () => {
  it('is the address the move message carries', () => {
    expect(moveTarget({ type: 'move', url: 'https://mac.tail.ts.net/' }, 'http://192.168.1.5:7420')).toBe('https://mac.tail.ts.net/');
  });

  it('is nothing when the message carries no address, so the page stays where it is', () => {
    expect(moveTarget({ type: 'move' }, 'http://192.168.1.5:7420')).toBeNull();
    expect(moveTarget({ type: 'move', url: '' }, 'http://192.168.1.5:7420')).toBeNull();
  });

  it('is nothing when the page is already there', () => {
    expect(moveTarget({ type: 'move', url: 'http://192.168.1.5:7420/' }, 'http://192.168.1.5:7420')).toBeNull();
  });

  it('refuses an address that is not http or https', () => {
    expect(moveTarget({ type: 'move', url: 'javascript:alert(1)' }, 'http://192.168.1.5:7420')).toBeNull();
  });
});
