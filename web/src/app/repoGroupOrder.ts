import { signal } from '@preact/signals';

const STORE = 'admiral.app.ships.repoOrder';

/** The stored value read defensively: anything but an array of strings starts empty. */
export function parseRepoOrder(raw: string | null): string[] {
  try {
    const v: unknown = JSON.parse(raw ?? '[]');
    if (!Array.isArray(v) || !v.every((x) => typeof x === 'string')) return [];
    return v.map((x) => x.toLowerCase());
  } catch {
    return [];
  }
}

function load(): string[] {
  try {
    return parseRepoOrder(localStorage.getItem(STORE));
  } catch {
    return [];
  }
}

// The engineer's repo order for the whole Ships page, lowercased as the grouping compares repos, kept on this device.
export const repoOrder = signal<string[]>(load());

/** Placed repos first in stored order, the rest after in the order given. */
export function applyRepoOrder<T extends { repo: string }>(groups: T[]): T[] {
  const rank = new Map(repoOrder.value.map((r, i) => [r.toLowerCase(), i]));
  const at = (g: T) => rank.get(g.repo.toLowerCase()) ?? Infinity;
  return groups.map((g, i) => ({ g, i })).sort((a, b) => at(a.g) - at(b.g) || a.i - b.i).map((x) => x.g);
}

/** Moves `repo` to index `to` among the status heading's repos (as shown, in order) and stores that whole order. */
export function moveRepo(shown: string[], repo: string, to: number) {
  const key = repo.toLowerCase();
  const heading = shown.map((r) => r.toLowerCase());
  if (heading.indexOf(key) === to) return;
  const next = heading.filter((r) => r !== key);
  next.splice(to, 0, key);
  repoOrder.value = [...next, ...repoOrder.value.filter((r) => !next.includes(r))];
  try {
    localStorage.setItem(STORE, JSON.stringify(repoOrder.value));
  } catch {
    /* private window or blocked storage */
  }
}
