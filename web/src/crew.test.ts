import { describe, expect, it } from 'vitest';
import { attachCommand, bridgePath, crewRowText, findCrew, takesInput } from './crew';
import type { WireCrew, WireShip } from './ships';

// Crew boats on the bridge (#154 u1). The interface the module `./crew` is built to, all pure:
//
//   crewRowText(c)               the flotilla row's second line for a crew boat:
//                                "Teammate · working", "Teammate · needs you",
//                                "Teammate · anchored, exit N", "Teammate · anchored, killed (SIG)"
//   bridgePath(session, crew?)   the bridge WebSocket's path: /ws/bridge/<session>, or
//                                /ws/bridge/<session>/crew/<crew> with both parts through encodeURIComponent
//   findCrew(ships, session, crew)
//                                the boat in that ship's `crew[]`, or undefined: the bridge returns to the ship when
//                                the boarded boat is no longer in a `ships` push, and a crew link's reconnect probe
//                                gives up on a boat that is gone even though its ship is still listed
//   takesInput(ship, crew?)      whether the boarded terminal takes keys: the crew boat's own status when one is
//                                boarded (a live boat of an anchored ship does), else the ship's
//
// `WireCrew` (exported from ./ships) is the wire's crew entry; exit_code and exit_signal are absent while live.

const crew = (extra: Partial<WireCrew>): WireCrew =>
  ({ name: 'api', harness: 'claude', dir: '/work/api', status: 'underway', launched: '2026-10-24T10:00:00Z', ...extra }) as WireCrew;
const ship = (session: string, status: WireShip['status'], boats: WireCrew[] = []): WireShip =>
  ({ session, name: session, status, crew: boats, subagents: [], crew_live: boats.length }) as unknown as WireShip;

describe('crewRowText', () => {
  it('reads the state in words, and an exit code or a signal once anchored', () => {
    expect(crewRowText(crew({ status: 'underway' }))).toBe('Teammate · working');
    expect(crewRowText(crew({ status: 'needs_you', reason: 'idle' }))).toBe('Teammate · needs you');
    expect(crewRowText(crew({ status: 'anchored', exit_code: 3 }))).toBe('Teammate · anchored, exit 3');
    expect(crewRowText(crew({ status: 'anchored', exit_code: 0 }))).toBe('Teammate · anchored, exit 0');
    expect(crewRowText(crew({ status: 'anchored', exit_signal: 'KILL' }))).toBe('Teammate · anchored, killed (KILL)');
  });
});

describe('bridgePath', () => {
  it('is the ship socket, or the crew socket with both parts encoded', () => {
    expect(bridgePath('s1')).toBe('/ws/bridge/s1');
    expect(bridgePath('s 1', 'api')).toBe('/ws/bridge/s%201/crew/api');
    expect(bridgePath('a/b', 'x?y#z')).toBe('/ws/bridge/a%2Fb/crew/x%3Fy%23z');
  });
});

describe('findCrew', () => {
  const list = [ship('s1', 'underway', [crew({ name: 'api' }), crew({ name: 'docs' })]), ship('s2', 'underway', [crew({ name: 'api' })])];
  it('finds the boat in its own ship only', () => {
    expect(findCrew(list, 's1', 'docs')?.name).toBe('docs');
    expect(findCrew(list, 's2', 'docs')).toBeUndefined();
  });
  it('finds nothing for a boat that left or a ship that is gone', () => {
    expect(findCrew([ship('s1', 'underway')], 's1', 'api')).toBeUndefined();
    expect(findCrew(list, 'nope', 'api')).toBeUndefined();
  });
});

describe('takesInput', () => {
  it('follows the boarded crew boat, not the ship', () => {
    const live = crew({ status: 'underway' });
    const needs = crew({ status: 'needs_you' });
    const done = crew({ status: 'anchored', exit_code: 1 });
    expect(takesInput(ship('s', 'anchored', [live]), live)).toBe(true);
    expect(takesInput(ship('s', 'anchored', [needs]), needs)).toBe(true);
    expect(takesInput(ship('s', 'underway', [done]), done)).toBe(false);
  });
  it('follows the ship when no crew boat is boarded', () => {
    expect(takesInput(ship('s', 'underway'))).toBe(true);
    expect(takesInput(ship('s', 'needs_you'))).toBe(true);
    expect(takesInput(ship('s', 'anchored'))).toBe(false);
  });
});

describe('attachCommand', () => {
  it('leaves ordinary names alone and quotes the ones a shell would act on', () => {
    expect(attachCommand('alpha-1', 'api')).toBe('admiral attach alpha-1 api');
    expect(attachCommand('alpha')).toBe('admiral attach alpha');
    expect(attachCommand('a b', "it's;rm")).toBe("admiral attach 'a b' 'it'\\''s;rm'");
    expect(attachCommand('alpha', '-x')).toBe("admiral attach alpha '-x'");
  });
});
