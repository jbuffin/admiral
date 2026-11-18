import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, Ship } from './api';
import type { ShipCard } from './chat';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc, shipPath } from './router';
import { ships } from './shipsLink';
import type { Surface } from './surfaces';

const nav = vi.hoisted(() => ({ go: vi.fn() }));
vi.mock('./router', async (orig) => ({ ...(await orig<typeof import('./router')>()), go: nav.go }));

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const alpha = { key: 'alpha', name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } } as unknown as Fleet;
const card: ShipCard = {
  ship: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', fleet: 'alpha', skill: 'tdd', skill_kind: 'skill', result: 'moored',
  prs: [{ url: 'https://github.com/acme/api/pull/7', number: 7 }],
};
const live = (over: Partial<Ship> = {}) => ({ session: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', status: 'moored', ...over }) as Ship;

const chatWith = (c: Partial<ShipCard>, text = 'Calm Otter') =>
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session')
      ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: [{ id: 'ship-1', kind: 'ship_card', text, at: '2026-01-02T03:04:05Z', card: { ...card, ...c } }] }))
      : new Response('{}'),
  );

const start = { loc: loc.value, fleets: fleets.value, ships: ships.value };
beforeEach(() => {
  nav.go.mockClear();
  fleets.value = [alpha];
  loc.value = { path: '/app/chat', search: '?fleet=alpha' };
});
afterEach(() => {
  vi.unstubAllGlobals();
  loc.value = start.loc;
  fleets.value = start.fleets;
  ships.value = start.ships;
});

const shown = async () => {
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelector('.chat-card')).not.toBeNull());
  return document.querySelector('.chat-card') as HTMLElement;
};

test('a ship card shows the ship, its kind and its result, with a link to the PR that opens outside the app', async () => {
  chatWith({});
  ships.value = [live()];
  const el = await shown();
  expect(el.textContent).toContain('Calm Otter');
  expect(el.textContent).toContain('tdd');
  const pr = screen.getByRole('link', { name: 'PR #7' });
  expect(pr.getAttribute('href')).toBe('https://github.com/acme/api/pull/7');
  expect(pr.getAttribute('target')).toBe('_blank');
  expect(pr.getAttribute('rel')).toBe('noopener noreferrer');
});

test('a PR whose URL is not https, or not a URL, shows as text with no link', async () => {
  chatWith({ prs: [{ url: 'javascript:alert(1)', number: 7 }, { url: 'http://github.com/acme/api/pull/8', number: 8 }, { url: 'not a url', number: 9 }] });
  ships.value = [live()];
  const el = await shown();
  expect(el.textContent).toContain('javascript:alert(1)');
  expect(el.textContent).toContain('not a url');
  expect(screen.queryByRole('link', { name: /^PR #/ })).toBeNull();
  expect(el.querySelector('a[href^="javascript"], a[href^="http:"], a[href="not a url"]')).toBeNull();
});

test('the ship links to its conversation while its voyage is afloat, and a plain click opens it in the app', async () => {
  chatWith({});
  ships.value = [live()];
  await shown();
  const link = screen.getByRole('link', { name: 'Open ship Calm Otter' });
  expect(link.getAttribute('href')).toContain(shipPath('calm-otter'));
  fireEvent.click(link);
  expect(nav.go).toHaveBeenCalledTimes(1);
  expect(nav.go.mock.calls[0][0]).toBe(shipPath('calm-otter'));
});

test('a modifier-click on the ship link leaves the browser to open it', async () => {
  chatWith({});
  ships.value = [live()];
  await shown();
  const link = screen.getByRole('link', { name: 'Open ship Calm Otter' });
  fireEvent.click(link, { metaKey: true });
  fireEvent.click(link, { ctrlKey: true });
  expect(nav.go).not.toHaveBeenCalled();
});

test('the ship is plain text, with no link, once it has gone or its name was reused by a later voyage', async () => {
  chatWith({});
  ships.value = [live({ first_launched: '2026-02-03T04:05:06Z' })];
  const el = await shown();
  expect(el.textContent).toContain('Calm Otter');
  expect(screen.queryByRole('link', { name: /^Open ship/ })).toBeNull();
  ships.value = [];
  expect(screen.queryByRole('link', { name: /^Open ship/ })).toBeNull();
});

test('a card without a name shows the session slug', async () => {
  chatWith({ name: '' }, '');
  ships.value = [];
  const el = await shown();
  expect(el.textContent).toContain('calm-otter');
});

test('hostile text in the name, skill and PR shows inert', async () => {
  const hostile = '<img src=x onerror="window.__xss=1"><script>window.__xss=1</script>';
  chatWith({ name: hostile, skill: '<b>bold</b>', prs: [{ url: 'https://github.com/a/b/pull/1"><i>x</i>', number: 1 }] }, hostile);
  ships.value = [];
  const el = await shown();
  expect(el.textContent).toContain(hostile);
  expect(el.textContent).toContain('<b>bold</b>');
  expect(el.querySelector('img, script, b, i')).toBeNull();
  expect((window as unknown as { __xss?: number }).__xss).toBeUndefined();
});

test('a result this build does not know shows the card without crashing', async () => {
  chatWith({ result: 'teleported' as ShipCard['result'] });
  ships.value = [live()];
  const el = await shown();
  expect(el.textContent).toContain('Calm Otter');
  expect(el.textContent).not.toContain('teleported');
});
