// The attention count over the Inbox queue, and where it shows: title, favicon, app badge.
import { describe, expect, it } from 'vitest';
import type { CrewBoat, Ship } from './api';
import { applyAttention, type AttentionTarget } from './attention';
import { attentionCount, createInboxQueue, HOLD_MS, sharedInboxQueue, type InboxItem } from './inbox';

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

describe('attentionCount', () => {
  const merge = (session: string) => ship(session, 'alpha', 'moored', { stack_ready: true });
  const chatQuestion = (fleet: string) => item(`_chat-${fleet}`, 'q-chat', at(1), { kind: 'question', fleet });

  it('counts stops, crew stops, merge entries and every item kind but notice, across fleets', () => {
    const q = createInboxQueue();
    q.sync(
      {
        items: [
          item('a', 'q-1', at(1), { kind: 'question', fleet: 'alpha' }),
          item('b', 'r-1', at(2), { fleet: 'bravo' }),
          item('b', 'f-1', at(2), { kind: 'findings', fleet: 'bravo' }),
          item('a', 'w-1', at(2), { kind: 'wake_proposal', fleet: 'alpha' }),
          item('a', 'c-1', at(2), { kind: 'crew_notes', fleet: 'alpha' }),
          item('a', 'c-2', at(2), { kind: 'crew_member', fleet: 'alpha' }),
          chatQuestion('alpha'),
          item('_admiral', 'n-1', at(3), { kind: 'notice' }),
        ],
        ships: [
          ship('s', 'zulu', 'needs_you', { reason: 'question', stop_since: at(1) }),
          ship('t', 'bravo', 'needs_you', { reason: 'crew', crew: [crew('Mate', 'needs_you')] }),
          merge('m'),
        ],
      },
      T0,
    );
    expect(attentionCount(q)).toBe(10);
  });

  it('is zero for an empty queue and for notices alone', () => {
    const q = createInboxQueue();
    expect(attentionCount(q)).toBe(0);
    q.sync({ items: [item('_admiral', 'n-1', at(1), { kind: 'notice' })], ships: [] }, T0);
    expect(attentionCount(q)).toBe(0);
  });

  it('drops at once on fold, before the hold is up', () => {
    const q = createInboxQueue();
    q.sync({ items: [item('a', 'q-1', at(1), { kind: 'question' })], ships: [merge('m')] }, T0);
    expect(attentionCount(q)).toBe(2);
    q.fold('item:a/q-1', 'Answered', T0);
    expect(attentionCount(q)).toBe(1);
    q.fold('merge:m', 'Merged', T0);
    expect(attentionCount(q)).toBe(0);
  });

  it('stays down while a stale push serves an answered item or a merged ship, and counts it again once the server stops and serves it anew', () => {
    const q = createInboxQueue();
    const it = item('a', 'q-1', at(1), { kind: 'question' });
    q.sync({ items: [it], ships: [merge('m')] }, T0);
    q.fold('item:a/q-1', 'Answered', T0);
    q.fold('merge:m', 'Merged', T0);
    q.tick(T0 + HOLD_MS);
    q.sync({ items: [it], ships: [merge('m')] }, T0 + HOLD_MS + 1);
    expect(attentionCount(q)).toBe(0);
    q.sync({ items: [], ships: [] }, T0 + HOLD_MS + 2);
    q.sync({ items: [it], ships: [merge('m')] }, T0 + HOLD_MS + 3);
    expect(attentionCount(q)).toBe(2);
  });

  it('drops when the server stops serving an item', () => {
    const q = createInboxQueue();
    q.sync({ items: [item('a', 'q-1', at(1), { kind: 'question' }), item('a', 'r-1', at(2))], ships: [] }, T0);
    expect(attentionCount(q)).toBe(2);
    q.sync({ items: [item('a', 'r-1', at(2))], ships: [] }, T0 + 1000);
    expect(attentionCount(q)).toBe(1);
  });

  it('drops when a stop goes back underway, without waiting for the slide-out', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) }), ship('b', 'alpha', 'needs_you', { reason: 'crew', crew: [crew('Mate', 'needs_you')] })] }, T0);
    expect(attentionCount(q)).toBe(2);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'underway'), ship('b', 'alpha', 'underway', { crew: [crew('Mate', 'idle')] })] }, T0 + 1000);
    expect(q.entries().map((e) => e.folded)).toEqual(['Back underway', 'Back underway']);
    expect(attentionCount(q)).toBe(0);
  });

  it('counts a stop that asks again during its hold', () => {
    const q = createInboxQueue();
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })] }, T0);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'underway')] }, T0 + 1000);
    q.sync({ items: [], ships: [ship('a', 'alpha', 'needs_you', { stop_since: at(2) })] }, T0 + 2000);
    expect(attentionCount(q)).toBe(0);
    q.tick(T0 + 1000 + HOLD_MS);
    expect(attentionCount(q)).toBe(1);
  });

  it('does not change when a push repeats the same items and ships', () => {
    const q = createInboxQueue();
    const input = { items: [item('a', 'q-1', at(1), { kind: 'question' })], ships: [ship('b', 'alpha', 'needs_you', { stop_since: at(1) })] };
    q.sync(input, T0);
    q.sync(input, T0 + 1000);
    expect(attentionCount(q)).toBe(2);
  });

  it('is one queue for every surface: a fold through one handle drops the count read through another', () => {
    const a = sharedInboxQueue();
    const b = sharedInboxQueue();
    a.sync({ items: [item('a', 'q-1', at(1), { kind: 'question' })], ships: [] }, T0);
    expect(attentionCount(b)).toBe(1);
    b.fold('item:a/q-1', 'Answered', T0);
    expect(attentionCount(a)).toBe(0);
    a.sync({ items: [], ships: [] }, T0 + 1000);
    a.tick(T0 + HOLD_MS + 1000);
  });
});

describe('applyAttention', () => {
  const target = (withBadge: boolean) => {
    const icon = { href: '/favicon.svg' };
    const badge: (number | 'clear')[] = [];
    const t: AttentionTarget = {
      document: { title: 'Admiral', querySelector: () => icon },
      navigator: withBadge ? { setAppBadge: async (n?: number) => void badge.push(n!), clearAppBadge: async () => void badge.push('clear') } : {},
    };
    return { t, icon, badge };
  };

  it('puts the count in the title and a dot on the favicon above zero, and clears both at zero', () => {
    const { t, icon } = target(false);
    applyAttention(2, t);
    expect(t.document.title).toBe('(2) Admiral');
    expect(icon.href).not.toBe('/favicon.svg');
    applyAttention(1, t);
    expect(t.document.title).toBe('(1) Admiral');
    applyAttention(0, t);
    expect(t.document.title).toBe('Admiral');
    expect(icon.href).toBe('/favicon.svg');
  });

  it('sets the app badge to the count and clears it at zero when the browser has the Badging API', () => {
    const { t, badge } = target(true);
    applyAttention(3, t);
    applyAttention(0, t);
    expect(badge).toEqual([3, 'clear']);
  });

  it('skips the badge when the browser has no Badging API', () => {
    const { t } = target(false);
    expect(() => applyAttention(3, t)).not.toThrow();
    expect(t.document.title).toBe('(3) Admiral');
  });
});
