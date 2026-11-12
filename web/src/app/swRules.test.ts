import { describe, expect, it } from 'vitest';
import { assetUrls, cacheable, classifyRequest, serverGone } from './swRules';

const origin = 'http://admiral.test';
const req = (path: string, init: { method?: string; mode?: string; origin?: string } = {}) =>
  classifyRequest({ method: init.method ?? 'GET', url: `${init.origin ?? origin}${path}`, mode: init.mode ?? 'no-cors' }, origin);

describe('classifyRequest', () => {
  it('keeps the shell page and the files it loads', () => {
    expect(req('/app/', { mode: 'navigate' })).toBe('shell');
    expect(req('/app/ships/x?tab=terminal', { mode: 'navigate' })).toBe('shell');
    expect(req('/assets/app-3fa9.js')).toBe('asset');
    expect(req('/assets/inter-latin.woff2')).toBe('asset');
  });

  it('never touches the API or the sockets', () => {
    for (const p of ['/api/v1/ships', '/api/version', '/ws/v1', '/api/v1/logos/claude']) {
      expect(req(p), p).toBe('pass');
      expect(req(p, { mode: 'navigate' }), `${p} as a page`).toBe('pass');
    }
  });

  it('leaves the old UI and everything else alone', () => {
    expect(req('/', { mode: 'navigate' })).toBe('pass');
    expect(req('/classic/', { mode: 'navigate' })).toBe('pass');
    expect(req('/index.html', { mode: 'navigate' })).toBe('pass');
    expect(req('/pair', { mode: 'navigate' })).toBe('pass');
    expect(req('/app/', { method: 'POST', mode: 'navigate' })).toBe('pass');
    expect(req('/assets/app.js', { origin: 'http://elsewhere.test' })).toBe('pass');
  });
});

describe('cacheable', () => {
  it('stores only a 200', () => {
    expect(cacheable(200)).toBe(true);
    for (const status of [206, 301, 304, 401, 404, 500, 503]) expect(cacheable(status), String(status)).toBe(false);
  });
});

describe('serverGone', () => {
  it('takes a proxy no-backend answer for a dead server, and nothing else', () => {
    for (const status of [502, 503, 504]) expect(serverGone(status), String(status)).toBe(true);
    for (const status of [200, 304, 401, 404, 500]) expect(serverGone(status), String(status)).toBe(false);
  });
});

describe('assetUrls', () => {
  const html = `<!doctype html><html><head>
    <link rel="stylesheet" crossorigin href="/assets/app-BJ5O9xz9.css">
    <link rel="manifest" href="/manifest.webmanifest">
    <link rel="icon" href="/icons/icon-192.png">
    <link rel="stylesheet" href="https://cdn.test/assets/theirs.css">
    <script type="module" crossorigin src="/assets/app-D2iJgih4.js"></script>
    <script src="/assets/legacy.js"></script>
    <script src="//elsewhere.test/assets/other.js"></script>
    </head><body></body></html>`;

  it('lists the same-origin files under /assets/ that the page loads, in order, and nothing else', () => {
    expect(assetUrls(html, origin)).toEqual(['/assets/app-BJ5O9xz9.css', '/assets/app-D2iJgih4.js', '/assets/legacy.js']);
  });

  it('is empty for a page that names none', () => {
    expect(assetUrls('<html><body>This device isn\'t paired</body></html>', origin)).toEqual([]);
  });
});
