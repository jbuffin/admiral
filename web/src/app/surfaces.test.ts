import { describe, expect, it } from 'vitest';
import { emptyLine, surfaceById } from './surfaces';

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
