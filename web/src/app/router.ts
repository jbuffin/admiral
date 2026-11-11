import { signal } from '@preact/signals';
import { SURFACES, type Surface } from './surfaces';

export const loc = signal({ path: location.pathname, search: location.search });

export const surfacePath = (s: Surface) => `/app/${s.id}`;

export function surfaceOf(path: string): Surface | undefined {
  const id = path.replace(/^\/app\/?/, '').replace(/\/$/, '');
  return SURFACES.find((s) => s.id === id);
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
