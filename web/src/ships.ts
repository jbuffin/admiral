import { computed, effect, signal } from '@preact/signals';
import { chime } from './chime';
import { config, type HarnessName } from './config';
import { shouldChime } from './crewSea';
import { connectionState, onMessage } from './connection';
import { FOOTPRINT, INSET, KEEP_OUT, REACH, placeShip, type PlacedShip } from './placement';

// A teammate's crew boat on the `ships` message (internal/ships/crew.go). A live boat has no exit_code or exit_signal.
export interface WireCrew {
  name: string;
  harness: HarnessName;
  dir: string;
  status: 'underway' | 'needs_you' | 'anchored';
  reason?: string;
  launched: string;
  last_activity?: string;
  exit_code?: number;
  exit_signal?: string;
}

// A subagent: a bot on its ship's deck, with no terminal (internal/sessionhost/subagents.go). `start` is RFC 3339.
export interface WireSubagent {
  id: string;
  type: string;
  start: string;
}

// What the `ships` message carries (internal/ships).
export interface WireShip {
  session: string;
  name: string;
  fleet: string;
  fleet_name: string;
  fleet_color: string;
  harness: HarnessName;
  dir: string;
  repo: string;
  skill: string | null;
  source: string;
  title: string;
  url: string;
  order_id: string;
  launched: string;
  status: 'underway' | 'needs_you' | 'anchored' | 'sailing_off';
  exit_code: number | null;
  exit_signal: string | null;
  // Set only while the ship needs you: why (a validated code, `idle` and `unknown` included) and since when (RFC 3339).
  reason?: string;
  stop_since?: string;
  position: { x: number; y: number } | null;
  crew: WireCrew[];
  subagents: WireSubagent[];
  // Crew boats that are not anchored.
  crew_live: number;
}

export interface Spot {
  x: number;
  y: number;
}

export const ships = signal<WireShip[]>([]);
export const shipsLoaded = signal(false);

// Captains' colors, tokens.json `bot`.
const BOTS = ['#7FC8FF', '#FFC23D', '#9BE08A', '#FF9A7A', '#C9B6FF', '#FFF6E3', '#6FE3C8'];

// A stable hash of the session, so a ship's captain never changes and matches across browsers.
function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
}
export const botFor = (session: string) => BOTS[hash(session) % BOTS.length];
// A ship's bob starts at its own point in the cycle.
export const bobDelay = (session: string) => `-${((hash(session) % 32) / 10).toFixed(1)}s`;

export { atSea } from './crew';

const launchedAt = (s: WireShip) => Date.parse(s.launched) || 0;

// What the bridge shows, if open: a ship's terminal, or one of its crew boats'.
export const boarded = signal<{ session: string; crew?: string } | null>(null);

// Boarding opens the bridge over open water. A test page that asked is told too: `<session>` for a ship,
// `<session>/<crew>` for a crew boat.
export function board(session: string, crew?: string): void {
  window.__admiral?.boarded.push(crew ? `${session}/${crew}` : session);
  boarded.value = crew === undefined ? { session } : { session, crew };
}

export const unboard = () => (boarded.value = null);

declare global {
  interface Window {
    // The tests create this object; `screen` is set while a bridge's terminal is up.
    __admiral?: { boarded: string[]; screen?: () => string };
  }
}

// Sessions that appeared after the page's first `ships` message: they sail in.
export const arrivals = signal<ReadonlySet<string>>(new Set());
// Sailing-off ships stay on screen until their animation ends, even when the server has dropped them.
export const leaving = signal<ReadonlyMap<string, WireShip>>(new Map());
const sailedOff = new Set<string>();
const SAIL_OFF_MS = 2300; // the animation takes 2 s; the brief allows 2.5 s
const SAIL_IN_MS = 2400;

let baseline = true; // the next `ships` message is the first of this connection: nothing sails in or chimes

// A reconnect starts over: the first message after it is only a baseline.
effect(() => {
  if (connectionState.value !== 'open') baseline = true;
});

// u9 tells the launching browser a ship has sailed, possibly before the `ships` push.
// A session known already is not announced (a reconnect baseline would sail it in again); an
// entry the pushes never claim is dropped after a while.
const ANNOUNCED_MS = 10_000;
const announced = new Map<string, number>();
window.addEventListener('admiral:launched', (e) => {
  if (!ships.peek().some((s) => s.session === e.detail.session)) announced.set(e.detail.session, Date.now());
});

// Crew boats (`<session>/<crew>`) that joined a ship after the page's first `ships` message: they pop in.
// A boat there from the start (a reconnect, a server restart) does not.
export const popping = signal<ReadonlySet<string>>(new Set());
const POP_MS = 1200; // the animation takes 0.6 s

function pop(keys: string[]): void {
  popping.value = new Set([...popping.value, ...keys]);
  setTimeout(() => {
    const next = new Set(popping.value);
    for (const k of keys) next.delete(k);
    popping.value = next;
  }, POP_MS);
}

function arrive(session: string): void {
  arrivals.value = new Set(arrivals.value).add(session);
  setTimeout(() => {
    const next = new Set(arrivals.value);
    next.delete(session);
    arrivals.value = next;
  }, SAIL_IN_MS);
}

onMessage('ships', (msg: { ships: WireShip[] }) => {
  const before = new Map(ships.value.map((s) => [s.session, s]));
  const first = baseline;
  const now = msg.ships;
  for (const [session, at] of announced) if (Date.now() - at > ANNOUNCED_MS) announced.delete(session);
  for (const s of now) {
    const was = before.get(s.session);
    if (!first) {
      if (!was) arrive(s.session);
      else {
        const known = new Set(was.crew?.map((c) => c.name));
        const joined = (s.crew ?? []).filter((c) => !known.has(c.name)).map((c) => `${s.session}/${c.name}`);
        if (joined.length) pop(joined);
      }
    } else if (announced.has(s.session)) arrive(s.session);
    announced.delete(s.session);
    if (s.status === 'sailing_off' && !sailedOff.has(s.session)) {
      sailedOff.add(s.session);
      leaving.value = new Map(leaving.value).set(s.session, s);
      setTimeout(() => {
        const next = new Map(leaving.value);
        next.delete(s.session);
        leaving.value = next;
      }, SAIL_OFF_MS);
    }
  }
  if (shouldChime([...before.values()], now, first)) chime(); // once, however many ships or crew boats turned needs you
  baseline = false;
  ships.value = now;
  shipsLoaded.value = true;
});

// What is on the water: the server's ships, minus sailing-off ones whose animation has ended, plus
// sailing-off ships the server has already dropped.
export const visible = computed(() => {
  const here = new Set(ships.value.map((s) => s.session));
  const sailing = [...leaving.value.values()].filter((l) => !here.has(l.session));
  return [...ships.value.filter((s) => s.status !== 'sailing_off' || leaving.value.has(s.session)), ...sailing];
});

// ---- placement ----

// Spots this browser has worked out and sent, until the server's push carries them.
const local = signal<ReadonlyMap<string, Spot>>(new Map());
// Spots dropped by a drag, shown until the server's push agrees.
export const dropped = signal<ReadonlyMap<string, Spot>>(new Map());
export const dragged = signal<{ session: string } & Spot | null>(null);

const clamp = (n: number) => Math.min(1, Math.max(0, n));

export async function putPosition(session: string, p: Spot): Promise<boolean> {
  const spot = { x: clamp(p.x), y: clamp(p.y) };
  try {
    const res = await fetch(`/api/ships/${encodeURIComponent(session)}/position`, {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(spot),
    });
    return res.ok;
  } catch {
    return false;
  }
}

const SETTLE_MS = 1500;
// Drops whose PUT succeeded: the next push from the server replaces them.
const accepted = new Set<string>();

function forget(session: string, spot: Spot): void {
  // Only the drop that was sent: a newer drop of the same ship stays.
  if (dropped.peek().get(session) !== spot) return;
  const next = new Map(dropped.peek());
  next.delete(session);
  dropped.value = next;
  accepted.delete(session);
}

export function drop(session: string, p: Spot): void {
  const spot = { x: clamp(p.x), y: clamp(p.y) };
  dropped.value = new Map(dropped.value).set(session, spot);
  accepted.delete(session);
  void putPosition(session, spot).then((ok) => {
    if (!ok) return forget(session, spot); // the server's spot wins
    if (dropped.peek().get(session) !== spot) return; // a newer drop is in charge now
    accepted.add(session);
    setTimeout(() => forget(session, spot), SETTLE_MS);
  });
}

// A drop the server accepted gives way to the next push; one still in flight stays until it settles.
effect(() => {
  const list = ships.value;
  if (accepted.size === 0) return;
  const next = new Map(dropped.peek());
  for (const session of accepted) {
    next.delete(session);
    accepted.delete(session);
  }
  dropped.value = next;
});

// Every ship without a position gets one, in launch order, each fed to the next.
// Any browser may send it: they all work out the same spot.
effect(() => {
  const fleets = config.value?.fleets.map((f) => f.key);
  const list = ships.value;
  if (!fleets) return;
  const known = local.peek();
  const pending = list
    .filter((s) => !s.position && !known.has(s.session))
    .sort((a, b) => launchedAt(a) - launchedAt(b) || (a.session < b.session ? -1 : 1));
  if (pending.length === 0) return;
  const placed: PlacedShip[] = list.flatMap((s) => {
    const p = s.position ?? known.get(s.session);
    return p ? [{ session: s.session, fleet: s.fleet, ...p }] : [];
  });
  const next = new Map(known);
  for (const s of pending) {
    const spot = placeShip({ fleets, placed, ship: s, footprint: FOOTPRINT, keepOut: KEEP_OUT, inset: INSET, reach: REACH });
    placed.push({ session: s.session, fleet: s.fleet, ...spot });
    next.set(s.session, spot);
    putPosition(s.session, spot);
  }
  local.value = next;
});

// Where a ship is drawn: under the finger, else where it was dropped, else the server's, else where this browser put it.
export function spotOf(s: WireShip): Spot | undefined {
  if (dragged.value?.session === s.session) return dragged.value;
  return dropped.value.get(s.session) ?? s.position ?? local.value.get(s.session);
}
