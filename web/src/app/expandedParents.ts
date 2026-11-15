import { signal } from '@preact/signals';

const STORE = 'admiral.app.orders.expanded';
type Expanded = Record<string, string[]>;

/** The stored value read defensively: only tabs whose entry is an array of strings survive. */
export function parseExpanded(raw: string | null): Expanded {
  try {
    const v: unknown = JSON.parse(raw ?? '{}');
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {};
    return Object.fromEntries(Object.entries(v).filter((e): e is [string, string[]] => Array.isArray(e[1]) && e[1].every((x) => typeof x === 'string')));
  } catch {
    return {};
  }
}

function load(): Expanded {
  try {
    return parseExpanded(localStorage.getItem(STORE));
  } catch {
    return {};
  }
}

// The expanded parent ids of each view tab, kept on this device.
export const expanded = signal<Expanded>(load());

export const isExpanded = (tab: string, parent: string) => (expanded.value[tab] ?? []).includes(parent);

export function toggleExpanded(tab: string, parent: string) {
  const ids = expanded.value[tab] ?? [];
  expanded.value = { ...expanded.value, [tab]: ids.includes(parent) ? ids.filter((x) => x !== parent) : [...ids, parent] };
  try {
    localStorage.setItem(STORE, JSON.stringify(expanded.value));
  } catch {
    /* private window or blocked storage */
  }
}
