import type { Source } from './api';
import { isModule, SURFACES } from './surfaces';

/** A row id of `.` or `..` would be read as a path step, so such a row has no page of its own. */
export const openableRow = (id: string) => id !== '.' && id !== '..';

export const modulePath = (key: string, row?: string) => `/app/${encodeURIComponent(key)}${row === undefined ? '' : `/${encodeURIComponent(row)}`}`;

/** The source key and selected row a path names, if it has the shape of a module path. */
export function moduleOf(path: string): { key: string; row: string | null } | null {
  const [key, row, ...rest] = path.replace(/^\/app\/?/, '').replace(/\/$/, '').split('/');
  if (!key || rest.length > 0) return null;
  try {
    const id = row === undefined ? null : decodeURIComponent(row);
    if (id !== null && !openableRow(id)) return null;
    return { key: decodeURIComponent(key), row: id };
  } catch {
    return null;
  }
}

export type ModuleRoute = { state: 'pending' } | { state: 'unknown' } | { state: 'open'; source: Source; fleet: string; row: string | null };

/** What a module-shaped path opens; null when the path isn't shaped like one or starts with a built-in surface id, which stays that surface's. */
export function resolveModule(path: string, sources: Source[], loaded: boolean): ModuleRoute | null {
  const m = moduleOf(path);
  if (!m) return null;
  if (SURFACES.some((s) => s.id === m.key)) return null;
  if (!loaded) return { state: 'pending' };
  const source = sources.find((s) => s.key === m.key);
  if (source && isModule(source)) return { state: 'open', source, fleet: source.fleet, row: m.row };
  // Until its plugin answers initialize, a source lists no capabilities, so it can't yet be told from a non-module.
  return source?.state === 'starting' ? { state: 'pending' } : { state: 'unknown' };
}
