import { signal } from '@preact/signals';
import { moduleOf, modulePath, resolveModule } from './modules';
import type { Source } from './api';
import { sources, sourcesLoaded } from './shipsLink';
import { SURFACES, type Surface } from './surfaces';

export { moduleOf, modulePath };

export const loc = signal({ path: location.pathname, search: location.search });

export const surfacePath = (s: Surface) => `/app/${s.id}`;

const segments = (path: string) => path.replace(/^\/app\/?/, '').replace(/\/$/, '').split('/');

export function surfaceOf(path: string): Surface | undefined {
  const [id, ...sub] = segments(path);
  const s = SURFACES.find((x) => x.id === id);
  if (s?.id === 'inbox' && sub.length > 0) return peekOf(path) ? s : undefined;
  if (s?.id === 'chat' && sub.length > 0) return chatPeekOf(path) || chatTerminalOf(path) || chatMessageOf(path) ? s : undefined;
  if (s?.id === 'ships' && sub.length === 1 && sub[0] !== '') return s;
  // Preferences alone has pages below it: /app/preferences/<fleet key>/<page>.
  // Campaigns has the Campaign page below it: /app/campaigns/<id>; Crew has a member: /app/crew/<name>.
  const below =
    (s?.id === 'preferences' && sub.length === 2 && (PREFERENCES_PAGES as readonly string[]).includes(sub[1]) && (!GLOBAL_PAGES.includes(sub[1]) || sub[0] === 'admiral')) ||
    (s?.id === 'campaigns' && sub.length === 1) ||
    (s?.id === 'crew' && sub.length === 1) ||
    (s?.id === 'routines' && sub.length === 1);
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
  /** The ship's ready-to-merge entry. */
  merge?: true;
  /** The ship's Re-review entry. */
  rereview?: true;
}

export const peekPath = (t: PeekTarget) => {
  const base = `/app/inbox/${encodeURIComponent(t.session)}`;
  if (t.merge) return `${base}/merge`;
  if (t.rereview) return `${base}/rereview`;
  return t.crew !== undefined ? `${base}/crew/${encodeURIComponent(t.crew)}` : t.item !== undefined ? `${base}/item/${encodeURIComponent(t.item)}` : base;
};

/** What a Peek path opens: a ship's stop, a crew boat's stop, a file item or a ship's merge or Re-review entry. Null for the list and for any other path. */
export function peekOf(path: string): PeekTarget | null {
  const [id, session, kind, name, ...rest] = segments(path);
  if (id !== 'inbox' || !session || rest.length > 0) return null;
  try {
    const decoded = decodeURIComponent(session);
    // A slash would let two paths map to one queue key.
    if (decoded.includes('/')) return null;
    if (kind === undefined) return { session: decoded };
    if (kind === 'merge' && name === undefined) return { session: decoded, merge: true };
    if (kind === 'rereview' && name === undefined) return { session: decoded, rereview: true };
    if (!name || (kind !== 'crew' && kind !== 'item')) return null;
    const n = decodeURIComponent(name);
    if (kind === 'crew' && n.includes('/')) return null;
    return { session: decoded, [kind]: n };
  } catch {
    return null;
  }
}

/** Peek in place inside the Chat surface: an item of any session, opened beside the fleet's chat. */
export const chatPeekPath = (fleetKey: string, session: string, id: string) =>
  `/app/chat/${encodeURIComponent(fleetKey)}/item/${encodeURIComponent(session)}/${encodeURIComponent(id)}`;

export function chatPeekOf(path: string): (PeekTarget & { item: string; fleet: string }) | null {
  const [id, fleet, kind, session, item, ...rest] = segments(path);
  if (id !== 'chat' || kind !== 'item' || !fleet || !session || !item || rest.length > 0) return null;
  try {
    const s = decodeURIComponent(session);
    if (s.includes('/')) return null;
    return { fleet: decodeURIComponent(fleet), session: s, item: decodeURIComponent(item) };
  } catch {
    return null;
  }
}

/** The fleet's chat opened at one message. */
export const chatMessagePath = (fleetKey: string, messageId: string) => `/app/chat/${encodeURIComponent(fleetKey)}/message/${encodeURIComponent(messageId)}`;

export function chatMessageOf(path: string): { fleet: string; message: string } | null {
  const [id, fleet, kind, message, ...rest] = segments(path);
  if (id !== 'chat' || kind !== 'message' || !fleet || !message || rest.length > 0) return null;
  try {
    return { fleet: decodeURIComponent(fleet), message: decodeURIComponent(message) };
  } catch {
    return null;
  }
}

/** The fleet's chat session shown through the bridge, in place of the conversation. */
export const chatTerminalPath = (fleetKey: string) => `/app/chat/${encodeURIComponent(fleetKey)}/terminal`;

export function chatTerminalOf(path: string): string | null {
  const [id, fleet, kind, ...rest] = segments(path);
  if (id !== 'chat' || kind !== 'terminal' || !fleet || rest.length > 0) return null;
  try {
    return decodeURIComponent(fleet);
  } catch {
    return null;
  }
}

const PREFERENCES_PAGES = ['fleet', 'admiralbot', 'github', 'sources', 'capabilities', 'usage', 'limits', 'allow-rules', 'all-usage', 'remote', 'install', 'notifications'] as const;

const GLOBAL_PAGES: string[] = ['remote', 'all-usage', 'install', 'notifications'];

// The global pages live only under the reserved `admiral` segment; any other page there belongs to a fleet keyed `admiral`.
const globalPage = (path: string): 'remote' | 'all-usage' | 'install' | 'notifications' | null => {
  const [id, key, page] = segments(path);
  return id === 'preferences' && key === 'admiral' && GLOBAL_PAGES.includes(page) ? (page as 'remote' | 'all-usage' | 'install' | 'notifications') : null;
};

export const preferencesPath = '/app/preferences';
export const remotePath = '/app/preferences/admiral/remote';
export const notificationsPath = '/app/preferences/admiral/notifications';
export const usageAllPath = '/app/preferences/admiral/all-usage';
export const installPath = '/app/preferences/admiral/install';
export type PreferencesPage = (typeof PREFERENCES_PAGES)[number];

export const sourcesPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/sources`;

export const githubPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/github`;

export const fleetPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/fleet`;

export const usagePath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/usage`;

export const chatSettingsPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/admiralbot`;

export const capabilitiesPath = (fleetKey: string) => `/app/preferences/${encodeURIComponent(fleetKey)}/capabilities`;

/** True for the bare Preferences path, which a phone answers with the list of pages. */
export const preferencesBare = (path: string) => segments(path).join('/') === 'preferences';

/** The fleet key a Preferences path names, if any. */
export function preferencesFleet(path: string): string | null {
  const [id, key] = segments(path);
  if (id !== 'preferences' || !key || globalPage(path)) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

/** The page below a fleet's Preferences group; sources when the path names none. */
export function preferencesPage(path: string): PreferencesPage {
  const global = globalPage(path);
  if (global) return global;
  const page = segments(path)[2];
  return (PREFERENCES_PAGES as readonly string[]).includes(page) && !GLOBAL_PAGES.includes(page) ? (page as PreferencesPage) : 'sources';
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

export const crewPath = (name: string) => `/app/crew/${encodeURIComponent(name)}`;

/** The crew member a detail path names, if any. */
export function crewOf(path: string): string | null {
  const [id, key, ...rest] = segments(path);
  if (id !== 'crew' || !key || rest.length > 0) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

export const routinePath = (id: string) => `/app/routines/${encodeURIComponent(id)}`;

export const routineNewPath = '/app/routines/new';

export const routineCreating = (path: string) => routinePath('new') === path.replace(/\/$/, '');

const STANDING_ORDERS = 'standing-orders';

/** The routine a detail path names, if any; null for the list and for the new-routine path. */
export function routineOf(path: string): string | null {
  const [id, key, ...rest] = segments(path);
  if (id !== 'routines' || !key || key === 'new' || key === STANDING_ORDERS || rest.length > 0) return null;
  try {
    return decodeURIComponent(key);
  } catch {
    return null;
  }
}

export const standingOrdersPath = '/app/routines/standing-orders';

/** True for the Routines screen's Standing orders tab. */
export const standingOrdersTab = (path: string): boolean => path.replace(/\/$/, '') === standingOrdersPath;

/** A fleet's Limits page, landing on one field when named. */
export const limitsPath = (fleetKey: string, field?: string): string => `/app/preferences/${encodeURIComponent(fleetKey)}/limits${field ? `?field=${encodeURIComponent(field)}` : ''}`;

/** A fleet's allow rules and skipped proposals. */
export const allowRulesPath = (fleetKey: string): string => `/app/preferences/${encodeURIComponent(fleetKey)}/allow-rules`;

/** The field a Limits page query names, if any. */
export const limitsFieldOf = (search: string): string | null => new URLSearchParams(search).get('field') || null;

function sync() {
  loc.value = { path: location.pathname, search: location.search };
}

export function go(path: string, search: string, replace = false) {
  history[replace ? 'replaceState' : 'pushState'](null, '', path + search);
  sync();
}

export interface FleetView {
  active: string | null;
  keys: string[];
}

// fleets.ts imports this file, so it hands its state over instead of being imported.
let fleetView = (): FleetView => ({ active: null, keys: [] });
export const provideFleets = (fn: () => FleetView) => {
  fleetView = fn;
};

const withFleet = (search: string, fleet: string) => {
  const q = new URLSearchParams(search);
  q.set('fleet', fleet);
  return `?${q}`;
};

/** Where a path that isn't a surface goes once its meaning is known: null keeps it, else the entry replacing it. */
export function settleTarget(path: string, search: string, list: Source[], loaded: boolean, fleet: FleetView): { path: string; search: string } | null {
  if (surfaceOf(path)) return null;
  const m = resolveModule(path, list, loaded);
  if (m?.state === 'pending') return null;
  if (m?.state === 'open') {
    // A module of another fleet opens that fleet, in place of the entry so Back doesn't return to the mismatch.
    return fleet.active && fleet.active !== m.fleet && fleet.keys.includes(m.fleet) ? { path, search: withFleet(search, m.fleet) } : null;
  }
  return { path: '/app/inbox', search };
}

/** The path a fleet switch lands on: a module page of another fleet is left for the inbox, so the switch sticks. */
export function pathAfterFleetSwitch(path: string, search: string, key: string, list: Source[], loaded: boolean): string {
  if (surfaceOf(path)) return path;
  const m = resolveModule(path, list, loaded);
  return m?.state === 'open' && m.fleet !== key ? '/app/inbox' : path;
}

function normalize() {
  // Reads every input before deciding, so an effect over it never goes idle.
  const t = settleTarget(location.pathname, location.search, sources.value, sourcesLoaded.value, fleetView());
  const at = loc.value;
  if (t) history.replaceState(null, '', t.path + t.search);
  // An unchanged location isn't rewritten, or the effect over this would feed itself.
  if (at.path !== location.pathname || at.search !== location.search) sync();
}

/** Judges the path again, so /app, /app/ and unknown paths land on the inbox in place of the entry, for when the sources, the fleets or the path change. */
export const settleRoute = normalize;

export function startRouter() {
  addEventListener('popstate', normalize);
  normalize();
}
