import { describe, expect, it } from 'vitest';
import { bridgeSearch, crewFromSearch, peekKey } from './peekLink';

describe('peekKey', () => {
  it('is the queue key of the stop, crew stop or item a path opens', () => {
    expect(peekKey({ session: 's-1' })).toBe('stop:s-1');
    expect(peekKey({ session: 's-1', crew: 'scout' })).toBe('stop:s-1/scout');
    expect(peekKey({ session: 's-1', item: 'q-1' })).toBe('item:s-1/q-1');
    expect(peekKey({ session: 's-1', merge: true })).toBe('merge:s-1');
    expect(peekKey({ session: 's-1', rereview: true })).toBe('rereview:s-1');
  });
});

describe('bridgeSearch', () => {
  it('names the ship’s own fleet, and the Terminal tab when asked', () => {
    expect(bridgeSearch('alpha')).toBe('?fleet=alpha');
    expect(bridgeSearch('a b', 'terminal')).toBe('?fleet=a%20b&tab=terminal');
    expect(bridgeSearch(undefined)).toBe('');
  });
});

describe('a link to a boat’s Terminal tab', () => {
  it('names the boat beside the tab, and ShipView reads it back', () => {
    const search = bridgeSearch('alpha', 'terminal', 'mate');
    expect(search).toBe('?fleet=alpha&tab=terminal&crew=mate');
    expect(crewFromSearch(search)).toBe('mate');
    expect(crewFromSearch('?tab=terminal')).toBeNull();
  });
});
