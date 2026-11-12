import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { gateOf, markPaired, pairHeading, wasPaired } from './pairing';

const memory = () => {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) };
};

beforeEach(() => vi.stubGlobal('localStorage', memory()));
afterEach(() => vi.unstubAllGlobals());

describe('pairHeading', () => {
  it('says Pair this app until the device has paired here, then This device was signed out', () => {
    expect(wasPaired()).toBe(false);
    expect(pairHeading(wasPaired())).toBe('Pair this app');
    markPaired();
    expect(wasPaired()).toBe(true);
    expect(pairHeading(wasPaired())).toBe('This device was signed out');
  });
});

describe('gateOf', () => {
  it('shows the pairing screen only for a signed-out device, and Can\'t reach for an unreachable one', () => {
    expect(gateOf('signed-out')).toBe('pair');
    expect(gateOf('unreachable')).toBe('unreachable');
    for (const link of ['connecting', 'open', 'reconnecting'] as const) expect(gateOf(link), link).toBe('shell');
  });
});
