import { describe, expect, it } from 'vitest';
import { parseBuildStamp, reloadDecision } from './buildCheck';

const built = { version: '1.4.0', commit: 'abc1234' };

describe('parseBuildStamp', () => {
  it('reads the version and commit the server stamped', () => {
    expect(parseBuildStamp('1.4.0 abc1234')).toEqual(built);
  });
  it('finds no stamp where the server added none', () => {
    expect(parseBuildStamp(null)).toBeNull();
    expect(parseBuildStamp('')).toBeNull();
  });
});

describe('reloadDecision', () => {
  it('does nothing when the shell has no stamp, as under the dev server', () => {
    expect(reloadDecision(null, built, null)).toEqual({ reload: false, guard: null });
  });

  it('does nothing when the server runs the build the shell came from', () => {
    expect(reloadDecision(built, { ...built }, null)).toEqual({ reload: false, guard: null });
  });

  it('reloads once when the server version differs, and remembers which build it reloaded for', () => {
    const server = { version: '1.5.0', commit: 'def5678' };
    const first = reloadDecision(built, server, null);
    expect(first.reload).toBe(true);
    expect(first.guard).not.toBeNull();

    // The reloaded shell still differs (a stale cache): no second reload for the same server build.
    expect(reloadDecision(built, server, first.guard)).toEqual({ reload: false, guard: first.guard });
  });

  it('reloads when only the commit differs', () => {
    expect(reloadDecision(built, { version: '1.4.0', commit: 'zzz9999' }, null).reload).toBe(true);
  });

  it('reloads again for a later, different server build', () => {
    const first = reloadDecision(built, { version: '1.5.0', commit: 'def5678' }, null);
    expect(reloadDecision(built, { version: '1.6.0', commit: 'fed8765' }, first.guard).reload).toBe(true);
  });

  it('forgets the guard once the shell matches the server, so a later mismatch reloads', () => {
    const first = reloadDecision(built, { version: '1.5.0', commit: 'def5678' }, null);
    const matched = reloadDecision(built, built, first.guard);
    expect(matched).toEqual({ reload: false, guard: null });
    expect(reloadDecision(built, { version: '1.5.0', commit: 'def5678' }, matched.guard).reload).toBe(true);
  });
});
