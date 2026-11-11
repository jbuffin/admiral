// The active-fleet state: the fleet list from /api/v1/config and which fleet is active.
import { computed, effect, signal } from '@preact/signals';
import { fetchFleets, type Fleet } from './api';
import { go, loc } from './router';
import { FLEET_COUNT } from './shell-tokens';

export const fleets = signal<Fleet[]>([]);
export const loaded = signal(false);

const STORE = 'admiral.app.fleet';
const store = {
  get() {
    try {
      return localStorage.getItem(STORE);
    } catch {
      return null;
    }
  },
  set(key: string) {
    try {
      localStorage.setItem(STORE, key);
    } catch {
      /* private window or blocked storage */
    }
  },
};

/** ?fleet=, else the last one chosen, else the first; an unknown key falls through the same way. */
export function resolveFleet(list: Fleet[], urlKey: string | null, storedKey: string | null): Fleet | null {
  for (const k of [urlKey, storedKey]) {
    const f = k && list.find((x) => x.key === k);
    if (f) return f;
  }
  return list[0] ?? null;
}

export const activeFleet = computed(() =>
  resolveFleet(fleets.value, new URLSearchParams(loc.value.search).get('fleet'), store.get()),
);

/** The query that keeps the active fleet across navigation. */
export const fleetSearch = computed(() => {
  // Until a fleet resolves (config loading or failed), the URL's own ?fleet= passes through.
  const key = activeFleet.value?.key ?? new URLSearchParams(loc.value.search).get('fleet');
  return key ? `?fleet=${encodeURIComponent(key)}` : '';
});

export function chooseFleet(key: string) {
  store.set(key);
  go(loc.value.path, `?fleet=${encodeURIComponent(key)}`);
}

export async function loadFleets() {
  try {
    fleets.value = await fetchFleets();
  } catch {
    /* keep the last list */
  }
  loaded.value = true;
}

export function watchFleets() {
  // An unknown ?fleet= falls back to the resolved fleet; show that key in the URL without a new history entry.
  effect(() => {
    const active = activeFleet.value;
    const urlKey = new URLSearchParams(loc.value.search).get('fleet');
    if (loaded.value && active && urlKey !== null && urlKey !== active.key) {
      go(loc.value.path, `?fleet=${encodeURIComponent(active.key)}`, true);
    }
  });
  void loadFleets();
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void loadFleets();
  });
}

/** Fill and the ink on it. An indexed fleet takes the redesign's pair; a custom hex keeps its own. */
export function fleetPaint(f: Fleet): { fill: string; ink: string } {
  if (f.color_index == null) return { fill: f.color, ink: f.ink };
  const n = ((f.color_index % FLEET_COUNT) + FLEET_COUNT) % FLEET_COUNT;
  return { fill: `var(--fleet-${n})`, ink: `var(--fleet-${n}-ink)` };
}
