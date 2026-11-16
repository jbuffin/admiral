import { describe, expect, it } from 'vitest';
import type { Ship } from './api';
import { shipLinkSet } from './shipLinkSet';

const ship = (over: Record<string, unknown>) =>
  ({ session: 'gaff-gull', name: 'Gaff Gull', fleet: 'harbor', via: 'admiralbot', launched: '2026-11-15T09:00:00Z', tail: 'line 1', ...over }) as unknown as Ship;

describe('shipLinkSet', () => {
  it('stays the same while a ship only prints more output, so the chat does not render its replies again', () => {
    expect(shipLinkSet([ship({ tail: 'line 2' })], 'harbor')).toBe(shipLinkSet([ship({ tail: 'line 1' })], 'harbor'));
  });

  it('changes when a ship AdmiralBot launched in the chat fleet joins or leaves, and ignores the rest', () => {
    const base = shipLinkSet([ship({})], 'harbor');
    expect(shipLinkSet([ship({}), ship({ session: 'saltpaw', name: 'Saltpaw' })], 'harbor')).not.toBe(base);
    expect(shipLinkSet([], 'harbor')).not.toBe(base);
    expect(shipLinkSet([ship({}), ship({ session: 'x', name: 'X', via: undefined }), ship({ session: 'y', name: 'Y', fleet: 'lagoon' })], 'harbor')).toBe(base);
  });
});
