import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { clickUrl, notice } from './swPush';

const manifest = JSON.parse(readFileSync(new URL('../../public/manifest.webmanifest', import.meta.url), 'utf8')) as { icons: { src: string }[] };
const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';
const origin = 'https://admiral.test';

describe('the notification a push shows', () => {
  const payload = { name: 'Dory', line: 'wants to run Bash', tag: 's1', fleet: 'f', fleet_name: 'Westwind', target: 'peek', session: 's1' };

  it('names the ship in the title, says the line, carries the tag, an app icon and the payload', () => {
    const n = notice(JSON.stringify(payload));
    expect(n.title).toContain('Dory');
    expect(n.options.body).toBe('wants to run Bash');
    expect(n.options.tag).toBe('s1');
    expect(manifest.icons.map((i) => i.src)).toContain(n.options.icon);
    expect(n.options.data).toMatchObject(payload);
  });

  it('shows hostile text exactly as text', () => {
    const n = notice(JSON.stringify({ ...payload, name: HOSTILE, line: HOSTILE, session: HOSTILE, item: HOSTILE, crew: HOSTILE }));
    expect(n.title).toContain(HOSTILE);
    expect(n.options.body).toBe(HOSTILE);
    expect(n.options.data).toMatchObject({ session: HOSTILE, item: HOSTILE, crew: HOSTILE });
  });

  it('carries the routine a tap opens', () => {
    const n = notice(JSON.stringify({ name: 'Nightly sweep', line: 'failed to run', tag: 'routine:r1:t1', fleet: 'f', target: 'routine', routine: 'r1' }));
    expect(n.options.data).toMatchObject({ target: 'routine', routine: 'r1' });
  });

  it('still shows a notification for a payload that does not parse, or none', () => {
    for (const raw of ['not json', '', null, '[1,2]', '"text"', 'null']) {
      const n = notice(raw);
      expect(n.title, String(raw)).not.toBe('');
      expect(typeof n.options.body, String(raw)).toBe('string');
    }
  });

  it('treats a field that is not text as absent', () => {
    const n = notice(JSON.stringify({ name: { x: 1 }, line: 7, tag: ['a'], session: 5 }));
    expect(n.title).not.toBe('');
    expect(typeof n.options.body).toBe('string');
  });
});

describe('where a tap on the notification opens', () => {
  const hostile = [
    { session: '//evil.test/x' },
    { session: 'https://evil.test' },
    { session: '../../api' },
    { session: 's1', item: 'a?b=c#d' },
    { session: 's1', item: '../../../api/v1/ships' },
    { session: 's1', crew: '%2e%2e/%2e%2e/api' },
    { session: '/\\evil.test', crew: '\\..\\..' },
    { session: 'javascript:alert(1)' },
  ];

  it('stays on this origin under /app/ whatever session, item or crew the payload says', () => {
    for (const data of hostile) {
      const url = new URL(clickUrl(data), origin);
      expect(url.origin, JSON.stringify(data)).toBe(origin);
      expect(url.pathname.startsWith('/app/'), `${JSON.stringify(data)} -> ${url.pathname}`).toBe(true);
    }
  });

  it('opens under /app/ for a payload with no fields at all', () => {
    const url = new URL(clickUrl({}), origin);
    expect(url.origin).toBe(origin);
    expect(url.pathname.startsWith('/app/')).toBe(true);
  });
});
