import { describe, expect, it } from 'vitest';
import type { Ship, ShipStatus } from './api';
import { groupShips } from './shipList';

const ship = (name: string, status: ShipStatus, fleet = 'zulu'): Ship => ({
  session: `s-${name}`,
  name,
  fleet,
  harness: 'claude',
  status,
  order_id: '',
  title: '',
  tail: '',
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
