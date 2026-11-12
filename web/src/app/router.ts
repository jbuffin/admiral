import { signal } from '@preact/signals';
import { SURFACES, type Surface } from './surfaces';

export const loc = signal({ path: location.pathname, search: location.search });

export const surfacePath = (s: Surface) => `/app/${s.id}`;

const segments = (path: string) => path.replace(/^\/app\/?/, '').replace(/\/$/, '').split('/');

export function surfaceOf(path: string): Surface | undefined {
  const [id, ...sub] = segments(path);
  const s = SURFACES.find((x) => x.id === id);
  if (s?.id === 'inbox' && sub.length > 0) return peekOf(path) ? s : undefined;
  if (s?.id === 'ships' && sub.length === 1 && sub[0] !== '') return s;
  // Preferences alone has pages below it: /app/preferences/<fleet key>/<page>.
  // Campaigns has the Campaign page below it: /app/campaigns/<id>.
  const below =
    (s?.id === 'preferences' && sub.length === 2 && (PREFERENCES_PAGES as readonly string[]).includes(sub[1]) && (sub[1] !== 'remote' || sub[0] === 'admiral')) ||
    (s?.id === 'campaigns' && sub.length === 1);
  if (s && sub.length > 0 && !below) return undefined;
  return s;
}

export const shipPath = (session: string) => `/app/ships/${encodeURIComponent(session)}`;

/** The session a ship view's path names, if any. */
export function shipSession(path: string): string | null {
  const [id, key, ...rest] = segments(path);
  if (id !== 'ships' || !key || rest.length > 0) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

export interface PeekTarget {
  session: string;
  crew?: string;
  item?: string;
}

export const peekPath = (t: PeekTarget) => {
  const base = `/app/inbox/${encodeURIComponent(t.session)}`;
  return t.crew !== undefined ? `${base}/crew/${encodeURIComponent(t.crew)}` : t.item !== undefined ? `${base}/item/${encodeURIComponent(t.item)}` : base;
};

/** What a Peek path opens: a ship's stop, a crew boat's stop or a file item. Null for the list and for any other path. */
export function peekOf(path: string): PeekTarget | null {
  const [id, session, kind, name, ...rest] = segments(path);
  if (id !== 'inbox' || !session || rest.length > 0) return null;
  try {
    const decoded = decodeURIComponent(session);
    // A slash would let two paths map to one queue key.
    if (decoded.includes('/')) return null;
    if (kind === undefined) return { session: decoded };
    if (!name || (kind !== 'crew' && kind !== 'item')) return null;
    const n = decodeURIComponent(name);
    if (kind === 'crew' && n.includes('/')) return null;
    return { session: decoded, [kind]: n };
  } catch {
    return null;
  }
}

const PREFERENCES_PAGES = ['fleet', 'sources', 'capabilities', 'remote'] as const;

// A fleet key can't be `admiral` on the remote page: that segment is reserved for the global pages.
const isRemote = (path: string) => {
  const [id, key, page] = segments(path);
  return id === 'preferences' && key === 'admiral' && page === 'remote';
};

export const remotePath = '/app/preferences/admiral/remote';
export type PreferencesPage = (typeof PREFERENCES_PAGES)[number];

export const sourcesPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/sources`;

export const fleetPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/fleet`;

export const capabilitiesPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/capabilities`;

/** The fleet key a Preferences path names, if any. */
export function preferencesFleet(path: string): string | null {
  const [id, key] = segments(path);
  if (id !== 'preferences' || !key || isRemote(path)) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

/** The page below a fleet's Preferences group; sources when the path names none. */
export function preferencesPage(path: string): PreferencesPage {
  if (isRemote(path)) return 'remote';
  const page = segments(path)[2];
  return (PREFERENCES_PAGES as readonly string[]).includes(page) && page !== 'remote' ? (page as PreferencesPage) : 'sources';
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
