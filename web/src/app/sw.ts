import { cacheable, classifyRequest } from './swRules';

// Built to a fixed /sw.js and registered with scope /app/, so the old UI is never controlled.
const CACHE = 'admiral-shell-v1';
const SHELL = '/app/';

interface WorkerFetchEvent extends Event {
  request: Request;
  respondWith(response: Promise<Response>): void;
}
interface WorkerScope {
  addEventListener(type: 'fetch', fn: (e: WorkerFetchEvent) => void): void;
  addEventListener(type: 'install' | 'activate', fn: (e: Event & { waitUntil(p: Promise<unknown>): void }) => void): void;
  skipWaiting(): Promise<void>;
  clients: { claim(): Promise<void> };
  location: Location;
}
const sw = self as unknown as WorkerScope;

sw.addEventListener('install', (e) => e.waitUntil(sw.skipWaiting()));
sw.addEventListener('activate', (e) => e.waitUntil(sw.clients.claim()));

async function networkFirst(request: Request, fallbackShell: boolean): Promise<Response> {
  const cache = await caches.open(CACHE);
  try {
    const res = await fetch(request);
    if (cacheable(res.status)) await cache.put(request, res.clone());
    return res;
  } catch (err) {
    const hit = (await cache.match(request)) ?? (fallbackShell ? await cache.match(SHELL) : undefined);
    if (hit) return hit;
    throw err;
  }
}

sw.addEventListener('fetch', (e) => {
  const handling = classifyRequest(e.request, sw.location.origin);
  if (handling === 'pass') return;
  e.respondWith(networkFirst(e.request, handling === 'shell'));
});
