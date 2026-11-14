import { describe, expect, it } from 'vitest';
import { emptyLine, MORE, PHONE_TABS, SURFACES, surfaceById } from './surfaces';

describe('the Orders surface', () => {
  it('follows Ships in the nav, is scoped to the fleet and is titled Orders', () => {
    expect(SURFACES.map((s) => s.id)).toEqual(['inbox', 'ships', 'orders', 'chat', 'campaigns', 'routines', 'crew', 'preferences']);
    expect(surfaceById('orders')).toMatchObject({ title: 'Orders', scoped: true });
  });

  it('is a phone tab after Ships, and More does not list it', () => {
    expect(PHONE_TABS).toEqual(['inbox', 'chat', 'ships', 'orders']);
    expect(MORE).toEqual(['campaigns', 'routines', 'crew', 'preferences']);
  });

  it('asks for a fleet like any scoped surface', () => {
    expect(emptyLine(surfaceById('orders'), true, false)).toBe('Set up a fleet and its orders will appear here.');
  });
});

describe('emptyLine', () => {
  it('asks for a fleet on a scoped surface once the config has loaded with none', () => {
    expect(emptyLine(surfaceById('ships'), true, false)).toBe('Set up a fleet and its ships will appear here.');
  });

  it('says what the surface will show while loading, with a fleet, and on an unscoped surface', () => {
    expect(emptyLine(surfaceById('ships'), false, false)).toBe(surfaceById('ships').line);
    expect(emptyLine(surfaceById('ships'), true, true)).toBe(surfaceById('ships').line);
    expect(emptyLine(surfaceById('inbox'), true, false)).toBe(surfaceById('inbox').line);
  });
});
