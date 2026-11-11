// Collision-free placement of a new ship in open water (ADR 0004). Everything is in fractions of the 1194x834
// reference open water, so every browser computes the same spot.

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface PlacedShip {
  session: string;
  fleet: string;
  x: number;
  y: number;
}

export interface PlaceInput {
  /** Fleet keys in config order: the first fleet's band is the top one. */
  fleets: string[];
  placed: PlacedShip[];
  /** A fleet missing from `fleets` is a removed fleet. */
  ship: { session: string; fleet: string };
  footprint: { w: number; h: number };
  keepOut: Rect[];
  /** How far from the left edge ships start, as a fraction of the width. */
  inset?: number;
  /** Room a new ship keeps free on its left for its crew boats, as a fraction of the width. */
  reach?: number;
}

const EPS = 1e-6;
const FIRST = 0.09;
const LAST = 0.57;
const BAND = 0.23;
// Coarse spots first, so ships line up in rows, then every 1 % of the water.
const FINE = 0.01;

// Measured against the narrowest layout (main 758 px wide, the sea ending at about 0.617 of it) and the shortest
// open water (760 px), so each layout's real ship box fits inside this nominal one.
export const FOOTPRINT = { w: 142 / 758, h: 142 / 760 };
export const INSET = 12 / 758; // ships keep off the orders panel's edge
// The crew boats raft off a ship's bow (tokens.json size.crewRaft: as far as -72 px), so every new ship keeps
// this much room free on its left. Already-placed ships don't move; (x, y) still marks the ship's own top-left.
export const REACH_PX = 76;
export const REACH = REACH_PX / 758;
export const KEEP_OUT: Rect[] = [
  { x: 0.01, y: 0.015, w: 0.46, h: 0.065 }, // the hint chip
  { x: 0.61, y: 0, w: 0.39, h: 1 }, // the marinas' land
];

const overlap = (a: Rect, b: Rect) =>
  Math.max(0, Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x) - EPS) *
  Math.max(0, Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y) - EPS);

function band(fleets: string[], fleet: string, h: number): [number, number] {
  const n = fleets.length;
  const at = (i: number) => (n < 2 ? FIRST : FIRST + (i * (LAST - FIRST)) / (n - 1));
  const i = fleets.indexOf(fleet);
  // A removed fleet's ships go in the band after the last fleet.
  const from = i >= 0 ? at(i) : n === 0 ? FIRST : at(n - 1) + BAND;
  const bottom = 1 - h;
  return [Math.min(from, bottom), Math.min(from + BAND, bottom)];
}

export function placeShip({ fleets, placed, ship, footprint, keepOut, inset = 0, reach = 0 }: PlaceInput): { x: number; y: number } {
  const { w, h } = footprint;
  const [top, bottom] = band(fleets, ship.fleet, h);
  const maxX = 1 - w;
  // Sorted, so the answer does not depend on the order the ships arrived in.
  const others: Rect[] = placed
    .filter((p) => p.session !== ship.session)
    .sort((a, b) => (a.session < b.session ? -1 : a.session > b.session ? 1 : 0))
    .map((p) => ({ x: p.x - reach, y: p.y, w: w + reach, h }));
  const covered = (x: number, y: number, rects: Rect[]) =>
    rects.reduce((sum, r) => sum + overlap({ x: x - reach, y, w: w + reach, h }, r), 0);

  const rows = (step: number, from = top, to = bottom) => {
    const out: number[] = [];
    for (let y = from; y <= to + EPS; y += step) out.push(y);
    return out;
  };
  const cols = (step: number) => {
    const out: number[] = [];
    for (let x = inset + reach; x <= maxX + EPS; x += step) out.push(Math.min(x, maxX));
    return out;
  };
  const free = (x: number, y: number) => covered(x, y, others) === 0 && covered(x, y, keepOut) === 0;

  for (const [rowStep, colStep] of [
    [h, w],
    [FINE, FINE],
  ]) {
    for (const y of rows(rowStep)) for (const x of cols(colStep)) if (free(x, y)) return { x, y };
  }

  // The band is full: the fleet's area is no hard lane, so any free spot in open water, nearest the band first.
  const near = (y: number) => (y < top ? top - y : y > bottom ? y - bottom : 0);
  const anywhere = rows(FINE, 0, 1 - h).sort((a, b) => near(a) - near(b));
  for (const y of anywhere) for (const x of cols(FINE)) if (free(x, y)) return { x, y };

  // Nothing is free: the spot that covers the least, the first one on a tie.
  let best = { x: inset + reach, y: top };
  let least = Infinity;
  for (const y of rows(FINE)) {
    for (const x of cols(FINE)) {
      const c = covered(x, y, others) + 4 * covered(x, y, keepOut);
      if (c < least - EPS) [best, least] = [{ x, y }, c];
    }
  }
  return best;
}
