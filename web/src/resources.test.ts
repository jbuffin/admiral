import { describe, expect, it } from 'vitest';
import type { Source } from './sources';
import { type Resource, chipLabel, chipText, launchHint } from './resources';

const phone = (over: Partial<Resource> = {}): Resource => ({ name: 'phone', capacity: 1, in_use: 0, holders: [], check: 'ok', ...over });
const held: Resource = phone({ in_use: 1, holders: [{ kind: 'ship', session: 's1' }] });
const panelNeeding = (resources: string[]) =>
  [{ key: 'a', panels: [{ id: 'p', autopilot: { resources } }] }] as unknown as Source[];
const hint = (needs: string[], all: Resource[]) => launchHint(panelNeeding(needs), 'a', 'p', all);
const name = (s: string) => (s === 's1' ? 'brave-otter' : s);

describe('resource chip', () => {
  it('reads in units and names the holders', () => {
    expect(chipText(held)).toBe('phone 1/1');
    expect(chipLabel(held, name)).toBe('phone: 1 of 1 unit in use, held by brave-otter');
    expect(chipLabel(phone({ in_use: 1, holders: [{ kind: 'lock', label: 'flash' }] }), name)).toBe('phone: 1 of 1 unit in use, held by flash');
    expect(chipLabel(phone({ capacity: 2, in_use: 1, holders: [{ kind: 'ship', session: 's1' }] }), name)).toBe('phone: 1 of 2 units in use, held by brave-otter');
  });

  it('says free, checking or missing without a count', () => {
    expect(chipLabel(phone(), name)).toBe('phone: free');
    expect(chipLabel(phone({ check: 'pending' }), name)).toBe('phone: checking');
    expect(chipLabel(phone({ check: 'missing' }), name)).toBe('phone: missing (its check failed)');
  });

  it('clamps in_use to capacity, and lists no holder for a launch in flight', () => {
    const over = phone({ in_use: 2, holders: [{ kind: 'ship', session: 's1' }] });
    expect(chipText(over)).toBe('phone 1/1');
    expect(chipLabel(over, name)).toBe('phone: 1 of 1 unit in use, held by brave-otter');
    expect(chipLabel(phone({ in_use: 1 }), name)).toBe('phone: 1 of 1 unit in use');
  });
});

describe('launch orders hint', () => {
  it('names a resource in use or missing', () => {
    expect(hint(['phone'], [held])).toBe('phone is in use');
    expect(hint(['phone'], [phone({ check: 'missing' })])).toBe('phone is missing');
    expect(hint(['phone'], [phone({ in_use: 2, holders: [] })])).toBe('phone is in use');
  });

  it('says nothing when a unit is free, the check is pending, or the panel needs none', () => {
    expect(hint(['phone'], [phone({ capacity: 2, in_use: 1 })])).toBeNull();
    expect(hint(['phone'], [phone({ check: 'pending', in_use: 1 })])).toBeNull();
    expect(hint([], [held])).toBeNull();
    expect(hint(['tablet'], [held])).toBeNull();
    expect(launchHint(panelNeeding(['phone']), 'a', 'other', [held])).toBeNull(); // another panel of the source needs nothing
    expect(launchHint(null, 'a', 'p', [held])).toBeNull();
  });
});
