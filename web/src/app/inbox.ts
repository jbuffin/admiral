import type { CrewBoat, Ship } from './api';

// The Inbox queue of the new shell: items and needs-you stops across every fleet. It is pure: time comes in
// as `now` and nothing here sets a timer or reads a signal.

// The server's item file (internal/inbox). Item files are written by ships and are untrusted.
export interface InboxItem {
  version: number;
  id: string;
  session: string;
  kind: 'question' | 'review' | 'findings' | 'notice' | 'wake_proposal' | 'routine_proposal' | 'crew_notes' | 'crew_member';
  created: string;
  title: string;
  name?: string;
  harness?: string;
  fleet?: string;
  fleet_name?: string;
  fleet_color?: string;
  order_url?: string;
  question?: { questions: InboxQuestion[] };
  review?: { pr_url?: string; review_id?: number | string; summary?: string };
  findings?: { pr_url?: string; cards?: unknown };
  notice?: { body?: string };
  routine_proposal?: { fleet?: string; routine?: string; routine_name?: string; trigger?: string; action?: string; expires_at?: string };
  crew_notes?: unknown;
  crew_member?: unknown;
}

export interface InboxQuestion {
  id: string;
  text: string;
  options: { label: string; description?: string }[];
  recommended?: string;
  multi?: boolean;
}

export interface EntryFleet {
  key: string;
  name: string;
  color: string;
}

export interface Entry {
  key: string;
  type: 'item' | 'stop' | 'merge';
  session: string;
  id?: string;
  item?: InboxItem;
  ship?: Ship;
  /** Set on a crew stop only: the crew boat that needs you. `ship` is its lead. */
  crew?: CrewBoat;
  /** The folded line's text while the entry is held after an action, else null. */
  folded: string | null;
  since: number;
  /** Absent on a notice, which belongs to no fleet. */
  fleet?: EntryFleet;
}

// How long a folded entry stays before it slides out (tokens.json inboxConfirmHold).
export const HOLD_MS = 5000;

export const itemKey = (session: string, id: string) => `item:${session}/${id}`;
export const stopKey = (session: string) => `stop:${session}`;
export const mergeKey = (session: string) => `merge:${session}`;
export const crewKey = (session: string, crew: string) => `stop:${session}/${crew}`;

// The wire says needs_you/crew while only a crew boat waits, so the ship itself needs you on any other reason.
const needsYou = (s: Ship) => s.status === 'needs_you' && s.reason !== 'crew';
// A crew boat that no longer asks: an answered boat whose next turn ended between two pushes goes straight to idle.
const isBack = (c?: CrewBoat) => c?.status === 'underway' || c?.status === 'idle';
// A sailing-off ship's session is about to die with its crew, so none of them waits on you.
const waiting = (s: Ship) => (s.status === 'sailing_off' ? [] : (s.crew ?? []).filter((c) => c.status === 'needs_you'));

// A moored ship whose whole stack can merge now.
const readyToMerge = (s: Ship) => s.status === 'moored' && s.stack_ready === true;

const shipFleet = (s: Ship): EntryFleet => ({ key: s.fleet, name: s.fleet_name ?? '', color: s.fleet_color });
const itemFleet = (it: InboxItem): EntryFleet | undefined =>
  it.fleet === undefined ? undefined : { key: it.fleet, name: it.fleet_name ?? '', color: it.fleet_color ?? '' };

const RANK: Record<Entry['type'], number> = { item: 0, stop: 1, merge: 2 };

const compare = (a: Entry, b: Entry) =>
  a.since - b.since ||
  RANK[a.type] - RANK[b.type] ||
  (a.session < b.session ? -1 : a.session > b.session ? 1 : 0) ||
  ((a.id ?? '') < (b.id ?? '') ? -1 : (a.id ?? '') > (b.id ?? '') ? 1 : 0);

interface Input {
  items: InboxItem[];
  ships: Ship[];
}

export function createInboxQueue() {
  let input: Input = { items: [], ships: [] };
  let served: Entry[] = [];
  const held = new Map<string, { entry: Entry; text: string; until: number }>();
  // Answered items a stale push may still serve: they stay out until the server stops serving them.
  const answered = new Set<string>();
  // Merged ships a stale push may still serve ready: they stay out until the server stops serving them.
  const merged = new Set<string>();
  const stopFirstSeen = new Map<string, number>();
  const mergeFirstSeen = new Map<string, number>();
  // A crew stop's start, kept from first sight: the server keeps none for a crew boat.
  const crewSince = new Map<string, number>();

  const merge = (): Entry[] => {
    const out: Entry[] = [];
    for (const it of input.items) {
      const key = itemKey(it.session, it.id);
      if (answered.has(key) || held.has(key)) continue;
      out.push({ key, type: 'item', session: it.session, id: it.id, item: it, folded: null, since: Date.parse(it.created) || 0, fleet: itemFleet(it) });
    }
    for (const s of input.ships) {
      const key = stopKey(s.session);
      if (needsYou(s) && !held.has(key)) {
        out.push({ key, type: 'stop', session: s.session, ship: s, folded: null, since: Date.parse(s.stop_since ?? '') || (stopFirstSeen.get(key) ?? 0), fleet: shipFleet(s) });
      }
      const mk = mergeKey(s.session);
      if (readyToMerge(s) && !merged.has(mk) && !held.has(mk)) {
        out.push({ key: mk, type: 'merge', session: s.session, ship: s, folded: null, since: mergeFirstSeen.get(mk) ?? 0, fleet: shipFleet(s) });
      }
      for (const c of waiting(s)) {
        const ck = crewKey(s.session, c.name);
        if (held.has(ck)) continue;
        out.push({ key: ck, type: 'stop', session: s.session, id: c.name, ship: s, crew: c, folded: null, since: crewSince.get(ck) ?? 0, fleet: shipFleet(s) });
      }
    }
    return out;
  };

  const entries = (): Entry[] =>
    [...served, ...[...held.values()].map((h) => ({ ...h.entry, folded: h.text }))].sort(compare);

  return {
    sync(next: Input, now: number) {
      const open = new Set(next.items.map((it) => itemKey(it.session, it.id)));
      for (const key of answered) if (!open.has(key)) answered.delete(key);
      const ships = new Map(next.ships.map((s) => [s.session, s]));
      // A needs-you stop whose ship, or crew boat, went back to underway (or for a boat, idle) folds; any other way out is silent.
      // A ship that needed you itself folds when the wire turns to underway or to needs_you/crew.
      for (const e of served) {
        if (e.type !== 'stop' || held.has(e.key)) continue;
        const ship = ships.get(e.session);
        const sailing = ship?.status === 'underway' || ship?.status === 'needs_you';
        // A boat is answered whatever its ship's status, bar a ship that is sailing off or gone.
        const back = e.crew
          ? ship !== undefined && ship.status !== 'sailing_off' && isBack(ship.crew?.find((c) => c.name === e.crew!.name))
          : sailing && !needsYou(ship!);
        if (back) held.set(e.key, { entry: e, text: 'Back underway', until: now + HOLD_MS });
      }
      const live = new Set<string>();
      for (const s of next.ships) {
        const mk = mergeKey(s.session);
        if (readyToMerge(s)) {
          if (!mergeFirstSeen.has(mk)) mergeFirstSeen.set(mk, now);
        } else {
          mergeFirstSeen.delete(mk);
          merged.delete(mk);
        }
        const key = stopKey(s.session);
        if (needsYou(s) && !stopFirstSeen.has(key)) stopFirstSeen.set(key, now);
        if (!needsYou(s)) stopFirstSeen.delete(key);
        for (const c of waiting(s)) {
          const ck = crewKey(s.session, c.name);
          live.add(ck);
          if (!crewSince.has(ck)) crewSince.set(ck, Date.parse(c.last_activity ?? '') || Date.parse(c.launched ?? '') || now);
        }
      }
      for (const mk of [...mergeFirstSeen.keys()]) if (!ships.has(mk.slice('merge:'.length))) mergeFirstSeen.delete(mk);
      for (const mk of merged) if (!ships.has(mk.slice('merge:'.length))) merged.delete(mk);
      for (const ck of crewSince.keys()) if (!live.has(ck)) crewSince.delete(ck);
      input = next;
      served = merge();
    },

    // `known` covers an answer whose push left the queue before its response came back.
    fold(key: string, text: string, now: number, known?: Entry) {
      const entry = entries().find((e) => e.key === key) ?? known;
      if (!entry || held.has(key)) return;
      if (entry.type === 'item') answered.add(key);
      if (entry.type === 'merge') merged.add(key);
      held.set(key, { entry: { ...entry, folded: null }, text, until: now + HOLD_MS });
      served = merge();
    },

    tick(now: number): boolean {
      const due = [...held].filter(([, h]) => h.until <= now);
      if (due.length === 0) return false;
      for (const [key] of due) held.delete(key);
      served = merge(); // a ship that asked again during its hold is back
      return true;
    },

    entries,
  };
}

export type InboxQueue = ReturnType<typeof createInboxQueue>;

// One queue for the whole shell, so a fold from any surface shows on every other and in the count.
const shared = createInboxQueue();
export const sharedInboxQueue = (): InboxQueue => shared;

// What waits on the engineer: every entry still in the queue but a folded one, bar FYI notices.
export const attentionCount = (queue: InboxQueue): number =>
  queue.entries().filter((e) => e.folded === null && e.item?.kind !== 'notice').length;
