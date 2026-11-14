import { describe, expect, it } from 'vitest';
import type { CrewBoat, Ship } from './api';
import { attentionCount, createInboxQueue, HOLD_MS, type InboxItem } from './inbox';

const T0 = Date.parse('2026-10-22T10:00:00Z');
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();

const crew = (name: string, status: string, extra: Partial<CrewBoat> = {}): CrewBoat => ({
  name,
  conversation: true,
  harness: 'claude',
  status,
  exit_code: null,
  exit_signal: null,
  launched: at(0),
  ...extra,
});

const ship = (session: string, fleet: string, status: Ship['status'], extra: Partial<Ship> = {}): Ship => ({
  session,
  name: session.toUpperCase(),
  fleet,
  fleet_name: fleet.toUpperCase(),
  fleet_color: `#${fleet}`,
  harness: 'claude',
  status,
  order_id: '',
  title: '',
  tail: '',
  launched: at(0),
  crew_live: 0,
  dir: '',
  repo: '',
  exit_code: null,
  exit_signal: null,
  crew: [],
  ...extra,
});

const item = (session: string, id: string, created: string, extra: Partial<InboxItem> = {}): InboxItem => ({
  version: 1,
  id,
  session,
  kind: 'review',
  created,
  title: id,
  ...extra,
});

const keys = (q: ReturnType<typeof createInboxQueue>) => q.entries().map((e) => e.key);

describe('createInboxQueue', () => {
  it('lists items and stops from two fleets, each tagged with its fleet', () => {
    const q = createInboxQueue();
    q.sync(
      {
        items: [
          item('a', 'q-1', at(1), { kind: 'question', fleet: 'alpha', fleet_name: 'Alpha', fleet_color: '#alpha' }),
          item('b', 'r-1', at(2), { fleet: 'bravo', fleet_name: 'Bravo', fleet_color: '#bravo' }),
        ],
        ships: [
          ship('a', 'alpha', 'needs_you', { stop_since: at(3) }),
          ship('b', 'bravo', 'needs_you', { reason: 'crew', crew: [crew('Mate', 'needs_you', { last_activity: at(4) })] }),
        ],
      },
      T0,
    );
    expect(q.entries().map((e) => [e.key, e.fleet])).toEqual([
      ['item:a/q-1', { key: 'alpha', name: 'Alpha', color: '#alpha' }],
      ['item:b/r-1', { key: 'bravo', name: 'Bravo', color: '#bravo' }],
      ['stop:a', { key: 'alpha', name: 'ALPHA', color: '#alpha' }],
      ['stop:b/Mate', { key: 'bravo', name: 'BRAVO', color: '#bravo' }],
    ]);
  });

  it('leaves an item fleet name empty when the item has none', () => {
    const q = createInboxQueue();
    q.sync({ items: [item('a', 'r-1', at(1), { fleet: 'alpha', fleet_color: '#alpha' })], ships: [] }, T0);
    expect(q.entries()[0].fleet).toEqual({ key: 'alpha', name: '', color: '#alpha' });
  });

  it('lists a notice with no fleet', () => {
    const q = createInboxQueue();
    q.sync({ items: [item('admiral', 'n-1', at(1), { kind: 'notice', notice: { body: 'Hi' } })], ships: [] }, T0);
    expect(q.entries().map((e) => [e.key, e.fleet])).toEqual([['item:admiral/n-1', undefined]]);
  });

  it('lists only the boat of a ship whose wire reason is crew', () => {
    const q = createInboxQueue();
    q.sync(
      { items: [], ships: [ship('a', 'alpha', 'needs_you', { reason: 'crew', crew: [crew('Mate', 'needs_you'), crew('Cook', 'underway')] })] },
      T0,
    );
    expect(keys(q)).toEqual(['stop:a/Mate']);
  });

  it('lists the ship and its boat when the ship needs you for another reason', () => {
    const q = createInboxQueue();
    q.sync(
      { items: [], ships: [ship('a', 'alpha', 'needs_you', { reason: 'question', stop_since: at(1), crew: [crew('Mate', 'needs_you', { last_activity: at(2) })] })] },
      T0,
    );
    expect(keys(q)).toEqual(['stop:a', 'stop:a/Mate']);
  });

  it('orders by since, items before stops on a tie, then session and id', () => {
    const q = createInboxQueue();
    q.sync(
      {
        items: [item('b', 'r-2', at(5)), item('a', 'r-9', at(5)), item('c', 'r-0', at(9))],
        ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(5) }), ship('z', 'alpha', 'needs_you', { stop_since: at(1) })],
      },
      T0,
    );
    expect(keys(q)).toEqual(['stop:z', 'item:a/r-9', 'item:b/r-2', 'stop:a', 'item:c/r-0']);
  });

  it('takes a ship stop since from stop_since, else from first sight', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(7) }), ship('b', 'alpha', 'needs_you')] }, T0);
    expect(q.entries().map((e) => [e.key, e.since])).toEqual([
      ['stop:b', T0],
      ['stop:a', Date.parse(at(7))],
    ]);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(7) }), ship('b', 'alpha', 'needs_you')] }, T0 + 60_000);
    expect(q.entries().find((e) => e.key === 'stop:b')!.since).toBe(T0);
  });

  it('takes a crew stop since from last_activity, else launched, else first sight', () => {
    const q = createInboxQueue();
    const s = ship('a', 'alpha', 'needs_you', {
      reason: 'crew',
      crew: [
        crew('Active', 'needs_you', { launched: at(1), last_activity: at(2) }),
        crew('Launched', 'needs_you', { launched: at(3) }),
        crew('Unknown', 'needs_you', { launched: '' }),
      ],
    });
    q.sync({ items: [], ships: [s] }, T0 + 60 * 60_000);
    expect(Object.fromEntries(q.entries().map((e) => [e.id, e.since]))).toEqual({
      Unknown: T0 + 60 * 60_000,
      Active: Date.parse(at(2)),
      Launched: Date.parse(at(3)),
    });
  });

  it('never lists the crew of a ship that is sailing off', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'sailing_off', { crew: [crew('Mate', 'needs_you')] })] }, T0);
    expect(keys(q)).toEqual([]);
  });

  it('keeps an answered item out while a stale push still serves it, and lets it back once the server stops', () => {
    const q = createInboxQueue();
    const it = item('a', 'q-1', at(1), { kind: 'question' });
    q.sync({ items: [it], ships: [] }, T0);
    q.fold('item:a/q-1', 'Answered', T0);
    expect(q.entries().map((e) => [e.key, e.folded])).toEqual([['item:a/q-1', 'Answered']]);

    q.sync({ items: [it], ships: [] }, T0 + 1000);
    q.tick(T0 + HOLD_MS);
    q.sync({ items: [it], ships: [] }, T0 + HOLD_MS + 1);
    expect(keys(q)).toEqual([]);

    q.sync({ items: [], ships: [] }, T0 + HOLD_MS + 2);
    q.sync({ items: [it], ships: [] }, T0 + HOLD_MS + 3);
    expect(keys(q)).toEqual(['item:a/q-1']);
  });

  it('folds a stop that goes back underway, holds it for 5 s, then drops it', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })] }, T0);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'underway')] }, T0 + 1000);
    expect(q.entries().map((e) => [e.key, e.folded])).toEqual([['stop:a', 'Back underway']]);

    expect(q.tick(T0 + 1000 + HOLD_MS - 1)).toBe(false);
    expect(keys(q)).toEqual(['stop:a']);
    expect(q.tick(T0 + 1000 + HOLD_MS)).toBe(true);
    expect(keys(q)).toEqual([]);
  });

  it('folds a ship stop when the ship turns to needs_you for its crew', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })] }, T0);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { reason: 'crew', crew: [crew('Mate', 'needs_you', { launched: at(2) })] })] }, T0 + 1000);
    expect(q.entries().map((e) => [e.key, e.folded])).toEqual([
      ['stop:a', 'Back underway'],
      ['stop:a/Mate', null],
    ]);
  });

  it('folds a crew stop when the boat goes underway or idle', () => {
    for (const back of ['underway', 'idle']) {
      const q = createInboxQueue();
      q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { reason: 'crew', crew: [crew('Mate', 'needs_you')] })] }, T0);
      q.sync({ items: [], ships: [ship('a', 'alpha', 'underway', { crew: [crew('Mate', back)] })] }, T0 + 1000);
      expect(q.entries().map((e) => [e.key, e.folded]), back).toEqual([['stop:a/Mate', 'Back underway']]);
    }
  });

  it('leaves out a stop the engineer has seen, without a fold, and lists the next stop', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })] }, T0);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1), seen: true })] }, T0 + 1000);
    expect(keys(q)).toEqual([]);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(3) })] }, T0 + 2000);
    expect(q.entries().map((e) => [e.key, e.folded])).toEqual([['stop:a', null]]);
  });

  it('drops a stop silently when its ship leaves any other way', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) }), ship('b', 'alpha', 'needs_you', { stop_since: at(2) })] }, T0);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'sailing_off')] }, T0 + 1000);
    expect(keys(q)).toEqual([]);
  });

  it('holds a folded stop from a stale push until its hold is up, and lists it again if it asks again', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })] }, T0);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'underway')] }, T0 + 1000);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(2) })] }, T0 + 2000);
    expect(q.entries().map((e) => [e.key, e.folded])).toEqual([['stop:a', 'Back underway']]);
    q.tick(T0 + 1000 + HOLD_MS);
    expect(q.entries().map((e) => [e.key, e.folded])).toEqual([['stop:a', null]]);
  });
});

describe('createInboxQueue: ready to merge', () => {
  const layer = (n: number) => ({ url: `https://github.com/acme/api/pull/${n}`, state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, ready: true });
  const ready = (session: string, extra: Partial<Ship> = {}) => ship(session, 'alpha', 'moored', { prs: [layer(7)], stack_ready: true, ...extra });
  const notReady = (session: string) => ship(session, 'alpha', 'moored', { prs: [{ ...layer(7), review: '', ready: false }] });
  const rows = (q: ReturnType<typeof createInboxQueue>) => q.entries().map((e) => [e.key, e.type, e.folded]);

  it('lists a moored ship whose stack is ready as a merge entry tagged with its fleet', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a')] }, T0);
    expect(q.entries().map((e) => [e.key, e.type, e.session, e.fleet])).toEqual([['merge:a', 'merge', 'a', { key: 'alpha', name: 'ALPHA', color: '#alpha' }]]);
  });

  it('lists a ship whose stack is ready, however many layers', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a', { prs: [layer(7), layer(8)] })] }, T0);
    expect(keys(q)).toEqual(['merge:a']);
  });

  it('lists only the moored ship that is ready, not a not-ready moored ship or a live one', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a'), notReady('b'), ship('c', 'alpha', 'underway', { stack_ready: true })] }, T0);
    expect(keys(q)).toEqual(['merge:a']);
  });

  it('takes its start from first sight and keeps it across later pushes', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a')] }, T0);
    q.sync({ items: [], ships: [ready('a')] }, T0 + 60_000);
    expect(q.entries().map((e) => e.since)).toEqual([T0]);
  });

  it('sits in time order beside a needs-you stop', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a'), ship('b', 'alpha', 'needs_you', { stop_since: at(-5) })] }, T0);
    expect(keys(q)).toEqual(['stop:b', 'merge:a']);
  });

  it('orders a merge and a stop that began together the same way whatever the input order', () => {
    for (const ships of [[ready('a'), ship('b', 'alpha', 'needs_you', { stop_since: at(0) })], [ship('b', 'alpha', 'needs_you', { stop_since: at(0) }), ready('a')]]) {
      const q = createInboxQueue();
      q.sync({ items: [], ships }, T0);
      expect(keys(q)).toEqual(['stop:b', 'merge:a']);
    }
  });

  it('goes by itself, with no fold, when the ship stops being ready or leaves the list', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a'), ready('b')] }, T0);
    expect(keys(q)).toEqual(['merge:a', 'merge:b']);
    q.sync({ items: [], ships: [notReady('a')] }, T0 + 1000);
    expect(rows(q)).toEqual([]);
  });

  it('folds with the given text, holds it for 5 s, then drops it', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a')] }, T0);
    q.fold('merge:a', 'Merged', T0 + 1000);
    expect(rows(q)).toEqual([['merge:a', 'merge', 'Merged']]);
    expect(q.tick(T0 + 1000 + HOLD_MS - 1)).toBe(false);
    expect(q.tick(T0 + 1000 + HOLD_MS)).toBe(true);
    expect(keys(q)).toEqual([]);
  });

  it('keeps a merged entry out while a stale push still serves the ship ready, and lets it back once the server stops and serves it ready again', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a')] }, T0);
    q.fold('merge:a', 'Merged', T0);
    q.tick(T0 + HOLD_MS);
    q.sync({ items: [], ships: [ready('a')] }, T0 + HOLD_MS + 1);
    expect(keys(q)).toEqual([]);

    q.sync({ items: [], ships: [notReady('a')] }, T0 + HOLD_MS + 2);
    q.sync({ items: [], ships: [ready('a')] }, T0 + HOLD_MS + 3);
    expect(keys(q)).toEqual(['merge:a']);
  });

  it('shows one folded line, not a second row, when a push serves the ship ready during the hold', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ready('a')] }, T0);
    q.fold('merge:a', 'Merged', T0);
    q.sync({ items: [], ships: [ready('a')] }, T0 + 1000);
    expect(rows(q)).toEqual([['merge:a', 'merge', 'Merged']]);
  });
});

describe('createInboxQueue: re-review', () => {
  const layer = (n: number, over: Record<string, unknown> = {}) => ({ url: `https://github.com/acme/api/pull/${n}`, state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, ready: true, reviewing: true, author: 'dana', ...over });
  const responded = (session: string, status: Ship['status'] = 'moored', extra: Partial<Ship> = {}) => ship(session, 'alpha', status, { prs: [layer(7, { responded: true })], ...extra });
  const rows = (q: ReturnType<typeof createInboxQueue>) => q.entries().map((e) => [e.key, e.type, e.folded]);

  it('lists a moored ship whose reviewed PR the author responded on, tagged with its fleet', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    expect(q.entries().map((e) => [e.key, e.type, e.session, e.fleet])).toEqual([['rereview:a', 'rereview', 'a', { key: 'alpha', name: 'ALPHA', color: '#alpha' }]]);
  });

  it('lists one entry per ship, however many layers responded', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'moored', { prs: [layer(7, { responded: true }), layer(8, { responded: true })] })] }, T0);
    expect(keys(q)).toEqual(['rereview:a']);
  });

  it('lists nothing for a live ship, an own PR, or a reviewed PR the author has not responded on', () => {
    const q = createInboxQueue();
    q.sync(
      {
        items: [],
        ships: [
          responded('live', 'underway'),
          ship('own', 'alpha', 'moored', { prs: [layer(7, { responded: true, reviewing: false })] }),
          ship('quiet', 'alpha', 'moored', { prs: [layer(7)] }),
        ],
      },
      T0,
    );
    expect(keys(q)).toEqual([]);
  });

  it('raises no merge entry for a reviewing ship the server serves ready to merge', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'moored', { prs: [layer(7)], stack_ready: true }), responded('b', 'moored', { stack_ready: true })] }, T0);
    expect(keys(q)).toEqual(['rereview:b']);
  });

  it('still raises a merge entry for a ship\'s own ready stack', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'moored', { prs: [layer(7, { reviewing: false })], stack_ready: true })] }, T0);
    expect(keys(q)).toEqual(['merge:a']);
  });

  it('takes its start from first sight and keeps it across later pushes', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    q.sync({ items: [], ships: [responded('a')] }, T0 + 60_000);
    expect(q.entries().map((e) => e.since)).toEqual([T0]);
  });

  it('sits after a merge entry that began at the same time, whatever the input order', () => {
    for (const ships of [
      [responded('a'), ship('b', 'alpha', 'moored', { prs: [layer(8, { reviewing: false })], stack_ready: true })],
      [ship('b', 'alpha', 'moored', { prs: [layer(8, { reviewing: false })], stack_ready: true }), responded('a')],
    ]) {
      const q = createInboxQueue();
      q.sync({ items: [], ships }, T0);
      expect(keys(q)).toEqual(['merge:b', 'rereview:a']);
    }
  });

  it('goes by itself, with no fold, when the author has not responded any more, the ship wakes or the ship leaves', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a'), responded('b'), responded('c')] }, T0);
    expect(keys(q)).toEqual(['rereview:a', 'rereview:b', 'rereview:c']);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'moored', { prs: [layer(7)] }), responded('b', 'underway')] }, T0 + 1000);
    expect(rows(q)).toEqual([]);
  });

  it('counts in the attention count until it is folded', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    expect(attentionCount(q)).toBe(1);
    q.fold('rereview:a', 'Re-reviewing', T0 + 1000);
    expect(attentionCount(q)).toBe(0);
  });

  it('folds with the given text, holds it for 5 s, then drops it', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    q.fold('rereview:a', 'Re-reviewing', T0 + 1000);
    expect(rows(q)).toEqual([['rereview:a', 'rereview', 'Re-reviewing']]);
    expect(q.tick(T0 + 1000 + HOLD_MS - 1)).toBe(false);
    expect(q.tick(T0 + 1000 + HOLD_MS)).toBe(true);
    expect(rows(q)).toEqual([]);
  });

  it('keeps a folded entry out while a stale push still serves the ship moored and responded', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    expect(keys(q)).toEqual(['rereview:a']);
    q.fold('rereview:a', 'Re-reviewing', T0);
    q.tick(T0 + HOLD_MS);
    q.sync({ items: [], ships: [responded('a')] }, T0 + HOLD_MS + 1000);
    expect(keys(q)).toEqual([]);
  });

  it('lists it again when the ship moors again with the author responded, even after a stale push in between', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    q.fold('rereview:a', 'Re-reviewing', T0);
    q.tick(T0 + HOLD_MS);
    // The woken ship keeps `responded` until a poll shows its new review.
    q.sync({ items: [], ships: [responded('a', 'underway')] }, T0 + HOLD_MS + 1000);
    q.sync({ items: [], ships: [responded('a')] }, T0 + HOLD_MS + 2000);
    expect(keys(q)).toEqual(['rereview:a']);
  });

  it('lists it again once the author has not responded any more and then does', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [responded('a')] }, T0);
    q.fold('rereview:a', 'Re-reviewing', T0);
    q.tick(T0 + HOLD_MS);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'moored', { prs: [layer(7)] })] }, T0 + HOLD_MS + 1000);
    q.sync({ items: [], ships: [responded('a')] }, T0 + HOLD_MS + 2000);
    expect(keys(q)).toEqual(['rereview:a']);
  });
});
