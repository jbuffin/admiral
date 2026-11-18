import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import { fleets } from './fleets';
import type { Entry } from './inbox';
import { Peek } from './Peek';
import { go } from './router';
import { ships } from './shipsLink';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const fleetOf = (key: string, name: string) => ({ key, name, color: '#336699', ink: '#fff', docks: [{ harness: 'claude' }] }) as unknown as Fleet;

interface Sent {
  path: string;
  method: string;
}
// A button's pending key outlives its test by the spinner's minimum, so each test gets its own item id.
let seq = 0;
let id: string;
let sent: Sent[];
let page: (before: string | null) => Response | Promise<Response>;

beforeEach(() => {
  id = `drafted-${++seq}`;
  sent = [];
  ships.value = [];
  fleets.value = [fleetOf('zulu', 'Westwind'), fleetOf('alpha', 'Homeport')];
  go('/app/inbox', '?fleet=zulu');
  page = () => json({ state: 'ok', more: false, items: [{ id: 'u1', kind: 'user', text: 'Draft a campaign for the parser' }, { id: 'a1', kind: 'assistant', text: 'I could not reach **GitHub**.' }] });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const url = new URL(path, 'http://admiral.test');
      const route = url.pathname.replace('/api/v1', '');
      const method = init.method ?? 'GET';
      sent.push({ path: route + url.search, method });
      if (route === `/inbox/_admiral/${id}/conversation`) return page(url.searchParams.get('before'));
      if (route === `/inbox/_admiral/${id}/remove`) return json({});
      if (/^\/fleets\/[^/]+\/campaigns\/search$/.test(route)) return json({ results: [] });
      throw new Error(`unstubbed ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const entry = (name = 'Saltpaw'): Entry =>
  ({
    key: `item:_admiral/${id}`,
    type: 'item',
    session: '_admiral',
    id,
    folded: null,
    since: 0,
    fleet: { key: 'alpha', name: 'Homeport', color: '#2a9d8f' },
    item: {
      id,
      kind: 'drafting_ended',
      name: 'Admiral',
      title: `${name} ended without filing a campaign`,
      fleet: 'alpha',
      fleet_name: 'Homeport',
      drafting_ended: { ship: 'saltpaw', name, fleet: 'alpha', harness: 'claude' },
    },
  }) as unknown as Entry;

const open = (e: Entry = entry(), fold = vi.fn()) => ({ fold, ...render(<Peek entry={e} fold={fold} />) });
const convoRequests = () => sent.filter((s) => s.path.includes('/conversation'));

test('the Peek heading reads Admiral, a drafting ship ended, with the ship name in italics from the item data', async () => {
  const name = '<b>Salt</b> *paw*';
  open(entry(name));
  expect(document.querySelector('.peek-who h2')!.textContent!.replace(/\s+/g, ' ').trim()).toBe('Admiral a drafting ship ended');
  const title = document.querySelector('.peek-title')!;
  expect(title.querySelector('em')!.textContent).toBe(name);
  expect(title.querySelector('b')).toBeNull();
  expect(title.textContent!.trim()).toBe(`${name} ended without filing a campaign`);
});

test("the Peek reads the item's conversation route, never the ship route, and shows it read-only", async () => {
  open();
  await screen.findByText('Draft a campaign for the parser');
  expect(document.querySelector('.msg.assistant strong')!.textContent).toBe('GitHub');
  expect(convoRequests().map((s) => s.path)).toEqual([`/inbox/_admiral/${id}/conversation`]);
  expect(sent.some((s) => s.path.startsWith('/ships/'))).toBe(false);
  expect(screen.queryByRole('textbox')).toBeNull();
  expect(screen.queryByRole('button', { name: /send|interrupt/i })).toBeNull();
});

test('Load earlier messages asks the item route for the page before the first item', async () => {
  page = (before) =>
    before === null
      ? json({ state: 'ok', more: true, items: [{ id: 'u2', kind: 'user', text: 'Newest question' }] })
      : json({ state: 'ok', more: false, items: [{ id: 'u1', kind: 'user', text: 'Oldest question' }] });
  open();
  await screen.findByText('Newest question');
  fireEvent.click(screen.getByRole('button', { name: 'Load earlier messages' }));
  await screen.findByText('Oldest question');
  expect(convoRequests().map((s) => s.path)).toEqual([`/inbox/_admiral/${id}/conversation`, `/inbox/_admiral/${id}/conversation?before=u2`]);
  expect(screen.queryByRole('button', { name: 'Load earlier messages' })).toBeNull();
});

test('a conversation Admiral cannot read says so, with no Open terminal', async () => {
  page = () => json({ state: 'none', more: false, items: [] });
  open();
  await screen.findByText('No conversation recorded');
  expect(screen.queryByRole('button', { name: 'Open terminal' })).toBeNull();
  cleanup();
  page = () => json({ state: 'unreadable', more: false, items: [] });
  open();
  await screen.findByText("Admiral can't read this conversation");
  expect(screen.queryByRole('button', { name: 'Open terminal' })).toBeNull();
});

test('a conversation that is gone (404) says so', async () => {
  page = () => json({ error: 'not found' }, 404);
  open();
  await screen.findByText('Conversation no longer available');
});

test('Follow an issue… opens Add campaign for the ended ship\'s fleet, not the active fleet, and follows nothing by itself', async () => {
  open();
  await screen.findByText('Draft a campaign for the parser');
  fireEvent.click(screen.getByRole('button', { name: 'Follow an issue…' }));
  expect(await screen.findByRole('dialog', { name: 'Add campaign' })).toBeTruthy();
  await waitFor(() => expect(sent.some((s) => s.path.startsWith('/fleets/alpha/campaigns/search'))).toBe(true));
  expect(sent.some((s) => s.path.startsWith('/fleets/zulu/'))).toBe(false);
  expect(sent.some((s) => s.method === 'POST')).toBe(false);
  expect(location.pathname).toBe('/app/inbox');
});

test('Follow an issue… is offered when the conversation is gone too', async () => {
  page = () => json({ error: 'not found' }, 404);
  open();
  await screen.findByText('Conversation no longer available');
  expect(screen.getByRole('button', { name: 'Follow an issue…' })).toBeTruthy();
});

test("Remove from inbox is offered with its own confirm copy, and Remove sends one remove and folds the row", async () => {
  const { fold } = open();
  fireEvent.click(screen.getByRole('button', { name: 'Remove from inbox' }));
  const copy = document.querySelector('.inbox-ask.discard p')!;
  expect(copy.textContent).not.toMatch(/never gets an answer/);
  fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
  await waitFor(() => expect(fold).toHaveBeenCalledTimes(1));
  expect(sent.filter((s) => s.method === 'POST').map((s) => s.path)).toEqual([`/inbox/_admiral/${id}/remove`]);
});

test('a conversation that reads none is read once, not asked about again', async () => {
  page = () => json({ state: 'none', more: false, items: [] });
  open();
  await screen.findByText('No conversation recorded');
  vi.useFakeTimers();
  try {
    await vi.advanceTimersByTimeAsync(10_000);
  } finally {
    vi.useRealTimers();
  }
  expect(convoRequests()).toHaveLength(1);
});
