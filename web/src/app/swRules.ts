export type Handling = 'shell' | 'asset' | 'pass';

/** What the worker does with a request: keep the shell page and the files it loads, leave everything else to the network. */
export function classifyRequest(req: { method: string; url: string; mode: string }, origin: string): Handling {
  if (req.method !== 'GET') return 'pass';
  const url = new URL(req.url);
  if (url.origin !== origin) return 'pass';
  if (req.mode === 'navigate' && (url.pathname === '/app' || url.pathname.startsWith('/app/'))) return 'shell';
  if (url.pathname.startsWith('/assets/')) return 'asset';
  return 'pass';
}

export const cacheable = (status: number) => status === 200;

/** Behind tailscale serve a stopped Admiral answers 502 from the proxy: the worker treats it as no answer and serves the cache. */
export const serverGone = (status: number) => status === 502 || status === 503 || status === 504;
