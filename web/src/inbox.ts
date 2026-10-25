import type { WireCrew, WireShip } from './ships';

// The Needs You inbox queue (#116): one list, oldest first, of the served inbox items and the ships that
// need you (needs-you stops), crew boats included. It is pure: time comes in as `now` and nothing here sets a timer or reads a
// signal, so it never imports ships.ts at runtime. inboxState.ts wires it to the signals and the clock.

// An item file as the server serves it (internal/inbox): the file's fields, plus the ship's session and,
// when the ship is known, its name and harness. Item files are written by ships and are untrusted.
export interface InboxItem {
  version: number;
  id: string;
  session: string;
  kind: 'question' | 'review' | 'findings';
  created: string;
  title: string;
  name?: string;
  harness?: string;
  order_url?: string;
  question?: { questions: InboxQuestion[] };
  review?: { pr_url?: string; review_id?: number | string; summary?: string };
  findings?: { pr_url?: string; cards?: unknown };
}

export interface InboxQuestion {
  id: string;
  text: string;
  options: { label: string; description?: string }[];
  recommended?: string;
  multi?: boolean;
}

export interface Entry {
  key: string;
  type: 'item' | 'stop';
  session: string;
  id?: string;
  item?: InboxItem;
  ship?: WireShip;
  /** Set on a crew stop only: the crew boat that needs you. `ship` is its lead. */
  crew?: WireCrew;
  /** The folded line's text while the entry is held after an action, else null. */
  folded: string | null;
  since: number;
}

// How long a folded entry stays before it slides out (tokens.json inboxConfirmHold).
export const HOLD_MS = 5000;

const itemKey = (session: string, id: string) => `item:${session}/${id}`;
const stopKey = (session: string) => `stop:${session}`;
const crewKey = (session: string, crew: string) => `stop:${session}/${crew}`;

// The wire says needs_you/crew while only a crew boat waits, so the ship itself needs you on any other reason.
const needsYou = (s: WireShip) => s.status === 'needs_you' && s.reason !== 'crew';
// A sailing-off ship's session is about to die with its crew, so none of them waits on you.
const waiting = (s: WireShip) => (s.status === 'sailing_off' ? [] : (s.crew ?? []).filter((c) => c.status === 'needs_you'));

const compare = (a: Entry, b: Entry) =>
  a.since - b.since ||
  (a.type === b.type ? 0 : a.type === 'item' ? -1 : 1) ||
  (a.session < b.session ? -1 : a.session > b.session ? 1 : 0) ||
  ((a.id ?? '') < (b.id ?? '') ? -1 : (a.id ?? '') > (b.id ?? '') ? 1 : 0);

interface Input {
  items: InboxItem[];
  ships: WireShip[];
}

export function createQueue() {
  let input: Input = { items: [], ships: [] };
  let served: Entry[] = [];
  const held = new Map<string, { entry: Entry; text: string; until: number }>();
  // Answered items a stale push may still serve: they stay out until the server stops serving them.
  const answered = new Set<string>();
  const stopFirstSeen = new Map<string, number>();
  // A crew stop's start, kept from first sight: the server keeps none for a crew boat.
  const crewSince = new Map<string, number>();
  // Entries whose action is in flight: they leave the count at the tap and come back if it fails.
  const pending = new Set<string>();
  let cursor: string | null = null;

  const merge = (): Entry[] => {
    const out: Entry[] = [];
    for (const it of input.items) {
      const key = itemKey(it.session, it.id);
      if (answered.has(key) || held.has(key)) continue;
      out.push({ key, type: 'item', session: it.session, id: it.id, item: it, folded: null, since: Date.parse(it.created) || 0 });
    }
    for (const s of input.ships) {
      const key = stopKey(s.session);
      if (needsYou(s) && !held.has(key)) {
        out.push({ key, type: 'stop', session: s.session, ship: s, folded: null, since: Date.parse(s.stop_since ?? '') || (stopFirstSeen.get(key) ?? 0) });
      }
      for (const c of waiting(s)) {
        const ck = crewKey(s.session, c.name);
        if (held.has(ck)) continue;
        out.push({ key: ck, type: 'stop', session: s.session, id: c.name, ship: s, crew: c, folded: null, since: crewSince.get(ck) ?? 0 });
      }
    }
    return out;
  };

  const entries = (): Entry[] =>
    [...served, ...[...held.values()].map((h) => ({ ...h.entry, folded: h.text }))].sort(compare);

  // The cursor stays on its entry; when that has gone, it goes to the next survivor of the old list, else the previous one.
  const settle = (before: Entry[]) => {
    const now = entries();
    const keys = new Set(now.map((e) => e.key));
    if (cursor && keys.has(cursor)) return;
    if (cursor) {
      const i = before.findIndex((e) => e.key === cursor);
      const next = before.slice(i + 1).find((e) => keys.has(e.key)) ?? before.slice(0, Math.max(i, 0)).reverse().find((e) => keys.has(e.key));
      cursor = next?.key ?? null;
    }
    if (!cursor) cursor = now[0]?.key ?? null;
  };

  return {
    sync(next: Input, now: number) {
      const before = entries();
      const open = new Set(next.items.map((it) => itemKey(it.session, it.id)));
      for (const key of answered) if (!open.has(key)) answered.delete(key);
      const ships = new Map(next.ships.map((s) => [s.session, s]));
      // A needs-you stop whose ship, or crew boat, went back to underway folds; any other way out is silent.
      // A ship that needed you itself folds when the wire turns to underway or to needs_you/crew.
      for (const e of served) {
        if (e.type !== 'stop' || held.has(e.key)) continue;
        const ship = ships.get(e.session);
        const sailing = ship?.status === 'underway' || ship?.status === 'needs_you';
        const back = e.crew ? sailing && ship!.crew?.find((c) => c.name === e.crew!.name)?.status === 'underway' : sailing && !needsYou(ship!);
        if (back) held.set(e.key, { entry: e, text: 'Back underway', until: now + HOLD_MS });
      }
      const live = new Set<string>();
      for (const s of next.ships) {
        const key = stopKey(s.session);
        if (needsYou(s) && !stopFirstSeen.has(key)) stopFirstSeen.set(key, now);
        if (!needsYou(s)) stopFirstSeen.delete(key);
        for (const c of waiting(s)) {
          const ck = crewKey(s.session, c.name);
          live.add(ck);
          if (!crewSince.has(ck)) crewSince.set(ck, Date.parse(c.last_activity ?? '') || Date.parse(c.launched) || now);
        }
      }
      for (const ck of crewSince.keys()) if (!live.has(ck)) crewSince.delete(ck);
      input = next;
      served = merge();
      settle(before);
    },

    fold(key: string, text: string, now: number) {
      const entry = entries().find((e) => e.key === key);
      if (!entry || held.has(key)) return;
      if (entry.type === 'item') answered.add(key);
      held.set(key, { entry: { ...entry, folded: null }, text, until: now + HOLD_MS });
      served = merge();
    },

    // Removes the entries whose hold has run out. True when anything went.
    tick(now: number): boolean {
      const due = [...held].filter(([, h]) => h.until <= now);
      if (due.length === 0) return false;
      const before = entries();
      for (const [key] of due) held.delete(key);
      served = merge(); // a ship that asked again during its hold is back
      settle(before);
      return true;
    },

    entries,
    count: () => served.filter((e) => !pending.has(e.key)).length,
    setPending(key: string, on: boolean) {
      if (on) pending.add(key);
      else pending.delete(key);
    },
    position(): { n: number; total: number } | null {
      const all = entries();
      if (all.length === 0) return null;
      return { n: Math.max(0, all.findIndex((e) => e.key === cursor)) + 1, total: all.length };
    },
    currentKey: () => cursor,
    move(by: 1 | -1) {
      const all = entries();
      const i = all.findIndex((e) => e.key === cursor);
      const to = all[Math.min(all.length - 1, Math.max(0, i + by))];
      if (to) cursor = to.key;
    },
    select(key: string) {
      if (entries().some((e) => e.key === key)) cursor = key;
    },
    pulsing: () => served.some((e) => e.type === 'stop'),
  };
}

export type Queue = ReturnType<typeof createQueue>;

// What a ship's reason code says in plain words. The code itself is shown beside it.
export function reasonWords(code: string): string {
  if (code.startsWith('permission:')) {
    const tool = code.slice('permission:'.length);
    return `${tool === 'Bash' ? 'Wants to run' : 'Wants to use'} ${tool}`;
  }
  if (code === 'notification:permission_prompt') return 'Needs a permission answer';
  if (code.startsWith('notification:')) return 'Sent a notification';
  return (
    { question: 'Asked a question', stop: 'Finished and is waiting', bell: 'Rang the bell', idle: 'Has gone quiet' }[code as 'stop'] ?? code
  );
}

// How long ago: "under a minute", "12 min", "2 h 15 min".
export function waited(sinceIso: string, nowMs: number): string {
  const ms = nowMs - Date.parse(sinceIso);
  if (!(ms >= 60_000)) return 'under a minute';
  const min = Math.floor(ms / 60_000);
  return min < 60 ? `${min} min` : `${Math.floor(min / 60)} h ${min % 60} min`;
}

// A link from an item file: only http and https addresses become links.
export function safeHref(url: string | undefined): string | undefined {
  if (!url) return undefined;
  try {
    const { protocol } = new URL(url);
    return protocol === 'https:' || protocol === 'http:' ? url : undefined;
  } catch {
    return undefined;
  }
}

// A PR address from an item file: only https addresses count. `label` is "owner/repo#n" for a github.com PR
// and the address itself otherwise; `files` is the PR's changed-files page.
export function prLink(url: string | undefined): { href: string; host: string; label: string; files: string } | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    if (u.protocol !== 'https:') return undefined;
    const m = /^\/([^/]+)\/([^/]+)\/pull\/(\d+)\/?$/.exec(u.pathname);
    return {
      href: url,
      host: u.host,
      label: u.host === 'github.com' && m ? `${m[1]}/${m[2]}#${m[3]}` : url,
      files: `${u.origin}${u.pathname.replace(/\/+$/, '')}/files`,
    };
  } catch {
    return undefined;
  }
}
