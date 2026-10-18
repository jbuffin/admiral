// Collision-free placement of a new ship in open water (u10, ADR 0004).
//
// Everything is in fraction space: x and y are fractions 0-1 of open water and
// mark the top-left of a ship's box (ship plus plaque). The caller passes the
// footprint of that box and the areas to stay out of, measured against the
// 1194x834 reference open water, so every browser computes the same spot.

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
  /** The ships that already have positions. */
  placed: PlacedShip[];
  /** The ship to place. A fleet missing from `fleets` is a removed fleet. */
  ship: { session: string; fleet: string };
  /** The size of one ship's box. */
  footprint: { w: number; h: number };
  /** Areas no ship may cover, such as the hint chip. */
  keepOut: Rect[];
}

export function placeShip(input: PlaceInput): { x: number; y: number } {
  void input;
  throw new Error('placeShip is not implemented');
}
