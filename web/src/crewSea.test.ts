import { describe, expect, it } from 'vitest';
import { crewAsks, deckLabel, plaqueNeeds, raft, shouldChime, subagentTip } from './crewSea';
import type { WireCrew, WireShip, WireSubagent } from './ships';

// Crew boats and subagents on the sea (#154 u2). The interface the module `./crewSea` is built to, all pure:
//
//   plaqueNeeds(crew)         the plaque's line while crew boats need you: the names of the boats with
//                             status 'needs_you', in crew order, joined by ", ", then " needs you"
//                             ("checker needs you", "api, docs needs you"); null when none does. It is keyed on
//                             the boats, never the ship's `reason`, so it also holds while the ship needs you itself.
//   raft(crew)                { shown, hidden, pill }: the first three boats raft; `hidden` is the rest;
//                             `pill` is null with no hidden boat, else { text, name }: text is "+N crew", or
//                             "+N crew !" when a hidden boat needs you; name is "N more crew boats; board the ship
//                             to see them"; the tests fix only the plural
//   shouldChime(before, now, first)
//                             whether this `ships` message plays the chime: false for the first message of a
//                             connection (`first`); true when a ship that was not needs_you turns needs_you, or a
//                             crew boat (key "<session>/<name>") that was not needs_you in `before` is needs_you
//                             in `now`, whether the boat is new or changed; a ship that is new in `now` never
//                             chimes (as today). One boolean per message: two boats at once is one chime.
//   subagentTip(subs, now)    the deck tip: "<b>N subagent(s)</b> · <type> <age> · … · no terminal" as plain text:
//                             "1 subagent · Explore just started · no terminal"; age is "just started" under a
//                             minute (or when `start` does not parse), else "Nm"; an empty type reads "subagent".
//                             `now` is a ms timestamp.
//   deckLabel(shipName, subs) the deck button's accessible name:
//                             "N subagent(s) on <ship>: <types>. They have no terminal" (types joined ", ",
//                             an empty type reads "subagent")
//
// crewAsks(c) is `c.status === 'needs_you'`, the one place a boat's asking is decided.

const crew = (name: string, status: WireCrew['status'] = 'underway'): WireCrew =>
  ({ name, harness: 'claude', dir: `/work/${name}`, status, launched: '2026-10-24T10:00:00Z' }) as WireCrew;
const ship = (session: string, status: WireShip['status'], boats: WireCrew[] = []): WireShip =>
  ({ session, name: session, status, crew: boats, subagents: [], crew_live: boats.length }) as unknown as WireShip;
const sub = (type: string, start: string): WireSubagent => ({ id: `a-${type}-${start}`, type, start });

describe('plaqueNeeds', () => {
  it('is null while no crew boat needs you, even with the ship asking itself', () => {
    expect(plaqueNeeds([])).toBeNull();
    expect(plaqueNeeds([crew('api'), crew('docs', 'anchored')])).toBeNull();
  });
  it('names the boats that need you, in crew order', () => {
    expect(plaqueNeeds([crew('checker', 'needs_you'), crew('api')])).toBe('checker needs you');
    expect(plaqueNeeds([crew('api', 'needs_you'), crew('docs', 'needs_you'), crew('x')])).toBe('api, docs needs you');
  });
});

describe('crewAsks', () => {
  it('is true only for needs_you', () => {
    expect(crewAsks(crew('a', 'needs_you'))).toBe(true);
    expect(crewAsks(crew('a', 'underway'))).toBe(false);
    expect(crewAsks(crew('a', 'anchored'))).toBe(false);
  });
});

describe('raft', () => {
  const four = [crew('a'), crew('b'), crew('c'), crew('d')];
  it('rafts up to three boats and has no pill', () => {
    expect(raft([]).shown).toEqual([]);
    expect(raft([]).pill).toBeNull();
    const three = raft(four.slice(0, 3));
    expect(three.shown.map((c) => c.name)).toEqual(['a', 'b', 'c']);
    expect(three.pill).toBeNull();
  });
  it('rafts the first three and counts the rest in the pill', () => {
    const r = raft([...four, crew('e')]);
    expect(r.shown.map((c) => c.name)).toEqual(['a', 'b', 'c']);
    expect(r.pill?.text).toBe('+2 crew');
    expect(r.pill?.name).toBe('2 more crew boats; board the ship to see them');
  });
  it('shows the "!" on the pill only when a hidden boat needs you', () => {
    expect(raft([crew('a', 'needs_you'), crew('b'), crew('c'), crew('d')]).pill?.text).toBe('+1 crew');
    expect(raft([crew('a'), crew('b'), crew('c'), crew('d', 'needs_you')]).pill?.text).toBe('+1 crew !');
  });
});

describe('shouldChime', () => {
  const asking = (boats: WireCrew[] = [], status: WireShip['status'] = 'underway') => ship('s1', status, boats);
  it('is silent for the first message of a connection', () => {
    expect(shouldChime([], [asking([crew('a', 'needs_you')], 'needs_you')], true)).toBe(false);
  });
  it('chimes when the ship turns needs_you, as before', () => {
    expect(shouldChime([asking()], [asking([], 'needs_you')], false)).toBe(true);
    expect(shouldChime([asking([], 'needs_you')], [asking([], 'needs_you')], false)).toBe(false);
  });
  it('chimes when a crew boat turns needs_you, with the ship turning at the same moment', () => {
    expect(shouldChime([asking([crew('a')])], [asking([crew('a', 'needs_you')], 'needs_you')], false)).toBe(true);
  });
  it('chimes again for a second boat while the ship already needs you', () => {
    const one = asking([crew('a', 'needs_you'), crew('b')], 'needs_you');
    const two = asking([crew('a', 'needs_you'), crew('b', 'needs_you')], 'needs_you');
    expect(shouldChime([one], [two], false)).toBe(true);
    expect(shouldChime([two], [two], false)).toBe(false);
  });
  it('chimes for a boat that appears already needing you, and for an anchored ship\'s boat', () => {
    expect(shouldChime([asking([crew('a')])], [asking([crew('a'), crew('b', 'needs_you')], 'needs_you')], false)).toBe(true);
    expect(shouldChime([asking([], 'anchored')], [asking([crew('b', 'needs_you')], 'anchored')], false)).toBe(true);
  });
  it('is silent when a boat stops asking, or a new ship arrives', () => {
    expect(shouldChime([asking([crew('a', 'needs_you')], 'needs_you')], [asking([crew('a')])], false)).toBe(false);
    expect(shouldChime([], [asking([], 'needs_you')], false)).toBe(false);
  });
  it('is one chime for a message, however many boats and ships turned', () => {
    const before = [ship('s1', 'underway', [crew('a'), crew('b')]), ship('s2', 'underway')];
    const now = [ship('s1', 'needs_you', [crew('a', 'needs_you'), crew('b', 'needs_you')]), ship('s2', 'needs_you')];
    expect(shouldChime(before, now, false)).toBe(true);
  });
});

describe('subagentTip', () => {
  const now = Date.parse('2026-10-24T10:10:00Z');
  it('reads one subagent started just now', () => {
    expect(subagentTip([sub('Explore', '2026-10-24T10:09:50Z')], now)).toBe('1 subagent · Explore just started · no terminal');
  });
  it('lists each subagent with its age, in minutes from a minute on', () => {
    expect(subagentTip([sub('Explore', '2026-10-24T10:09:10Z'), sub('Plan', '2026-10-24T10:07:00Z')], now)).toBe(
      '2 subagents · Explore just started · Plan 3m · no terminal',
    );
  });
  it('falls back to "subagent" for an empty type and to "just started" for a start that does not parse', () => {
    expect(subagentTip([sub('', '2026-10-24T10:09:55Z')], now)).toBe('1 subagent · subagent just started · no terminal');
    expect(subagentTip([sub('Explore', 'not a time')], now)).toBe('1 subagent · Explore just started · no terminal');
  });
});

describe('deckLabel', () => {
  it('names the count, the ship and the types, and says there is no terminal', () => {
    expect(deckLabel('Saltpaw', [sub('Explore', 'x')])).toBe('1 subagent on Saltpaw: Explore. They have no terminal');
    expect(deckLabel('Saltpaw', [sub('Explore', 'x'), sub('', 'y')])).toBe('2 subagents on Saltpaw: Explore, subagent. They have no terminal');
  });
});
