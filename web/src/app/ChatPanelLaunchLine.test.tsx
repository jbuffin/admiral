import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, Ship } from './api';
import type { LaunchLine } from './chat';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc, shipPath } from './router';
import { ships } from './shipsLink';
import type { Surface } from './surfaces';

const nav = vi.hoisted(() => ({ go: vi.fn() }));
vi.mock('./router', async (orig) => ({ ...(await orig<typeof import('./router')>()), go: nav.go }));

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const alpha = { key: 'alpha', name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } } as unknown as Fleet;
const card: LaunchLine = { ship: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', fleet: 'alpha', kind: 'quick_ship', reason: 'to fix the 500 in widgets', text: 'Read the logs and fix the 500.' };
const live = (over: Partial<Ship> = {}) => ({ session: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', status: 'working', ...over }) as Ship;

const chatWith = (c: Partial<LaunchLine>) =>
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session')
      ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: [{ id: 'launch-line-calm-otter-1', kind: 'launch_line', text: 'calm-otter', at: '2026-01-02T03:04:05Z', card: { ...card, ...c } }] }))
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

const line = async (needle: string) => {
  render(<ChatPanel surface={surface} title phone />);
  const main = screen.getByRole('main');
  await waitFor(() => expect(main.textContent).toContain(needle));
  return main;
};
const toggle = () => screen.getByRole('button', { name: /launch text/i });

test.each([
  ['quick_ship', 'Sent a quick ship to fix the 500 in widgets'],
  ['scout', 'Sent a scout to fix the 500 in widgets'],
  ['commodore', 'Sent a Commodore ship to fix the 500 in widgets'],
  ['tdd', 'Sent a tdd ship to fix the 500 in widgets'],
  ['improve-codebase', 'Sent an improve-codebase ship to fix the 500 in widgets'],
])('a %s launch reads "%s", with the reason as written', async (kind, wording) => {
  chatWith({ kind });
  ships.value = [live()];
  const el = await line('Sent a');
  expect(el.textContent).toContain(wording);
});

test('a launch with no reason reads without a trailing space', async () => {
  chatWith({ reason: '' });
  ships.value = [live()];
  await line('Sent a');
  expect(document.querySelector('.launch-sentence')?.textContent).toBe('Sent a quick ship');
});

test('the launch text is behind a closed disclosure that a click opens and shows exactly', async () => {
  chatWith({});
  ships.value = [live()];
  await line('Sent a');
  expect(toggle().getAttribute('aria-expanded')).toBe('false');
  expect(screen.queryByText(card.text)).toBeNull();
  fireEvent.click(toggle());
  expect(toggle().getAttribute('aria-expanded')).toBe('true');
  expect(screen.getByText(card.text).textContent).toBe(card.text);
});

test('launch text shows as typed: markup is not interpreted', async () => {
  const text = '<img src=x onerror=alert(1)> **bold** [link](https://example.com)';
  chatWith({ text });
  ships.value = [live()];
  const el = await line('Sent a');
  fireEvent.click(toggle());
  expect(screen.getByText(text).textContent).toBe(text);
  expect(el.querySelector('img, strong, a[href="https://example.com"]')).toBeNull();
});

test('a launch with no launch text has no disclosure', async () => {
  chatWith({ text: '' });
  ships.value = [live()];
  await line('Sent a');
  expect(screen.queryByRole('button', { name: /launch text/i })).toBeNull();
  expect(document.querySelector('[aria-expanded]')).toBeNull();
});

test("the ship's name links to it while it is afloat", async () => {
  chatWith({});
  ships.value = [live()];
  await line('Sent a');
  const link = screen.getByRole('link', { name: /Calm Otter/ });
  expect(link.getAttribute('href')).toContain(shipPath('calm-otter'));
  fireEvent.click(link);
  expect(nav.go.mock.calls[0][0]).toBe(shipPath('calm-otter'));
});

test('a ship with the same session but another first_launched shows its name with no link', async () => {
  chatWith({});
  ships.value = [live({ first_launched: '2026-02-03T04:05:06Z' })];
  const el = await line('Sent a');
  expect(el.textContent).toContain('Calm Otter');
  expect(screen.queryByRole('link', { name: /Calm Otter/ })).toBeNull();
});

test('a ship no longer afloat shows its name with no link', async () => {
  chatWith({});
  ships.value = [];
  const el = await line('Sent a');
  expect(el.textContent).toContain('Calm Otter');
  expect(screen.queryByRole('link', { name: /Calm Otter/ })).toBeNull();
});

test('a commitment card renders its text and reason as markdown', async () => {
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session')
      ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: [{ id: 'commitment-c1-cant_run-ab12cd34', kind: 'commitment', text: 'Run **the sweep** nightly', at: '2026-01-02T03:04:06Z', card: { commitment: 'c1', outcome: 'cant_run', reason: 'no dock for `codex`' } }] }))
      : new Response('{}'),
  );
  const main = await line("AdmiralBot can't run this");
  expect(main.querySelector('strong')!.textContent).toBe('the sweep');
  expect(main.querySelector('code')!.textContent).toBe('codex');
  expect(main.textContent).not.toContain('**');
});
