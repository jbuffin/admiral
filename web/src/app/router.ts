import { signal } from '@preact/signals';
import { SURFACES, type Surface } from './surfaces';

export const loc = signal({ path: location.pathname, search: location.search });

export const surfacePath = (s: Surface) => `/app/${s.id}`;

const segments = (path: string) => path.replace(/^\/app\/?/, '').replace(/\/$/, '').split('/');

export function surfaceOf(path: string): Surface | undefined {
  const [id, ...sub] = segments(path);
  const s = SURFACES.find((x) => x.id === id);
  // Preferences alone has pages below it: /app/preferences/<fleet key>/<page>.
  // Campaigns has the Campaign page below it: /app/campaigns/<id>.
  const below = (s?.id === 'preferences' && sub.length === 2 && (PREFERENCES_PAGES as readonly string[]).includes(sub[1])) || (s?.id === 'campaigns' && sub.length === 1);
  if (s && sub.length > 0 && !below) return undefined;
  return s;
}

const PREFERENCES_PAGES = ['fleet', 'sources', 'capabilities'] as const;
export type PreferencesPage = (typeof PREFERENCES_PAGES)[number];

export const sourcesPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/sources`;

export const fleetPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/fleet`;

export const capabilitiesPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/capabilities`;

/** The fleet key a Preferences path names, if any. */
export function preferencesFleet(path: string): string | null {
  const [id, key] = segments(path);
  if (id !== 'preferences' || !key) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

/** The page below a fleet's Preferences group; sources when the path names none. */
export function preferencesPage(path: string): PreferencesPage {
  const page = segments(path)[2];
  return (PREFERENCES_PAGES as readonly string[]).includes(page) ? (page as PreferencesPage) : 'sources';
}

export const campaignPath = (id: string) => `/app/campaigns/${encodeURIComponent(id)}`;

/** The campaign id a Campaign page path names, if any. */
export function campaignOf(path: string): string | null {
  const [id, key, ...rest] = segments(path);
  if (id !== 'campaigns' || !key || rest.length > 0) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

function sync() {
  loc.value = { path: location.pathname, search: location.search };
}

export function go(path: string, search: string, replace = false) {
  history[replace ? 'replaceState' : 'pushState'](null, '', path + search);
  sync();
}

// /app, /app/ and unknown paths land on the inbox, replacing the entry so Back doesn't loop.
function normalize() {
  if (!surfaceOf(location.pathname)) history.replaceState(null, '', `/app/inbox${location.search}`);
  sync();
}

export function startRouter() {
  addEventListener('popstate', normalize);
  normalize();
}
