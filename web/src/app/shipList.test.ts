import { describe, expect, it } from 'vitest';
import type { Ship, ShipStatus } from './api';
import { groupShips, runningFor, shipAge, shipRowAge } from './shipList';

const ship = (name: string, status: ShipStatus, fleet = 'zulu'): Ship => ({
  session: `s-${name}`,
  name,
  fleet,
  harness: 'claude',
  status,
  order_id: '',
  title: '',
  tail: '',
  fleet_color: '',
  launched: '',
  crew_live: 0,
  dir: '',
  repo: '',
  exit_code: null,
  exit_signal: null,
  crew: [],
});

const names = (groups: { ships: Ship[] }[]) => groups.map((g) => g.ships.map((s) => s.name));

describe('groupShips', () => {
  it('groups the fleet by status: Needs you, Underway, Anchored', () => {
    const groups = groupShips(
      [ship('a', 'anchored'), ship('b', 'underway'), ship('c', 'needs_you'), ship('d', 'underway')],
      'zulu',
    );
    expect(groups.map((g) => g.status)).toEqual(['needs_you', 'underway', 'anchored']);
    expect(names(groups).map((n) => n.sort())).toEqual([['c'], ['b', 'd'], ['a']]);
  });

  it('leaves out an empty group', () => {
    const groups = groupShips([ship('a', 'anchored'), ship('b', 'needs_you')], 'zulu');
    expect(groups.map((g) => g.status)).toEqual(['needs_you', 'anchored']);
  });

  it('lists only the active fleet', () => {
    const list = [ship('mine', 'underway'), ship('theirs', 'underway', 'alpha')];
    expect(names(groupShips(list, 'zulu'))).toEqual([['mine']]);
    expect(names(groupShips(list, 'alpha'))).toEqual([['theirs']]);
  });

  it('hides a ship sailing off', () => {
    expect(groupShips([ship('gone', 'sailing_off')], 'zulu')).toEqual([]);
    expect(names(groupShips([ship('gone', 'sailing_off'), ship('here', 'underway')], 'zulu'))).toEqual([['here']]);
  });
});

describe('shipAge', () => {
  const now = Date.parse('2026-11-11T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it('counts minutes, then hours, then days', () => {
    expect(shipAge(ago(10_000), now)).toBe('now');
    expect(shipAge(ago(24 * 60_000), now)).toBe('24m');
    expect(shipAge(ago(59 * 60_000 + 59_000), now)).toBe('59m');
    expect(shipAge(ago(2 * 3600_000 + 5 * 60_000), now)).toBe('2h');
    expect(shipAge(ago(3 * 86400_000), now)).toBe('3d');
  });

  it('is blank for a missing or unreadable launch time, and never negative', () => {
    expect(shipAge('', now)).toBe('');
    expect(shipAge('soon', now)).toBe('');
    expect(shipAge(ago(-5 * 60_000), now)).toBe('now');
  });
});

describe('how long a running ship has been going', () => {
  const now = Date.parse('2026-11-11T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it('counts minutes until the hour, then hours and minutes', () => {
    expect(runningFor(ago(20_000), now)).toBe('<1m');
    expect(runningFor(ago(59 * 60_000 + 59_000), now)).toBe('59m');
    expect(runningFor(ago(3600_000), now)).toBe('1h 0m');
    expect(runningFor(ago(2 * 3600_000 + 5 * 60_000), now)).toBe('2h 5m');
    expect(runningFor(ago(26 * 3600_000 + 7 * 60_000), now)).toBe('26h 7m');
  });

  it('is blank for a missing or unreadable launch time, and never negative', () => {
    expect(runningFor('', now)).toBe('');
    expect(runningFor('soon', now)).toBe('');
    expect(runningFor(ago(-5 * 60_000), now)).toBe('<1m');
  });

  it('shows on the row of an underway or needs-you ship, not an anchored one', () => {
    const launched = ago(90 * 60_000);
    expect(shipRowAge({ ...ship('a', 'underway'), launched }, now)).toBe('1h 30m');
    expect(shipRowAge({ ...ship('b', 'needs_you'), launched }, now)).toBe('1h 30m');
    expect(shipRowAge({ ...ship('c', 'anchored'), launched }, now)).toBe('1h');
  });
});
