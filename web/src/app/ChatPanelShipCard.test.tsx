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

test('a done card names its branch as plain text', async () => {
  chatWith({ result: 'done', prs: [], branch: 'admiral/calm-otter<b>x</b>' });
  const el = await shown();
  expect(el.textContent).toContain('Done');
  expect(el.querySelector('code')?.textContent).toBe('admiral/calm-otter<b>x</b>');
  expect(el.querySelector('b')).toBeNull();
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

test('the ship link goes once the ship leaves the list, and a later voyage that reused the name gets none', async () => {
  chatWith({});
  ships.value = [live()];
  await shown();
  expect(screen.getByRole('link', { name: 'Open ship Calm Otter' })).not.toBeNull();
  ships.value = [live({ first_launched: '2026-02-03T04:05:06Z' })];
  await waitFor(() => expect(screen.queryByRole('link', { name: /^Open ship/ })).toBeNull());
  ships.value = [live()];
  await waitFor(() => expect(screen.queryByRole('link', { name: 'Open ship Calm Otter' })).not.toBeNull());
  ships.value = [];
  await waitFor(() => expect(screen.queryByRole('link', { name: /^Open ship/ })).toBeNull());
  expect(document.querySelector('.chat-card')?.textContent).toContain('Calm Otter');
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

test("a ship card shows the ship's title beside its name", async () => {
  chatWith({ title: 'Fix the login redirect' });
  ships.value = [live()];
  const el = await shown();
  expect(el.textContent).toContain('Calm Otter');
  expect(el.textContent).toContain('Fix the login redirect');
});

test.each([[undefined], ['']])('a ship card with title %j shows the name alone', async (title) => {
  chatWith({ title });
  ships.value = [live()];
  const el = await shown();
  expect(el.textContent).toContain('Calm Otter');
  expect(el.textContent).not.toContain('undefined');
});

test('a ship card shows a title as text, never as markup', async () => {
  const title = '<img src=x onerror="window.__xss=1"> <b>bold</b>';
  chatWith({ title });
  ships.value = [live()];
  const el = await shown();
  expect(el.textContent).toContain(title);
  expect(el.querySelector('img, b')).toBeNull();
});

const twoCards = (cards: Partial<ShipCard>[], findingsBody = '# Found it') => {
  const calls: string[] = [];
  vi.stubGlobal('fetch', async (url: string) => {
    calls.push(String(url));
    if (url.endsWith('/session')) {
      const messages = cards.map((c, i) => ({ id: `ship-${i}`, kind: 'ship_card', text: 'x', at: '2026-01-02T03:04:05Z', card: { ...card, ...c } }));
      return new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages }));
    }
    return new Response(JSON.stringify({ ship: 'calm-otter', first_launched: '', fleet: 'alpha', findings: findingsBody }));
  });
  return calls;
};

test('only the card whose voyage has findings shows a Findings button', async () => {
  twoCards([{ ship: 'calm-otter', name: 'Calm Otter', findings: true }, { ship: 'calm-lark', name: 'Calm Lark' }]);
  ships.value = [];
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelectorAll('.chat-card')).toHaveLength(2));
  const buttons = screen.getAllByRole('button', { name: 'Findings' });
  expect(buttons).toHaveLength(1);
  expect(buttons[0].closest('.chat-card')?.textContent).toContain('Calm Otter');
});

test('tapping Findings opens the sheet and Close shuts it', async () => {
  twoCards([{ findings: true }]);
  ships.value = [];
  render(<ChatPanel surface={surface} title phone />);
  fireEvent.click(await screen.findByRole('button', { name: 'Findings' }));
  expect(await screen.findByRole('dialog', { name: 'Findings' })).not.toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});

test('cards of a reused ship name each open their own voyage', async () => {
  const calls = twoCards([{ first_launched: '2026-01-02T03:04:05Z', findings: true }, { first_launched: '2026-03-04T05:06:07Z', findings: true }]);
  ships.value = [];
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(screen.getAllByRole('button', { name: 'Findings' })).toHaveLength(2));
  fireEvent.click(screen.getAllByRole('button', { name: 'Findings' })[1]);
  await screen.findByRole('dialog');
  const url = new URL(calls.find((c) => c.includes('/findings')) ?? '', 'http://admiral.test');
  expect(url.searchParams.get('first_launched')).toBe('2026-03-04T05:06:07Z');
});

test('a scout without findings shows Failed and no Findings button', async () => {
  chatWith({ result: 'failed', skill_kind: 'scout', skill: '' });
  ships.value = [];
  const el = await shown();
  expect(el.textContent).toContain('Failed');
  expect(el.textContent).not.toContain('Result unknown');
  expect(screen.queryByRole('button', { name: 'Findings' })).toBeNull();
});
