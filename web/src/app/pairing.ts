import type { shipsLink } from './shipsLink';

const KEY = 'admiral.paired';

export function wasPaired(): boolean {
  try {
    return localStorage.getItem(KEY) !== null;
  } catch {
    return false;
  }
}

export function markPaired() {
  try {
    localStorage.setItem(KEY, '1');
  } catch {
    /* blocked storage: the heading stays Pair this app */
  }
}

export const pairHeading = (paired: boolean) => (paired ? 'This device was signed out' : 'Pair this app');

/** A rejected fetch is unreachable and never asks for a code. */
export function gateOf(link: typeof shipsLink.value): 'pair' | 'unreachable' | 'shell' {
  if (link === 'signed-out') return 'pair';
  if (link === 'unreachable') return 'unreachable';
  return 'shell';
}
