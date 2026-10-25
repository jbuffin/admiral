import { describe, expect, it } from 'vitest';
import { FOOTPRINT, INSET, KEEP_OUT, REACH, type PlacedShip, type PlaceInput, type Rect, placeShip } from './placement';

// u10 S1. Expected values come from the spec: the first fleet's general area is
// roughly 9-32 % of the height and the second's 57-80 % (the prototype's slots,
// prototype:672-673), further fleets get bands evenly between, a removed fleet
// uses a band after the last one, and a ship never overlaps another or the
// hint chip and stays inside open water. The footprint is the plaque's 140 px
// against the 1194x834 reference, rounded.
const footprint = { w: 0.12, h: 0.17 };
const EPS = 1e-6;

const place = (over: Partial<PlaceInput> & Pick<PlaceInput, 'ship'>) =>
  placeShip({ fleets: ['work', 'home'], placed: [], footprint, keepOut: [], ...over });

const box = (p: { x: number; y: number }): Rect => ({ x: p.x, y: p.y, ...footprint });
const overlap = (a: Rect, b: Rect) =>
  Math.max(0, Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x) - EPS) *
  Math.max(0, Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y) - EPS);
const inWater = (p: { x: number; y: number }) =>
  p.x >= -EPS && p.y >= -EPS && p.x + footprint.w <= 1 + EPS && p.y + footprint.h <= 1 + EPS;
const ship = (session: string, fleet: string) => ({ session, fleet });

// Places `n` ships of one fleet one after the other, feeding each result back in.
function fill(fleet: string, n: number, start: PlacedShip[] = [], fleets = ['work', 'home']) {
  const placed = [...start];
  for (let i = 0; i < n; i++) {
    const s = `${fleet}-${placed.length}`;
    placed.push({ session: s, fleet, ...placeShip({ fleets, placed, ship: ship(s, fleet), footprint, keepOut: [] }) });
  }
  return placed;
}

describe('placeShip', () => {
  it('puts the first fleet toward the top and the second toward the bottom of open water', () => {
    const first = place({ ship: ship('a', 'work') });
    const second = place({ ship: ship('b', 'home') });
    expect(first.y).toBeGreaterThanOrEqual(0.07);
    expect(first.y).toBeLessThanOrEqual(0.34);
    expect(second.y).toBeGreaterThanOrEqual(0.55);
    expect(second.y).toBeLessThanOrEqual(0.82);
    expect(inWater(first)).toBe(true);
    expect(inWater(second)).toBe(true);
  });

  it('orders further fleets down the water in config order', () => {
    const fleets = ['one', 'two', 'three', 'four'];
    const ys = fleets.map((f) => place({ fleets, ship: ship(`s-${f}`, f) }));
    for (let i = 1; i < ys.length; i++) expect(ys[i].y).toBeGreaterThan(ys[i - 1].y);
    ys.forEach((p) => expect(inWater(p)).toBe(true));
  });

  it('puts a ship of a removed fleet inside open water, no higher than the last fleet', () => {
    const last = place({ ship: ship('h', 'home') });
    const gone = place({ ship: ship('g', 'long-gone') });
    expect(inWater(gone)).toBe(true);
    expect(gone.y).toBeGreaterThanOrEqual(last.y - EPS);
    expect(gone.y).toBeGreaterThanOrEqual(0.55);
  });

  it('places many ships of one fleet without any overlap, inside open water and the fleet area', () => {
    const placed = fill('work', 6);
    for (const p of placed) {
      expect(inWater(p)).toBe(true);
      expect(p.y).toBeGreaterThanOrEqual(0.07);
      expect(p.y).toBeLessThanOrEqual(0.34);
    }
    for (let i = 0; i < placed.length; i++) {
      for (let j = i + 1; j < placed.length; j++) expect(overlap(box(placed[i]), box(placed[j]))).toBe(0);
    }
  });

  it('does not overlap ships of another fleet that sit in its way', () => {
    const intruder: PlacedShip = { session: 'x', fleet: 'home', x: 0.0, y: 0.09 };
    const p = place({ placed: [intruder], ship: ship('a', 'work') });
    expect(overlap(box(p), box(intruder))).toBe(0);
  });

  it('finds the one free spot in a band that is otherwise tiled', () => {
    const placed: PlacedShip[] = [];
    for (const y of [0.09, 0.26]) {
      for (let i = 0; i < 8; i++) {
        if (y === 0.26 && i === 3) continue; // the gap, x = 0.36
        placed.push({ session: `t-${y}-${i}`, fleet: 'work', x: i * footprint.w, y });
      }
    }
    const p = place({ placed, ship: ship('new', 'work') });
    for (const o of placed) expect(overlap(box(p), box(o))).toBe(0);
    expect(p.x).toBeCloseTo(0.36, 4);
    expect(inWater(p)).toBe(true);
  });

  it('with the band full, takes the free water nearest the band rather than overlap a ship', () => {
    const tiled: PlacedShip[] = [];
    for (const y of [0.09, 0.26]) {
      for (let i = 0; i < 8; i++) tiled.push({ session: `t-${y}-${i}`, fleet: 'work', x: i * footprint.w, y });
    }
    const p = place({ placed: tiled, ship: ship('new', 'work') });
    expect(inWater(p)).toBe(true);
    for (const o of tiled) expect(overlap(box(p), box(o))).toBe(0);
    expect(p.y).toBeCloseTo(0.43, 4);
  });

  it('with all open water full, still lands inside it, not stacked on a ship', () => {
    const tiled: PlacedShip[] = [];
    for (let r = 0; r * footprint.h <= 1 - footprint.h + EPS; r++) {
      for (let i = 0; i < 8; i++) tiled.push({ session: `t-${r}-${i}`, fleet: 'work', x: i * footprint.w, y: r * footprint.h });
    }
    const p = place({ placed: tiled, ship: ship('new', 'work') });
    expect(inWater(p)).toBe(true);
    const covered = tiled.reduce((sum, o) => sum + overlap(box(p), box(o)), 0);
    expect(covered).toBeLessThan(0.9 * footprint.w * footprint.h);
  });

  it('keeps out of the hint chip and every other keep-out area', () => {
    const chip: Rect = { x: 0, y: 0.05, w: 1, h: 0.2 }; // covers the first fleet band's natural start
    const p = place({ keepOut: [chip], ship: ship('a', 'work') });
    expect(overlap(box(p), chip)).toBe(0);
    expect(inWater(p)).toBe(true);
  });

  it('gives the same answer for the same input, wherever the browser runs', () => {
    const placed = fill('work', 4).concat(fill('home', 3));
    const input: PlaceInput = { fleets: ['work', 'home'], placed, ship: ship('new', 'home'), footprint, keepOut: [{ x: 0.3, y: 0, w: 0.4, h: 0.06 }] };
    const first = placeShip(input);
    expect(placeShip(structuredClone(input))).toEqual(first);
    expect(placeShip({ ...input, placed: [...placed].reverse() })).toEqual(first);
  });
});

describe('placeShip with the real footprint, inset and keep-out areas', () => {
  const fleets = ['work', 'home'];
  const boxOf = (p: { x: number; y: number }): Rect => ({ x: p.x, y: p.y, ...FOOTPRINT });

  it('keeps six ships of each fleet on the water, off the land and the left edge, and apart', () => {
    const placed: PlacedShip[] = [];
    for (let i = 0; i < 12; i++) {
      const fleet = fleets[i % 2];
      const session = `s-${i}`;
      const spot = placeShip({ fleets, placed, ship: { session, fleet }, footprint: FOOTPRINT, keepOut: KEEP_OUT, inset: INSET });
      placed.push({ session, fleet, ...spot });
    }
    for (const [i, p] of placed.entries()) {
      expect(p.x).toBeGreaterThanOrEqual(INSET - EPS);
      for (const k of KEEP_OUT) expect(overlap(boxOf(p), k), `ship ${i} in a keep-out area`).toBe(0);
      for (const q of placed.slice(i + 1)) expect(overlap(boxOf(p), boxOf(q)), `ships ${i} and ${placed.indexOf(q)}`).toBe(0);
    }
  });
});

// Crew boats (#154 u2). Every new ship gets room on its left for the crew boats that may raft there: a left reach
// of REACH (a fraction of the width, about 76 px of the 758 px layout, past the crew spots' furthest -72).
// `placeShip` takes it as `reach` (default 0, so the cases above are unchanged). The candidate's box and every
// placed ship's box grow to the left by the reach: {x - reach, y, w + reach, h}. (x, y) still means the ship's
// top-left, so a stored position means what it meant before; placed ships are only read, never moved.
describe('placeShip with a left reach', () => {
  const reach = REACH;
  const reachBox = (p: { x: number; y: number }): Rect => ({ x: p.x - reach, y: p.y, ...footprint });
  const withReach = (p: { x: number; y: number }): Rect => ({ x: p.x - reach, y: p.y, w: footprint.w + reach, h: footprint.h });
  const next = (placed: PlacedShip[], session: string, extra: Partial<PlaceInput> = {}) =>
    placeShip({ fleets: ['work', 'home'], placed, ship: ship(session, 'work'), footprint, keepOut: [], reach, ...extra });

  it('is wide enough for the crew boats: past the crew spots at -72 px, and not a whole ship', () => {
    expect(REACH).toBeGreaterThanOrEqual(72 / 758);
    expect(REACH).toBeLessThan(FOOTPRINT.w);
  });

  it('starts the first column past the inset and the reach', () => {
    const inset = 0.02;
    const p = next([], 'a', { inset });
    expect(p.x).toBeGreaterThanOrEqual(inset + reach - EPS);
  });

  it("never lets a new ship's reach cover a ship that is already placed, however it was placed", () => {
    // Ships placed with no reach at all (older ones), packed against the left edge and each other.
    const old = fill('work', 4);
    const p = next(old, 'new');
    for (const o of old) {
      expect(overlap(withReach(p), box(o)), `reach covers ${o.session}`).toBe(0);
      expect(overlap(box(p), withReach(o))).toBe(0);
    }
    expect(inWater(p)).toBe(true);
  });

  it("keeps every new ship's reach clear of the others' when ships are placed in turn", () => {
    const placed: PlacedShip[] = [];
    for (let i = 0; i < 6; i++) placed.push({ session: `r-${i}`, fleet: 'work', ...next(placed, `r-${i}`) });
    for (const [i, p] of placed.entries()) {
      expect(inWater(p)).toBe(true);
      for (const q of placed.slice(i + 1)) {
        expect(overlap(withReach(p), box(q)), `${p.session} reach over ${q.session}`).toBe(0);
        expect(overlap(withReach(q), box(p)), `${q.session} reach over ${p.session}`).toBe(0);
      }
    }
    expect(reachBox(placed[0]).x).toBeLessThan(placed[0].x);
  });
});
