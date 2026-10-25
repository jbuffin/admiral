import { describe, expect, it } from 'vitest';
import * as shipListModule from './shipList';
import { sortShips } from './shipList';
import type { WireCrew, WireShip } from './ships';

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

// Crew boats on the phone Ships list (#174). The interface it is built to, in shipList.ts:
//
//   asksYou(ship)          the ship is in the needs-you group: its status is needs_you (an underway ship whose boat needs
//                          you arrives so, reason 'crew'), or any crew[] boat has status needs_you (an anchored ship
//                          stays anchored on the wire)
//   askingCount(ships)     how many ships asksYou; the Ships tab badge and the Ships hint, sailing_off ships left out
//   shipStatusLine(ship, now)
//                          the row's status line: "Needs you · <crew names joined by ', '> · waiting <age>" when the
//                          ship's needs-you is only its crew (reason 'crew', or an anchored ship with a needing boat),
//                          else as before ("Needs you · <reason words> · waiting …", "Anchored, exited N", "Underway")
//   sortShips              the needs-you group by the ship's stop_since, else its earliest needing boat's
//                          last_activity ?? launched; an empty stamp sorts last in the group, never first by accident

// The new exports are reached through the module so a missing one fails at runtime, not at compile time.
const list = shipListModule as unknown as {
  asksYou(s: WireShip): boolean;
  askingCount(l: WireShip[]): number;
  shipStatusLine(s: WireShip, now: number): string;
};

const boat = (name: string, status: WireCrew['status'], launchedMin: number, activityMin?: number): WireCrew =>
  ({
    name,
    status,
    launched: at(launchedMin),
    ...(activityMin === undefined ? {} : { last_activity: at(activityMin) }),
  }) as WireCrew;

const withCrew = (s: WireShip, ...crew: WireCrew[]): WireShip => ({ ...s, crew });

describe('crew boats and the needs-you group', () => {
  it('puts an anchored ship with a needing boat in the group, and an underway or anchored one without it out', () => {
    expect(list.asksYou(withCrew(ship('a', 'anchored', 1), boat('api', 'needs_you', 1, 5)))).toBe(true);
    expect(list.asksYou(withCrew(ship('b', 'needs_you', 1, 5), boat('api', 'underway', 1)))).toBe(true);
    expect(list.asksYou(withCrew(ship('c', 'underway', 1), boat('api', 'underway', 1), boat('web', 'anchored', 1)))).toBe(false);
    expect(list.asksYou(withCrew(ship('d', 'anchored', 1), boat('api', 'anchored', 1)))).toBe(false);
  });

  it('sorts an anchored ship with a needing boat among the needs-you ships by that boat’s last activity', () => {
    const quiet = ship('quiet', 'underway', 90);
    const anchored = withCrew(ship('anchored-crew', 'anchored', 80), boat('api', 'needs_you', 70, 20));
    const long = ship('waited-30', 'needs_you', 10, 30);
    const short = ship('waited-5', 'needs_you', 10, 5);
    expect(order([quiet, short, anchored, long])).toEqual(['waited-5', 'anchored-crew', 'waited-30', 'quiet']);
  });

  it('falls back to the boat’s launch when it has no activity yet', () => {
    const anchored = withCrew(ship('anchored-crew', 'anchored', 80), boat('api', 'needs_you', 15));
    expect(order([ship('waited-30', 'needs_you', 10, 30), anchored, ship('waited-5', 'needs_you', 10, 5)])).toEqual([
      'waited-5',
      'anchored-crew',
      'waited-30',
    ]);
  });

  it('takes the earliest of several needing boats', () => {
    const two = withCrew(ship('two', 'anchored', 80), boat('late', 'needs_you', 1, 40), boat('early', 'needs_you', 1, 12));
    expect(order([ship('waited-30', 'needs_you', 10, 30), two, ship('waited-5', 'needs_you', 10, 5)])).toEqual(['waited-5', 'two', 'waited-30']);
  });

  it('never lets an empty stamp sort first in the group', () => {
    // The server can send an underway ship with reason crew and stop_since "" when no boat stamp parses.
    const blank = { ...ship('blank', 'needs_you', 5), reason: 'crew', stop_since: '' } as WireShip;
    expect(order([blank, ship('waited-30', 'needs_you', 10, 30), ship('waited-5', 'needs_you', 10, 5)])).toEqual(['waited-5', 'waited-30', 'blank']);
  });

  it('counts the same set the group holds, and leaves out a ship that is sailing off', () => {
    const l = [
      ship('plain', 'needs_you', 1, 5),
      withCrew(ship('anchored-crew', 'anchored', 2), boat('api', 'needs_you', 2, 3)),
      withCrew(ship('quiet', 'underway', 3), boat('api', 'underway', 3)),
      withCrew(ship('gone', 'sailing_off', 4), boat('api', 'needs_you', 4, 4)),
    ];
    expect(list.askingCount(l)).toBe(2);
    expect(order(l).slice(0, 2)).toEqual(['plain', 'anchored-crew']);
  });
});

describe('shipStatusLine', () => {
  const now = T0 + 60 * 60_000;

  it('names the crew boats for a ship that needs you only through them', () => {
    const s = { ...withCrew(ship('s', 'needs_you', 1, 20), boat('api', 'needs_you', 1, 20), boat('web', 'needs_you', 1, 25)), reason: 'crew' } as WireShip;
    expect(list.shipStatusLine(s, now)).toBe('Needs you · api, web · waiting 40 min');
  });

  it('does the same for an anchored ship with a needing boat, waiting from the boat’s last activity', () => {
    const s = withCrew(ship('s', 'anchored', 1), boat('quiet', 'underway', 1), boat('api', 'needs_you', 1, 30));
    expect(list.shipStatusLine(s, now)).toBe('Needs you · api · waiting 30 min');
  });

  it('keeps the ship’s own reason when it needs you itself', () => {
    expect(list.shipStatusLine(ship('s', 'needs_you', 1, 50), now)).toBe('Needs you · Wants to run Bash · waiting 10 min');
  });

  it('reads Underway and Anchored as before', () => {
    expect(list.shipStatusLine(ship('s', 'underway', 1), now)).toBe('Underway');
    expect(list.shipStatusLine({ ...ship('s', 'anchored', 1), exit_code: 1 } as WireShip, now)).toBe('Anchored, exited 1');
  });
});
