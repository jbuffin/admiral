import { cacheable, classifyRequest, serverGone } from './swRules';

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
sw.addEventListener('activate', (e) =>
  e.waitUntil(
    (async () => {
      for (const name of await caches.keys()) if (name !== CACHE) await caches.delete(name);
      await sw.clients.claim();
    })(),
  ),
);

// Every shell navigation is the same page, so it lives under one key: no copy per deep link or query string.
async function networkFirst(request: Request, isShell: boolean): Promise<Response> {
  const cache = await caches.open(CACHE);
  const key = isShell ? SHELL : request;
  let res: Response;
  try {
    res = await fetch(request);
  } catch (err) {
    const hit = await cache.match(key);
    if (hit) return hit;
    throw err;
  }
  if (serverGone(res.status)) return (await cache.match(key)) ?? res;
  if (cacheable(res.status)) await cache.put(key, res.clone()).catch(() => {});
  return res;
}

sw.addEventListener('fetch', (e) => {
  const handling = classifyRequest(e.request, sw.location.origin);
  if (handling === 'pass') return;
  e.respondWith(networkFirst(e.request, handling === 'shell'));
});
