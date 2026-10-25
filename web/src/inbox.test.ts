import { describe, expect, it } from 'vitest';
import { HOLD_MS, createQueue, reasonWords, safeHref, waited, type InboxItem } from './inbox';
import type { WireCrew, WireShip } from './ships';

// The inbox queue (#116 u3, S1). The interface the module is built to, all pure: time comes in as `now`
// (epoch ms), nothing here sets a timer.
//
//   createQueue()                    an empty queue
//   q.sync({ items, ships }, now)    the served inbox items and the ships signal's list; merges the needs-you
//                                    stops in. An entry that is no longer served leaves without a fold, except a
//                                    stop that went needs_you -> underway, which folds to "Back underway".
//   q.fold(key, text, now)           the engineer acted: count drops now, the entry stays (folded, text) for HOLD_MS
//   q.tick(now)                      removes entries whose hold has run out; the cursor moves to the next entry
//   q.entries()                      in order, oldest first: { key, type: 'item' | 'stop', session, id?, folded: string | null, … }
//   q.count()                        the badge: entries that are not folded
//   q.position()                     { n, total } for "n of N" (folded entries count), or null when empty
//   q.currentKey(), q.move(±1), q.select(key)
//   q.pulsing()                      at least one needs-you stop is open (not folded)
//
// Keys: items `item:<session>/<id>`, needs-you stops `stop:<session>`, crew stops `stop:<session>/<crew>`.
//
// Crew stops (#154 u3). The seam: a crew boat with status needs_you in a ship's `crew[]` is an entry of its own,
//   { key: 'stop:<session>/<crew>', type: 'stop', session, id: <crew name>, ship: <the WireShip>, crew: <the WireCrew>,
//     folded, since }
// `crew` is set on crew stops only. `since` is frozen at first sight: Date.parse(last_activity) || Date.parse(launched)
// || now, and later pushes do not move it. The ship's own stop exists when `status === 'needs_you' && reason !== 'crew'`
// (the wire shows needs_you/crew while only a crew boat waits); that one predicate also drives its "Back underway" fold
// and its first-seen time. A crew stop folds "Back underway" when its boat goes back to underway; any other way out is
// silent. Crew stops count and pulse like ship stops.

const T0 = Date.parse('2026-10-22T10:00:00Z');
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();

const item = (session: string, id: string, min: number, extra: Partial<InboxItem> = {}): InboxItem =>
  ({ version: 1, id, session, kind: 'question', created: at(min), title: `Item ${id}`, ...extra }) as InboxItem;
const ship = (session: string, status: WireShip['status'], stopMin?: number, reason = 'permission:Bash', crew: WireCrew[] = []): WireShip =>
  ({
    session,
    name: session,
    status,
    reason: status === 'needs_you' ? reason : undefined,
    stop_since: status === 'needs_you' && stopMin !== undefined ? at(stopMin) : undefined,
    crew,
  }) as unknown as WireShip;
const boat = (name: string, status: WireCrew['status'], extra: Partial<WireCrew> = {}): WireCrew =>
  ({
    name,
    harness: 'claude',
    dir: '/w',
    status,
    reason: status === 'needs_you' ? 'permission:Bash' : undefined,
    launched: at(-30),
    ...extra,
  }) as WireCrew;
// A lead that is underway itself while one of its crew boats waits: the wire says needs_you/crew.
const crewOnly = (session: string, boats: WireCrew[]) => ship(session, 'needs_you', 1, 'crew', boats);

const keys = (q: ReturnType<typeof createQueue>) => q.entries().map((e) => e.key);

describe('order and count', () => {
  it('merges items and needs-you stops oldest first; ties go to items, then session, then id', () => {
    const q = createQueue();
    q.sync(
      {
        items: [item('b', 'z', 5), item('a', 'y', 5), item('a', 'x', 5), item('c', 'late', 9), item('c', 'early', 1)],
        ships: [ship('s2', 'needs_you', 5), ship('s1', 'needs_you', 5), ship('s3', 'needs_you', 3), ship('u', 'underway')],
      },
      T0,
    );
    expect(keys(q)).toEqual([
      'item:c/early',
      'stop:s3',
      'item:a/x',
      'item:a/y',
      'item:b/z',
      'stop:s1',
      'stop:s2',
      'item:c/late',
    ]);
    expect(q.count()).toBe(8);
  });

  it('keys an item by its session and id, so two ships may both have q-1', () => {
    const q = createQueue();
    q.sync({ items: [item('a', 'q-1', 1), item('b', 'q-1', 2)], ships: [] }, T0);
    expect(keys(q)).toEqual(['item:a/q-1', 'item:b/q-1']);
  });

  it('an empty queue has no current entry and no position', () => {
    const q = createQueue();
    q.sync({ items: [], ships: [ship('a', 'underway')] }, T0);
    expect(q.count()).toBe(0);
    expect(q.currentKey()).toBeNull();
    expect(q.position()).toBeNull();
    expect(q.pulsing()).toBe(false);
  });

  it('the cursor starts on the oldest and stays on its entry when an older one arrives', () => {
    const q = createQueue();
    q.sync({ items: [item('a', 'm', 5), item('a', 'n', 6)], ships: [] }, T0);
    q.move(1);
    expect(q.currentKey()).toBe('item:a/n');
    q.sync({ items: [item('a', 'old', 1), item('a', 'm', 5), item('a', 'n', 6)], ships: [] }, T0);
    expect(q.currentKey()).toBe('item:a/n');
    expect(q.position()).toEqual({ n: 3, total: 3 });
  });

  it('move stops at both ends', () => {
    const q = createQueue();
    q.sync({ items: [item('a', '1', 1), item('a', '2', 2)], ships: [] }, T0);
    q.move(-1);
    expect(q.position()).toEqual({ n: 1, total: 2 });
    q.move(1);
    q.move(1);
    expect(q.position()).toEqual({ n: 2, total: 2 });
  });

  it('pulses while a needs-you stop is open, and not for items alone', () => {
    const q = createQueue();
    q.sync({ items: [item('a', '1', 1)], ships: [] }, T0);
    expect(q.pulsing()).toBe(false);
    q.sync({ items: [item('a', '1', 1)], ships: [ship('a', 'needs_you', 2)] }, T0);
    expect(q.pulsing()).toBe(true);
  });
});

describe('resolve, the hold and removal', () => {
  const three = () => {
    const q = createQueue();
    q.sync({ items: [item('a', '1', 1), item('a', '2', 2), item('a', '3', 3)], ships: [] }, T0);
    return q;
  };

  it('the count drops at once; the folded entry stays in the list and in "n of N"', () => {
    const q = three();
    q.fold('item:a/1', 'Answered', T0);
    expect(q.count()).toBe(2);
    expect(keys(q)).toEqual(['item:a/1', 'item:a/2', 'item:a/3']);
    expect(q.entries()[0].folded).toBe('Answered');
    expect(q.entries()[1].folded).toBeNull();
    expect(q.position()).toEqual({ n: 1, total: 3 });
    expect(q.currentKey()).toBe('item:a/1');
  });

  it('holds for HOLD_MS (5 s), then removes it and moves the cursor to the next entry', () => {
    const q = three();
    q.fold('item:a/1', 'Answered', T0);
    q.tick(T0 + HOLD_MS - 1);
    expect(keys(q)).toContain('item:a/1');
    q.tick(T0 + HOLD_MS);
    expect(keys(q)).toEqual(['item:a/2', 'item:a/3']);
    expect(q.currentKey()).toBe('item:a/2');
    expect(q.position()).toEqual({ n: 1, total: 2 });
  });

  it('at the end of the queue the cursor goes to the previous entry', () => {
    const q = three();
    q.select('item:a/3');
    q.fold('item:a/3', 'Answered', T0);
    q.tick(T0 + HOLD_MS);
    expect(q.currentKey()).toBe('item:a/2');
    expect(q.position()).toEqual({ n: 2, total: 2 });
  });

  it('removing an entry that is not current leaves the cursor where it was', () => {
    const q = three();
    q.select('item:a/2');
    q.fold('item:a/3', 'Answered', T0);
    q.tick(T0 + HOLD_MS);
    expect(q.currentKey()).toBe('item:a/2');
  });

  it('the last entry going leaves an empty queue', () => {
    const q = createQueue();
    q.sync({ items: [item('a', '1', 1)], ships: [] }, T0);
    q.fold('item:a/1', 'Answered', T0);
    q.tick(T0 + HOLD_MS);
    expect(q.entries()).toEqual([]);
    expect(q.currentKey()).toBeNull();
    expect(q.position()).toBeNull();
  });

  it('a push that still serves the folded item neither unfolds it nor counts it again', () => {
    const q = three();
    q.fold('item:a/1', 'Answered', T0);
    q.sync({ items: [item('a', '1', 1), item('a', '2', 2), item('a', '3', 3)], ships: [] }, T0 + 1000);
    expect(q.count()).toBe(2);
    expect(q.entries()[0].folded).toBe('Answered');
    q.tick(T0 + HOLD_MS);
    expect(keys(q)).toEqual(['item:a/2', 'item:a/3']);
  });

  it('a push that drops the folded item does not cut the hold short', () => {
    const q = three();
    q.fold('item:a/1', 'Answered', T0);
    q.sync({ items: [item('a', '2', 2), item('a', '3', 3)], ships: [] }, T0 + 1000);
    expect(keys(q)).toEqual(['item:a/1', 'item:a/2', 'item:a/3']);
    q.tick(T0 + HOLD_MS);
    expect(keys(q)).toEqual(['item:a/2', 'item:a/3']);
  });

  it('a new item arriving mid-hold joins in order, counts at once, and the hold still ends on time', () => {
    const q = three();
    q.fold('item:a/3', 'Answered', T0);
    q.sync({ items: [item('a', '1', 1), item('a', '2', 2), item('a', '3', 3), item('a', '4', 4)], ships: [] }, T0 + 2000);
    expect(q.count()).toBe(3);
    expect(q.position()).toEqual({ n: 1, total: 4 });
    q.select('item:a/3');
    q.tick(T0 + HOLD_MS);
    expect(keys(q)).toEqual(['item:a/1', 'item:a/2', 'item:a/4']);
    expect(q.currentKey()).toBe('item:a/4'); // the next entry, which arrived during the hold
  });

  it('two folds in a row each hold for their own time', () => {
    const q = three();
    q.fold('item:a/1', 'Answered', T0);
    q.fold('item:a/2', 'Answered', T0 + 3000);
    expect(q.count()).toBe(1);
    q.tick(T0 + HOLD_MS);
    expect(keys(q)).toEqual(['item:a/2', 'item:a/3']);
    q.tick(T0 + 3000 + HOLD_MS);
    expect(keys(q)).toEqual(['item:a/3']);
    expect(q.currentKey()).toBe('item:a/3');
  });
});

describe('leaving without an action', () => {
  it('an item that vanishes leaves without a fold, and the cursor moves on if it was current', () => {
    const q = createQueue();
    q.sync({ items: [item('a', '1', 1), item('a', '2', 2), item('a', '3', 3)], ships: [] }, T0);
    q.select('item:a/2');
    q.sync({ items: [item('a', '1', 1), item('a', '3', 3)], ships: [] }, T0 + 1000);
    expect(keys(q)).toEqual(['item:a/1', 'item:a/3']);
    expect(q.currentKey()).toBe('item:a/3');
    expect(q.entries().every((e) => e.folded === null)).toBe(true);
    expect(q.count()).toBe(2);
  });

  it('the current item vanishing at the end of the queue moves the cursor to the previous one', () => {
    const q = createQueue();
    q.sync({ items: [item('a', '1', 1), item('a', '2', 2)], ships: [] }, T0);
    q.select('item:a/2');
    q.sync({ items: [item('a', '1', 1)], ships: [] }, T0);
    expect(q.currentKey()).toBe('item:a/1');
  });

  it('a stop that goes needs_you -> underway folds to "Back underway" and holds like any other', () => {
    const q = createQueue();
    q.sync({ items: [], ships: [ship('a', 'needs_you', 1), ship('b', 'needs_you', 2)] }, T0);
    q.sync({ items: [], ships: [ship('a', 'underway'), ship('b', 'needs_you', 2)] }, T0 + 1000);
    expect(q.count()).toBe(1);
    expect(q.pulsing()).toBe(true); // b is still open
    expect(q.entries()[0]).toMatchObject({ key: 'stop:a', folded: 'Back underway' });
    expect(q.position()).toEqual({ n: 1, total: 2 });
    q.tick(T0 + 1000 + HOLD_MS);
    expect(keys(q)).toEqual(['stop:b']);
    expect(q.currentKey()).toBe('stop:b');
  });

  it.each(['anchored', 'sailing_off'] as const)('a stop whose ship becomes %s leaves with no "Back underway"', (status) => {
    const q = createQueue();
    q.sync({ items: [], ships: [ship('a', 'needs_you', 1)] }, T0);
    q.sync({ items: [], ships: [ship('a', status)] }, T0 + 1000);
    expect(q.entries()).toEqual([]);
  });

  it('a stop whose ship is gone leaves with no "Back underway"', () => {
    const q = createQueue();
    q.sync({ items: [], ships: [ship('a', 'needs_you', 1)] }, T0);
    q.sync({ items: [], ships: [] }, T0 + 1000);
    expect(q.entries()).toEqual([]);
  });

  it('a ship that needs you again after folding is a new stop once the old one has left', () => {
    const q = createQueue();
    q.sync({ items: [], ships: [ship('a', 'needs_you', 1)] }, T0);
    q.sync({ items: [], ships: [ship('a', 'underway')] }, T0 + 1000);
    q.tick(T0 + 1000 + HOLD_MS);
    q.sync({ items: [], ships: [ship('a', 'needs_you', 20)] }, T0 + 9000);
    expect(q.count()).toBe(1);
    expect(q.entries()[0]).toMatchObject({ key: 'stop:a', folded: null });
  });
});

describe('crew stops (#154)', () => {
  const sync = (q: ReturnType<typeof createQueue>, ships: WireShip[], now = T0) => q.sync({ items: [], ships }, now);

  it('a crew-only ship has a crew stop and no ship stop', () => {
    const q = createQueue();
    sync(q, [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    expect(keys(q)).toEqual(['stop:a/api']);
    expect(q.entries()[0]).toMatchObject({ type: 'stop', session: 'a', id: 'api', folded: null });
    expect(q.entries()[0].crew).toMatchObject({ name: 'api', reason: 'permission:Bash' });
    expect(q.entries()[0].ship).toMatchObject({ session: 'a' });
  });

  it('a boat that is working makes no stop', () => {
    const q = createQueue();
    sync(q, [ship('a', 'underway', undefined, '', [boat('api', 'underway')])]);
    expect(q.entries()).toEqual([]);
  });

  it('a ship that needs you itself and a waiting crew boat make two entries, oldest first', () => {
    const q = createQueue();
    sync(q, [ship('a', 'needs_you', 5, 'permission:Bash', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    expect(keys(q)).toEqual(['stop:a/api', 'stop:a']);
    expect(q.count()).toBe(2);
  });

  it('an anchored ship with a waiting crew boat still shows the crew stop', () => {
    const q = createQueue();
    sync(q, [ship('a', 'anchored', undefined, '', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    expect(keys(q)).toEqual(['stop:a/api']);
  });

  it("the ship's own stop folds when it is answered, though the wire stays needs_you/crew", () => {
    const q = createQueue();
    const api = boat('api', 'needs_you', { last_activity: at(2) });
    sync(q, [ship('a', 'needs_you', 1, 'permission:Bash', [api])]);
    sync(q, [crewOnly('a', [api])], T0 + 1000);
    expect(q.entries().find((e) => e.key === 'stop:a')).toMatchObject({ folded: 'Back underway' });
    expect(q.entries().find((e) => e.key === 'stop:a/api')).toMatchObject({ folded: null });
    expect(q.count()).toBe(1);
    q.tick(T0 + 1000 + HOLD_MS);
    expect(keys(q)).toEqual(['stop:a/api']);
  });

  it('a ship stop first seen after a crew-only stretch counts from its own first sight', () => {
    const q = createQueue();
    const api = boat('api', 'needs_you', { last_activity: at(2) });
    sync(q, [crewOnly('a', [api])]);
    sync(q, [ship('a', 'needs_you', undefined, 'permission:Bash', [api])], T0 + 5 * 60_000);
    expect(q.entries().find((e) => e.key === 'stop:a')!.since).toBe(T0 + 5 * 60_000);
  });

  it('a crew stop whose boat goes back to underway folds "Back underway"', () => {
    const q = createQueue();
    sync(q, [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    sync(q, [ship('a', 'underway', undefined, '', [boat('api', 'underway')])], T0 + 1000);
    expect(q.entries()[0]).toMatchObject({ key: 'stop:a/api', folded: 'Back underway' });
    expect(q.count()).toBe(0);
    expect(q.pulsing()).toBe(false);
    q.tick(T0 + 1000 + HOLD_MS);
    expect(q.entries()).toEqual([]);
  });

  it('a crew stop folds "Back underway" when its boat is answered on an anchored ship', () => {
    const q = createQueue();
    sync(q, [ship('a', 'anchored', undefined, '', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    expect(keys(q)).toEqual(['stop:a/api']);
    sync(q, [ship('a', 'anchored', undefined, '', [boat('api', 'underway')])], T0 + 1000);
    expect(q.entries()[0]).toMatchObject({ key: 'stop:a/api', folded: 'Back underway' });
  });

  it.each([
    ['the boat is closed', () => ship('a', 'underway', undefined, '', [])],
    ['the boat anchors', () => ship('a', 'underway', undefined, '', [boat('api', 'anchored', { exit_code: 0 })])],
    ['the ship is sailing off', () => ship('a', 'sailing_off', undefined, '', [boat('api', 'needs_you')])],
  ])('a crew stop leaves silently when %s', (_why, next) => {
    const q = createQueue();
    sync(q, [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    expect(keys(q)).toEqual(['stop:a/api']);
    sync(q, [next()], T0 + 1000);
    expect(q.entries()).toEqual([]);
  });

  it('a crew stop leaves silently when its ship is gone, and the cursor moves on', () => {
    const q = createQueue();
    q.sync({ items: [item('z', '1', 9)], ships: [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(2) })])] }, T0);
    expect(q.currentKey()).toBe('stop:a/api');
    q.sync({ items: [item('z', '1', 9)], ships: [] }, T0 + 1000);
    expect(keys(q)).toEqual(['item:z/1']);
    expect(q.currentKey()).toBe('item:z/1');
  });

  it('since is last_activity, else launched, else the first sight, and later pushes do not move it', () => {
    const q = createQueue();
    sync(q, [
      crewOnly('a', [
        boat('act', 'needs_you', { last_activity: at(3) }),
        boat('lau', 'needs_you', { launched: at(-10), last_activity: undefined }),
        boat('now', 'needs_you', { launched: undefined, last_activity: undefined } as Partial<WireCrew>),
      ]),
    ]);
    const since = () => Object.fromEntries(q.entries().map((e) => [e.id, e.since]));
    expect(since()).toEqual({ lau: T0 - 10 * 60_000, act: T0 + 3 * 60_000, now: T0 });
    // A later hook restamps last_activity, and a later push arrives; nothing moves.
    sync(q, [
      crewOnly('a', [
        boat('act', 'needs_you', { last_activity: at(8) }),
        boat('lau', 'needs_you', { launched: at(-10), last_activity: at(9) }),
        boat('now', 'needs_you', { launched: undefined, last_activity: at(9) } as Partial<WireCrew>),
      ]),
    ], T0 + 20 * 60_000);
    expect(since()).toEqual({ lau: T0 - 10 * 60_000, act: T0 + 3 * 60_000, now: T0 });
  });

  it('a boat that waits again after it left is a new stop with a new since', () => {
    const q = createQueue();
    sync(q, [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(2) })])]);
    sync(q, [ship('a', 'underway', undefined, '', [boat('api', 'underway')])], T0 + 1000);
    q.tick(T0 + 1000 + HOLD_MS);
    sync(q, [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(20) })])], T0 + 9000);
    expect(q.entries()[0]).toMatchObject({ key: 'stop:a/api', folded: null, since: T0 + 20 * 60_000 });
  });

  it('crew stops of one ship, and the ship stop on a tie, keep a stable order whatever the wire order', () => {
    const same = at(4);
    const boats = [boat('b', 'needs_you', { last_activity: same }), boat('a', 'needs_you', { last_activity: same })];
    const q1 = createQueue();
    sync(q1, [ship('s', 'needs_you', 4, 'permission:Bash', boats)]);
    const q2 = createQueue();
    sync(q2, [ship('s', 'needs_you', 4, 'permission:Bash', [...boats].reverse())]);
    expect(keys(q1)).toEqual(['stop:s', 'stop:s/a', 'stop:s/b']);
    expect(keys(q2)).toEqual(keys(q1));
  });

  it('crew stops count and pulse like ship stops', () => {
    const q = createQueue();
    q.sync({ items: [item('z', '1', 9)], ships: [crewOnly('a', [boat('api', 'needs_you', { last_activity: at(2) }), boat('db', 'needs_you', { last_activity: at(3) })])] }, T0);
    expect(q.count()).toBe(3);
    expect(q.pulsing()).toBe(true);
    q.fold('stop:a/api', 'Back underway', T0);
    q.fold('stop:a/db', 'Back underway', T0);
    expect(q.count()).toBe(1);
    expect(q.pulsing()).toBe(false);
  });
});

describe('reasonWords', () => {
  it.each([
    ['permission:Bash', 'Wants to run Bash'],
    ['permission:Edit', 'Wants to use Edit'],
    ['question', 'Asked a question'],
    ['stop', 'Finished and is waiting'],
    ['bell', 'Rang the bell'],
    ['idle', 'Has gone quiet'],
    ['notification:permission_prompt', 'Needs a permission answer'],
    ['notification:idle_prompt', 'Sent a notification'],
  ])('%s -> %s', (code, words) => {
    expect(reasonWords(code)).toBe(words);
  });

  it('falls back to the code itself', () => {
    expect(reasonWords('unknown')).toBe('unknown');
    expect(reasonWords('whatever:else')).toBe('whatever:else');
  });
});

describe('waited', () => {
  it.each([
    [0, 'under a minute'],
    [59_000, 'under a minute'],
    [60_000, '1 min'],
    [12 * 60_000, '12 min'],
    [60 * 60_000, '1 h 0 min'],
    [135 * 60_000, '2 h 15 min'],
  ])('%i ms -> %s', (ms, text) => {
    expect(waited(at(0), T0 + ms)).toBe(text);
  });

  it('a start time in the future or unreadable reads as under a minute', () => {
    expect(waited(at(5), T0)).toBe('under a minute');
    expect(waited('not a time', T0)).toBe('under a minute');
  });
});

describe('safeHref (item files are written by ships and untrusted)', () => {
  it('allows https and http', () => {
    expect(safeHref('https://github.com/o/r/pull/1')).toBe('https://github.com/o/r/pull/1');
    expect(safeHref('http://example.com/x')).toBe('http://example.com/x');
  });

  it.each(['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,<script>1</script>', 'file:///etc/passwd', '//evil.example/x', 'not a url', ''])(
    'refuses %j',
    (url) => {
      expect(safeHref(url)).toBeUndefined();
    },
  );

  it('refuses a missing value', () => {
    expect(safeHref(undefined)).toBeUndefined();
  });
});
