import { clickUrl, dataOf, notice } from './swPush';
import { assetUrls, cacheable, classifyRequest, serverGone } from './swRules';

// Built to a fixed /sw.js and registered with scope /app/, so the old UI is never controlled.
const CACHE = 'admiral-shell-v1';
const SHELL = '/app/';

interface WorkerFetchEvent extends Event {
  request: Request;
  respondWith(response: Promise<Response>): void;
}
interface WorkerScope {
  addEventListener(type: 'fetch', fn: (e: WorkerFetchEvent) => void): void;
  addEventListener(type: 'push', fn: (e: Event & { data: { text(): string } | null; waitUntil(p: Promise<unknown>): void }) => void): void;
  addEventListener(type: 'notificationclick', fn: (e: Event & { notification: { data: unknown; close(): void }; waitUntil(p: Promise<unknown>): void }) => void): void;
  addEventListener(type: 'install' | 'activate', fn: (e: Event & { waitUntil(p: Promise<unknown>): void }) => void): void;
  skipWaiting(): Promise<void>;
  clients: {
    claim(): Promise<void>;
    matchAll(opts: { type: 'window'; includeUncontrolled: boolean }): Promise<{ url: string; focus(): Promise<unknown>; postMessage(m: unknown): void }[]>;
    openWindow(url: string): Promise<unknown>;
  };
  registration: { showNotification(title: string, options: object): Promise<void> };
  location: Location;
}
const sw = self as unknown as WorkerScope;

// A first visit may be the only one before the device goes offline, so the install itself keeps the shell; a failure leaves the install standing.
async function precache() {
  try {
    const res = await fetch(SHELL, { cache: 'no-store' });
    if (!cacheable(res.status) || res.redirected) return;
    const cache = await caches.open(CACHE);
    const html = await res.clone().text();
    await cache.put(SHELL, res);
    await Promise.all(assetUrls(html, sw.location.origin).map((u) => cache.add(u).catch(() => {})));
  } catch {
    /* offline or proxied away: the first page load caches what it uses */
  }
}

sw.addEventListener('install', (e) => e.waitUntil(precache().then(() => sw.skipWaiting())));
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

sw.addEventListener('push', (e) => {
  let raw: string | null = null;
  try {
    raw = e.data?.text() ?? null;
  } catch {
    /* an unreadable body still shows the generic line */
  }
  const n = notice(raw);
  e.waitUntil(sw.registration.showNotification(n.title, n.options));
});

sw.addEventListener('notificationclick', (e) => {
  e.notification.close();
  const data = dataOf(e.notification.data);
  e.waitUntil(
    (async () => {
      const windows = await sw.clients.matchAll({ type: 'window', includeUncontrolled: true });
      const open = windows.find((c) => /^\/app(\/|$)/.test(new URL(c.url).pathname));
      if (open) {
        // A browser may refuse focus without a real activation; the page still has to hear the tap.
        await open.focus().catch(() => {});
        open.postMessage({ type: 'push-click', data });
      } else await sw.clients.openWindow(clickUrl(data));
    })(),
  );
});
