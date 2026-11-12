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

/** The same-origin /assets/ files a page loads through <script src> and <link href>, in document order. */
export function assetUrls(html: string, origin: string): string[] {
  const out: string[] = [];
  for (const m of html.matchAll(/<(?:script|link)\b[^>]*?\s(?:src|href)\s*=\s*["']([^"']+)["']/gi)) {
    try {
      const u = new URL(m[1], origin);
      if (u.origin === origin && u.pathname.startsWith('/assets/')) out.push(u.pathname);
    } catch {
      /* not a URL */
    }
  }
  return out;
}
