import { describe, expect, it } from 'vitest';
import { sortShips } from './shipList';
import type { WireShip } from './ships';

// The phone Ships list (issue #119, u2, C5). The interface it is built to, pure:
//
//   sortShips(ships)   the rows in order: ships that need you first, longest waiting first (stop_since ascending),
//                      then every other ship, newest first (launched descending). sailing_off ships are left out,
//                      anchored ships stay. The input is not changed.

const T0 = Date.parse('2026-10-22T10:00:00Z');
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();

const ship = (session: string, status: WireShip['status'], launchedMin: number, stopMin?: number): WireShip =>
  ({
    session,
    name: session,
    status,
    launched: at(launchedMin),
    ...(stopMin === undefined ? {} : { reason: 'permission:Bash', stop_since: at(stopMin) }),
  }) as WireShip;

const order = (list: WireShip[]) => sortShips(list).map((s) => s.session);

describe('sortShips', () => {
  it('lists needs-you ships first, the longest waiting first, whatever their launch order', () => {
    const list = [
      ship('new-underway', 'underway', 50),
      ship('waited-5', 'needs_you', 10, 5),
      ship('waited-30', 'needs_you', 40, 30),
      ship('waited-20', 'needs_you', 20, 20),
    ];
    expect(order(list)).toEqual(['waited-5', 'waited-20', 'waited-30', 'new-underway']);
  });

  it('lists the rest newest first, anchored ships among them', () => {
    const list = [ship('old', 'underway', 1), ship('anchored', 'anchored', 30), ship('mid', 'underway', 20)];
    expect(order(list)).toEqual(['anchored', 'mid', 'old']);
  });

  it('leaves out ships that are sailing off', () => {
    const list = [ship('gone', 'sailing_off', 99), ship('here', 'underway', 1)];
    expect(order(list)).toEqual(['here']);
  });

  it('does not change the list it is given', () => {
    const list = [ship('a', 'underway', 1), ship('b', 'needs_you', 2, 2)];
    sortShips(list);
    expect(list.map((s) => s.session)).toEqual(['a', 'b']);
  });
});
