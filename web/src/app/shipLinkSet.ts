import type { Ship } from './api';

/** The ships a chat may link, reduced to what linking reads and as a string, so a computed over it changes only when the set does: a ship's tail moving changes the ships list but not this. */
export function shipLinkSet(ships: Ship[], fleet: string): string {
  const set = ships
    .filter((s) => s.via === 'admiralbot' && s.fleet === fleet)
    .map((s) => ({ session: s.session, name: s.name, fleet: s.fleet, via: s.via, launched: s.launched, first_launched: s.first_launched }))
    .sort((a, b) => a.session.localeCompare(b.session));
  return JSON.stringify(set);
}

export const shipsOfLinkSet = (set: string) => JSON.parse(set) as Ship[];
