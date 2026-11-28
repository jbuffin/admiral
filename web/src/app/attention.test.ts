// Where the served attention count shows (title, favicon, app badge) and the shared Inbox queue the watcher keeps.
import { afterEach, describe, expect, it } from 'vitest';
import type { Ship } from './api';
import { readFileSync } from 'node:fs';
import { applyAttention, attentionApplier, DOT_ICON, watchAttention, type AttentionTarget } from './attention';
import { inboxItems, servedAttention, ships } from './shipsLink';
import { HOLD_MS, sharedInboxQueue, type InboxItem } from './inbox';

const T0 = Date.parse('2026-10-22T10:00:00Z');
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();

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

describe('the shared Inbox queue', () => {
  it('is one queue for every surface: a fold through one handle shows through another', () => {
    const a = sharedInboxQueue();
    const b = sharedInboxQueue();
    a.sync({ items: [item('a', 'q-1', at(1), { kind: 'question' })], ships: [] }, T0);
    expect(b.entries().map((e) => e.folded)).toEqual([null]);
    b.fold('item:a/q-1', 'Answered', T0);
    expect(a.entries().map((e) => e.folded)).toEqual(['Answered']);
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

describe('attentionApplier', () => {
  it('writes nothing when the count has not changed', () => {
    const icon = { href: '/favicon.svg' };
    let titleWrites = 0;
    let title = 'Admiral';
    const t: AttentionTarget = {
      document: {
        get title() {
          return title;
        },
        set title(v: string) {
          titleWrites++;
          title = v;
        },
        querySelector: () => icon,
      },
      navigator: {},
    };
    const apply = attentionApplier(t);
    apply(2);
    apply(2);
    expect(titleWrites).toBe(1);
    apply(0);
    expect(titleWrites).toBe(2);
  });
});

describe('the dotted favicon', () => {
  it('is the favicon file plus the dot', () => {
    const file = readFileSync(new URL('../../public/favicon.svg', import.meta.url), 'utf8').trim();
    const dotted = decodeURIComponent(DOT_ICON.slice('data:image/svg+xml,'.length));
    expect(dotted.startsWith(file.slice(0, -'</svg>'.length))).toBe(true);
    expect(dotted.slice(file.length - '</svg>'.length)).toMatch(/^<circle [^>]*\/><\/svg>$/);
  });
});

describe('watchAttention shows the served count', () => {
  const watched = () => {
    const icon = { href: 'untouched.svg' };
    const badge: (number | 'clear')[] = [];
    const t: AttentionTarget = {
      document: { title: 'untouched', querySelector: () => icon },
      navigator: { setAppBadge: async (n?: number) => void badge.push(n!), clearAppBadge: async () => void badge.push('clear') },
    };
    return { t, icon, badge };
  };
  let stopWatching: (() => void) | undefined;
  afterEach(() => {
    stopWatching?.();
    stopWatching = undefined;
    servedAttention.value = null;
    ships.value = [];
    inboxItems.value = [];
    const q = sharedInboxQueue();
    q.sync({ items: [], ships: [] }, T0);
    q.tick(T0 + HOLD_MS + 1000);
  });

  it('puts a served count above zero in the title, the favicon dot and the badge, and clears all three at a served 0', () => {
    const { t, icon, badge } = watched();
    stopWatching = watchAttention(t);
    servedAttention.value = 2;
    expect(t.document.title).toBe('(2) Admiral');
    expect(icon.href).toBe(DOT_ICON);
    expect(badge).toEqual([2]);
    servedAttention.value = 0;
    expect(t.document.title).toBe('Admiral');
    expect(icon.href).toBe('/favicon.svg');
    expect(badge).toEqual([2, 'clear']);
  });

  it('leaves the title, favicon and badge as they were while no count has been served, even with ships and items present', () => {
    const { t, icon, badge } = watched();
    ships.value = [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })];
    inboxItems.value = [item('b', 'q-1', at(1), { kind: 'question' })];
    stopWatching = watchAttention(t);
    expect(t.document.title).toBe('untouched');
    expect(icon.href).toBe('untouched.svg');
    expect(badge).toEqual([]);
  });

  it('shows the served count when the served ships and items work out to another number', () => {
    const { t } = watched();
    stopWatching = watchAttention(t);
    ships.value = [ship('a', 'alpha', 'needs_you', { stop_since: at(1) })];
    inboxItems.value = [item('b', 'q-1', at(1), { kind: 'question' }), item('c', 'q-2', at(2), { kind: 'question' })];
    servedAttention.value = 1;
    expect(t.document.title).toBe('(1) Admiral');
    servedAttention.value = 0;
    expect(t.document.title).toBe('Admiral');
  });

  it('keeps the title through an optimistic fold in the Inbox until the server serves the next count', () => {
    const { t, badge } = watched();
    stopWatching = watchAttention(t);
    inboxItems.value = [item('a', 'q-1', at(1), { kind: 'question' }), item('a', 'q-2', at(2), { kind: 'question' })];
    servedAttention.value = 2;
    expect(sharedInboxQueue().entries().length).toBe(2);
    sharedInboxQueue().fold('item:a/q-1', 'Answered', T0);
    expect(t.document.title).toBe('(2) Admiral');
    servedAttention.value = 1;
    expect(t.document.title).toBe('(1) Admiral');
    expect(badge).toEqual([2, 1]);
  });
});
