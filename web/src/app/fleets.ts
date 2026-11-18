import { computed, effect, signal } from '@preact/signals';
import { fetchFleets, type Fleet } from './api';
import { chatTerminalOf, go, loc, pathAfterFleetSwitch, provideFleets } from './router';
import { sources, sourcesLoaded } from './shipsLink';
import { fleetPaint } from './fleetPaint';

export { fleetPaint };
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

provideFleets(() => ({ active: activeFleet.value?.key ?? null, keys: fleets.value.map((f) => f.key) }));

export const fleetSearch = computed(() => {
  // Until a fleet resolves (config loading or failed), the URL's own ?fleet= passes through.
  const key = activeFleet.value?.key ?? new URLSearchParams(loc.value.search).get('fleet');
  return key ? `?fleet=${encodeURIComponent(key)}` : '';
});

/** Remembers a fleet as the last one chosen, for a reload without ?fleet=. */
export const rememberFleet = (key: string) => store.set(key);

export function chooseFleet(key: string) {
  store.set(key);
  // The terminal path names one fleet's chat session; another fleet opens on its conversation.
  const path = chatTerminalOf(loc.value.path) === null ? loc.value.path : '/app/chat';
  go(pathAfterFleetSwitch(path, loc.value.search, key, sources.value, sourcesLoaded.value), `?fleet=${encodeURIComponent(key)}`);
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
      const draft = new URLSearchParams(loc.value.search).get('draft');
      go(loc.value.path, `?fleet=${encodeURIComponent(active.key)}${draft ? `&draft=${encodeURIComponent(draft)}` : ''}`, true);
    }
  });
  void loadFleets();
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void loadFleets();
  });
}

