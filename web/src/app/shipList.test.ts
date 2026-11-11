import { describe, expect, it } from 'vitest';
import type { Ship, ShipStatus } from './api';
import { groupShips, shipAge } from './shipList';

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
