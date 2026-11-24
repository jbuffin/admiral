import { computed, effect, signal } from '@preact/signals';
import { config, type HarnessName } from './config';
import { connectionState, onMessage } from './connection';
import { FOOTPRINT, INSET, KEEP_OUT, REACH, placeShip, type PlacedShip } from './placement';

// A live boat has no exit_code or exit_signal.
export interface WireCrew {
  name: string;
  harness: HarnessName;
  dir: string;
  status: 'underway' | 'idle' | 'needs_you' | 'anchored';
  reason?: string;
  launched: string;
  last_activity?: string;
  exit_code?: number;
  exit_signal?: string;
}

// `start` is RFC 3339.
export interface WireSubagent {
  id: string;
  type: string;
  start: string;
}

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
export const bobDelay = (session: string) => `-${((hash(session) % 32) / 10).toFixed(1)}s`;

export { atSea } from './crew';

const launchedAt = (s: WireShip) => Date.parse(s.launched) || 0;

// Nothing in the classic view boards a ship; the signal stays so its tests can pin that.
export const boarded = signal<{ session: string; crew?: string } | null>(null);

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
const SAIL_OFF_MS = 2300; // the animation takes 2 s
const SAIL_IN_MS = 2400;

let baseline = true; // the next `ships` message is the first of this connection: nothing sails in

// A reconnect starts over: the first message after it is only a baseline.
effect(() => {
  if (connectionState.value !== 'open') baseline = true;
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

onMessage('ships', (msg: { ships: (Omit<WireShip, 'status'> & { status: WireShip['status'] | 'moored' })[] }) => {
  // This UI has no place for a moored ship: no state to draw.
  const now = msg.ships.filter((s): s is WireShip => s.status !== 'moored');
  const before = new Map(ships.value.map((s) => [s.session, s]));
  const first = baseline;
  for (const s of now) {
    const was = before.get(s.session);
    if (!first) {
      if (!was) arrive(s.session);
      else {
        const known = new Set(was.crew?.map((c) => c.name));
        const joined = (s.crew ?? []).filter((c) => !known.has(c.name)).map((c) => `${s.session}/${c.name}`);
        if (joined.length) pop(joined);
      }
    }
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
  baseline = false;
  ships.value = now;
  shipsLoaded.value = true;
});

export const visible = computed(() => {
  const here = new Set(ships.value.map((s) => s.session));
  const sailing = [...leaving.value.values()].filter((l) => !here.has(l.session));
  return [...ships.value.filter((s) => s.status !== 'sailing_off' || leaving.value.has(s.session)), ...sailing];
});

// Spots this browser has worked out and sent, until the server's push carries them.
const local = signal<ReadonlyMap<string, Spot>>(new Map());
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

// Where a ship is drawn: the server's spot, else where this browser put it.
export function spotOf(s: WireShip): Spot | undefined {
  return s.position ?? local.value.get(s.session);
}
