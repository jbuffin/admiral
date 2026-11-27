import { signal } from '@preact/signals';

const STORE = 'admiral.app.ships.collapsedRepos';

/** The stored value read defensively: anything but an array of strings starts empty. */
export function parseCollapsed(raw: string | null): Set<string> {
  try {
    const v: unknown = JSON.parse(raw ?? '[]');
    if (!Array.isArray(v) || !v.every((x) => typeof x === 'string')) return new Set();
    return new Set(v);
  } catch {
    return new Set();
  }
}

function load(): Set<string> {
  try {
    return parseCollapsed(localStorage.getItem(STORE));
  } catch {
    return new Set();
  }
}

// The collapsed repo groups of the Ships page, kept on this device.
export const collapsed = signal<Set<string>>(load());

// Keyed by status and lowercased repo, as the grouping compares repos.
const keyOf = (status: string, repo: string) => `${status}:${repo.toLowerCase()}`;

export const isCollapsed = (status: string, repo: string) => collapsed.value.has(keyOf(status, repo));

export function toggleCollapsed(status: string, repo: string) {
  const key = keyOf(status, repo);
  const next = new Set(collapsed.value);
  if (!next.delete(key)) next.add(key);
  collapsed.value = next;
  try {
    localStorage.setItem(STORE, JSON.stringify([...next]));
  } catch {
    /* private window or blocked storage */
  }
}
