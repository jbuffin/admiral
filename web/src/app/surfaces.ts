import type { Source } from './api';
import { sourcesForFleet } from './launchForm';
export type SurfaceId = 'inbox' | 'ships' | 'orders' | 'chat' | 'campaigns' | 'routines' | 'crew' | 'preferences';

export interface Surface {
  id: SurfaceId;
  title: string;
  /** Names the active fleet. The inbox spans every fleet; Preferences is global. */
  scoped: boolean;
  line: string;
}

// In nav order.
export const SURFACES: Surface[] = [
  { id: 'inbox', title: 'Inbox', scoped: false, line: 'Whatever needs you, from every fleet, will land here.' },
  { id: 'ships', title: 'Ships', scoped: true, line: 'The ships sailing from this fleet will appear here.' },
  { id: 'orders', title: 'Orders', scoped: true, line: 'The orders this fleet\'s sources list will appear here.' },
  { id: 'chat', title: 'Chat', scoped: true, line: "This fleet's AdmiralBot will live here." },
  { id: 'campaigns', title: 'Campaigns', scoped: true, line: 'Campaigns for this fleet will be planned and followed here.' },
  { id: 'routines', title: 'Routines', scoped: true, line: 'The routines this fleet runs on a schedule will live here.' },
  { id: 'crew', title: 'Crew', scoped: true, line: 'The crew members of this fleet will live here.' },
  { id: 'preferences', title: 'Preferences', scoped: false, line: 'Settings for each fleet.' },
];

export const PHONE_TABS: SurfaceId[] = ['inbox', 'chat', 'ships', 'orders'];
export const MORE: SurfaceId[] = ['campaigns', 'routines', 'crew', 'preferences'];
export const isModule = (s: Source): boolean => !!s.page || !!s.capabilities?.includes('page');

export type NavItem = { kind: 'surface'; id: SurfaceId } | { kind: 'module'; key: string; name: string; icon_url: string; waiting: number };

// A source named like a built-in surface gets no entry, so the surface keeps its path.
export function modulesOf(sources: Source[], fleetKey: string | undefined): Extract<NavItem, { kind: 'module' }>[] {
  const taken = new Set<string>(SURFACES.map((s) => s.id));
  return sourcesForFleet(sources, fleetKey ?? '')
    .filter((s) => isModule(s) && !taken.has(s.key))
    .map((s) => ({ kind: 'module', key: s.key, name: s.name, icon_url: s.icon_url ?? '', waiting: s.page?.waiting ?? 0 }));
}

// Modules sit after Crew and before Preferences.
function withModules(ids: SurfaceId[], sources: Source[], fleetKey: string | undefined): NavItem[] {
  const mods = modulesOf(sources, fleetKey);
  const at = ids.indexOf('preferences');
  const items: NavItem[] = ids.map((id) => ({ kind: 'surface', id }));
  items.splice(at < 0 ? items.length : at, 0, ...mods);
  return items;
}

export const navItems = (sources: Source[], fleetKey: string | undefined) => withModules(SURFACES.map((s) => s.id), sources, fleetKey);
export const moreItems = (sources: Source[], fleetKey: string | undefined) => withModules(MORE, sources, fleetKey);

export const surfaceById = (id: SurfaceId) => SURFACES.find((s) => s.id === id)!;

/** The empty state's line: a scoped surface with no fleet asks for one, else the surface's own line. */
export function emptyLine(surface: Surface, loaded: boolean, hasFleet: boolean): string {
  return surface.scoped && loaded && !hasFleet ? `Set up a fleet and its ${surface.title.toLowerCase()} will appear here.` : surface.line;
}
