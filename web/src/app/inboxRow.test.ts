import { describe, expect, it } from 'vitest';
import type { CrewBoat, Fleet, Ship } from './api';
import type { Entry, InboxItem } from './inbox';
import { entryAge, fleetLabel, reasonWords, rowShip, rowVerb } from './inboxRow';

const T0 = Date.parse('2026-10-22T10:00:00Z');

const fleet = (key: string, name: string, extra: Partial<Fleet> = {}): Fleet => ({
  key,
  name,
  color: `#${key}`,
  ink: '#000',
  search_roots: [],
  owners: [],
  wake_reviewers: [],
  clone_root: '',
  can_launch: true,
  docks: [],
  ...extra,
});

const ship = { session: 's1', name: 'Dory', fleet: 'a', status: 'needs_you', reason: 'question' } as unknown as Ship;
const itemEntry = (extra: Partial<InboxItem>, over: Partial<Entry> = {}): Entry => ({
  key: 'item:s1/q-1',
  type: 'item',
  session: 's1',
  id: 'q-1',
  item: { version: 1, id: 'q-1', session: 's1', kind: 'question', created: '2026-10-22T10:00:00Z', title: 'Which port?', name: 'Dory', ...extra },
  folded: null,
  since: T0,
  ...over,
});
const stopEntry = (reason: string, over: Partial<Entry> = {}): Entry => ({
  key: 'stop:s1',
  type: 'stop',
  session: 's1',
  ship: { ...ship, reason } as Ship,
  folded: null,
  since: T0,
  ...over,
});

describe('rowVerb', () => {
  it('words an item by its kind', () => {
    expect(rowVerb(itemEntry({ kind: 'question' }))).toBe('asked');
    expect(rowVerb(itemEntry({ kind: 'review' }))).toBe('requested a review');
  });

  it('counts the finding cards', () => {
    expect(rowVerb(itemEntry({ kind: 'findings', findings: { cards: [{}, {}, {}] } }))).toBe('sent 3 finding cards');
    expect(rowVerb(itemEntry({ kind: 'findings', findings: { cards: [{}] } }))).toBe('sent 1 finding card');
  });

  it('words the two crew kinds', () => {
    expect(rowVerb(itemEntry({ kind: 'crew_notes', session: '_admiral', name: 'Admiral' }))).toBe('proposes crew notes');
    expect(rowVerb(itemEntry({ kind: 'crew_member', session: '_admiral', name: 'Admiral' }))).toBe('asks to keep a crew member');
  });

  it('words a stop by its reason', () => {
    expect(rowVerb(stopEntry('permission:Bash'))).toBe('wants to run Bash');
    expect(rowVerb(stopEntry('stop'))).toBe('finished and is waiting');
  });
});

describe('rowShip', () => {
  it('names the ship the item came from', () => {
    expect(rowShip(itemEntry({ name: 'Dory' }))).toBe('Dory');
  });

  it('names the admiral on a notice', () => {
    expect(rowShip(itemEntry({ kind: 'notice', name: 'Admiral', session: '_admiral' }))).toBe('Admiral');
  });

  it('falls back to the ship record when the item has no name', () => {
    expect(rowShip(itemEntry({ name: undefined }, { ship }))).toBe('Dory');
  });

  it('names a crew boat after its ship with the › separator', () => {
    const crew = { name: 'mate', status: 'needs_you' } as CrewBoat;
    expect(rowShip(stopEntry('question', { ship, crew }))).toBe('Dory › mate');
  });
});

describe('fleetLabel', () => {
  const fleets = [fleet('a', 'Alpha', { color_index: 2 }), fleet('b', 'Bravo', { color: '#123456' })];

  it('paints the dot as the Ships list does, from the fleets signal', () => {
    const e = itemEntry({}, { fleet: { key: 'a', name: 'Stale', color: '#feed00' } });
    expect(fleetLabel(e, fleets)).toEqual({ name: 'Stale', fill: 'var(--fleet-2)' });
    const b = itemEntry({}, { fleet: { key: 'b', name: 'Bravo', color: '#feed00' } });
    expect(fleetLabel(b, fleets)!.fill).toBe('#123456');
  });

  it('takes the name from the fleets signal when the entry has none', () => {
    const e = itemEntry({}, { fleet: { key: 'a', name: '', color: '#feed00' } });
    expect(fleetLabel(e, fleets)!.name).toBe('Alpha');
  });

  it('falls back to the key and the entry\'s own color for a fleet no longer configured', () => {
    const e = itemEntry({}, { fleet: { key: 'gone', name: '', color: '#feed00' } });
    expect(fleetLabel(e, fleets)).toEqual({ name: 'gone', fill: '#feed00' });
  });

  it('has no fleet on a notice', () => {
    expect(fleetLabel(itemEntry({ kind: 'notice' }), fleets)).toBeNull();
  });
});

describe('entryAge', () => {
  it('prints the short form of the Ships list', () => {
    expect(entryAge(itemEntry({}, { since: T0 }), T0 + 30_000)).toBe('now');
    expect(entryAge(itemEntry({}, { since: T0 }), T0 + 3 * 60_000)).toBe('3m');
    expect(entryAge(itemEntry({}, { since: T0 }), T0 + 61 * 60_000)).toBe('1h');
  });

  it('prints nothing when the start is unknown', () => {
    expect(entryAge(itemEntry({}, { since: 0 }), T0)).toBe('');
  });
});

describe('reasonWords', () => {
  it('words a stop reason', () => {
    expect(reasonWords('permission:Edit')).toBe('Wants to use Edit');
    expect(reasonWords('bell')).toBe('Rang the bell');
  });
});

describe('a merge entry', () => {
  const mergeEntry = (over: Partial<Entry> = {}): Entry => ({
    key: 'merge:s1',
    type: 'merge',
    session: 's1',
    ship: { ...ship, status: 'moored' } as Ship,
    folded: null,
    since: T0,
    ...over,
  });

  it('names the ship from its record and reads after it as ready to merge', () => {
    expect(rowShip(mergeEntry())).toBe('Dory');
    expect(rowVerb(mergeEntry())).toBe('is ready to merge');
  });
});
