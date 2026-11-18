// Built-in actions on a module page: Open link, Launch on an issue and the AdmiralBot message.
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Fleet, Source } from './api';
import { fleets } from './fleets';
import { ModulePage } from './ModulePage';
import { loc } from './router';
import { sources } from './shipsLink';
import { ToastRegion } from './ToastRegion';

const start = { value: loc.value, url: location.href };

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sources.value = [];
  fleets.value = [];
  loc.value = start.value;
  history.replaceState({}, '', start.url);
});

const source = (state = 'ready'): Source =>
  ({ key: 'deploys', name: 'Deploys', fleet: 'zulu', views: [], capabilities: ['page'], state: 'ok', page: { state, waiting: 0, version: 1 } }) as unknown as Source;

const ISSUE = 'https://github.com/acme/widgets/issues/7';
const LINK = 'https://tracker.example/t/1';
const docs = { id: 'docs', label: 'Open docs', builtin: 'open', url: 'https://tracker.example/docs' };
const bare = { id: 'bare', label: 'Open runbook', url: 'https://tracker.example/runbook' };
const evil = { id: 'evil', label: 'Open script', builtin: 'open', url: 'javascript:alert(1)' };
// Launch and message carry a confirm value the shell must ignore: it keys on builtin.
const start_ = { id: 'dock', label: 'Start ship', builtin: 'launch', url: ISSUE, confirm: 'form' };
const tell = { id: 'tell', label: 'Message AdmiralBot', builtin: 'message', kind: 'review', ref: 'ACME-1', title: 'Fix login', link: LINK, confirm: 'yesno' };
const archive = { id: 'archive', label: 'Archive', confirm: 'click' };

const pageOf = (state = 'ready') => ({
  state,
  waiting: 0,
  ...(state === 'error' ? { error: 'plugin crashed' } : {}),
  groups: [
    {
      id: 'review',
      title: 'Needs review',
      rows: [
        { id: 'r1', title: 'Fix login', subtitle: '', chips: [], reason: '', actions: [docs, bare, evil, start_, tell] },
        { id: 'r3', title: 'Ship docs', subtitle: '', chips: [], reason: '', action: archive },
      ],
    },
  ],
});

// Markup, backticks and newlines in the title: the confirm shows them as text and sends them whole.
const TEXT = '*From the Deploys:* Review this. Row: ACME-1\n`````\nFix <b>login</b> `` now\n`````\n' + LINK;
const NEWER = '*From the Deploys:* Review this. Row: ACME-1\n`````\nFix login again\n`````\n' + LINK;
const CHAT = '/app/chat?fleet=zulu';

const fleet = (key: string, name: string) => ({ key, name, docks: [{ harness: 'claude' }] }) as unknown as Fleet;

type Answer = { status?: number; body: unknown };
interface Handlers {
  state?: () => string;
  launch?: () => Answer;
  preview?: () => Answer;
  send?: (body: { text: string }) => Answer;
  ships?: () => Answer;
}
interface Seen {
  path: string;
  method: string;
  body: unknown;
}

const json = (a: Answer) => new Response(JSON.stringify(a.body), { status: a.status ?? 200 });

function server(h: Handlers = {}) {
  const seen: Seen[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const method = init.method ?? 'GET';
      const route = new URL(path, 'http://admiral.test').pathname.replace('/api/v1', '');
      const body = init.body ? JSON.parse(String(init.body)) : undefined;
      seen.push({ path: route, method, body });
      if (route === '/sources/deploys/page') return new Response(JSON.stringify(pageOf(h.state?.() ?? 'ready')));
      if (/\/actions\/dock\/launch$/.test(route)) return json((h.launch ?? (() => ({ body: { url: ISSUE, fleet: 'alpha' } })))());
      if (/\/actions\/tell\/message$/.test(route))
        return method === 'GET' ? json((h.preview ?? (() => ({ body: { text: TEXT, fleet: 'zulu', chat: CHAT } })))()) : json((h.send ?? (() => ({ body: { sent: true, chat: CHAT } })))(body));
      if (route === '/launch-orders/dirs') return new Response(JSON.stringify({ entries: [{ path: '/p', name: 'p', kind: 'checkout', ships_here: 0 }], selected: '/p', clone: null }));
      if (route === '/launch-orders/skills') return new Response(JSON.stringify({ mark: '/', commodore: { found: false, name: 'commodore', state: 'missing' }, skills: [] }));
      if (route === '/launch-orders/crew') return new Response(JSON.stringify({ members: [], warnings: [], errors: [] }));
      if (route === '/ships' && method === 'POST') return json((h.ships ?? (() => ({ status: 201, body: { ship: { session: 'alpha-1' } } })))());
      return new Response('{}', { status: 404 });
    }),
  );
  return seen;
}

const posts = (seen: Seen[], tail: string) => seen.filter((s) => s.method === 'POST' && s.path.endsWith(tail));
const gets = (seen: Seen[], tail: string) => seen.filter((s) => s.method === 'GET' && s.path.endsWith(tail));
const BASE = '/sources/deploys/rows/r1/actions';

const open = (state = 'ready') => {
  fleets.value = [fleet('zulu', 'Westwind'), fleet('alpha', 'Homeport')];
  sources.value = [source(state)];
  return render(
    <>
      <ModulePage sourceKey="deploys" title />
      <ToastRegion />
    </>,
  );
};
const rowItem = async (title = 'Fix login') => (await screen.findByRole('button', { name: `Open ${title}` })).closest('li') as HTMLElement;
const button = (scope: HTMLElement, name: string) => within(scope).getByRole('button', { name }) as HTMLButtonElement;
const press = (scope: HTMLElement, name: string) => fireEvent.click(button(scope, name));

// Open link

test('Open link is a link that opens its http(s) address in a new tab with rel noopener noreferrer, with or without builtin, and runs nothing', async () => {
  const seen = server();
  open();
  const row = await rowItem();
  for (const [name, href] of [['Open docs', docs.url], ['Open runbook', bare.url]]) {
    const a = within(row).getByRole('link', { name }) as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe(href);
    expect(a.target).toBe('_blank');
    expect(a.rel.split(/\s+/)).toEqual(expect.arrayContaining(['noopener', 'noreferrer']));
    fireEvent.click(a);
  }
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(seen.filter((s) => s.method !== 'GET')).toHaveLength(0);
});

test('Open link never links a javascript: address', async () => {
  server();
  open();
  const row = await rowItem();
  // The same row's good link is there, so the row has drawn its actions.
  await waitFor(() => expect(within(row).getByRole('link', { name: 'Open docs' })).toBeTruthy());
  expect(row.querySelector('a[href^="javascript"]')).toBeNull();
});

// Launch

test('Launch asks the launch route, not the module action routes, whatever confirm the plugin declared', async () => {
  const seen = server();
  open();
  press(await rowItem(), 'Start ship');
  await screen.findByRole('dialog', { name: 'Launch' });
  expect(gets(seen, `${BASE}/dock/launch`)).toHaveLength(1);
  expect(seen.filter((s) => /\/(form|run)$/.test(s.path))).toHaveLength(0);
});

test('Launch opens the normal launch dialog in the fleet the issue routes to, and launches with issues and that fleet and no order', async () => {
  const seen = server();
  open();
  press(await rowItem(), 'Start ship');
  const dialog = await screen.findByRole('dialog', { name: 'Launch' });
  // The routed fleet (alpha), not the module's own (zulu).
  expect(within(dialog).getByText('Launches in Homeport.')).toBeTruthy();
  const go = within(dialog).getByRole('button', { name: 'Launch' }) as HTMLButtonElement;
  await waitFor(() => expect(go.disabled).toBe(false));
  fireEvent.click(go);
  await waitFor(() => expect(posts(seen, '/ships')).toHaveLength(1));
  const body = posts(seen, '/ships')[0].body as Record<string, unknown>;
  expect(body).toMatchObject({ fleet: 'alpha', issues: [ISSUE] });
  expect(body).not.toHaveProperty('source');
  expect(body).not.toHaveProperty('order_id');
  expect(body).not.toHaveProperty('anyway');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});

test.each([
  [409, 'The issue already has a ship: alpha-3'],
  [400, 'The issue is not a GitHub issue this fleet uses'],
  [503, 'Admiral cannot read the ships'],
])('a %i from the launch route shows its message, opens no dialog and reads nothing for one', async (status, error) => {
  const seen = server({ launch: () => ({ status, body: { error } }) });
  open();
  press(await rowItem(), 'Start ship');
  expect(await screen.findByText(error)).toBeTruthy();
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(seen.filter((s) => s.path.startsWith('/launch-orders') || s.path === '/ships')).toHaveLength(0);
});

test('a launch the server refuses with 409 shows its message in the dialog and offers no anyway', async () => {
  server({ ships: () => ({ status: 409, body: { error: 'The issue already has a ship' } }) });
  open();
  press(await rowItem(), 'Start ship');
  const dialog = await screen.findByRole('dialog', { name: 'Launch' });
  const go = within(dialog).getByRole('button', { name: 'Launch' }) as HTMLButtonElement;
  await waitFor(() => expect(go.disabled).toBe(false));
  fireEvent.click(go);
  expect(await within(dialog).findByText('The issue already has a ship')).toBeTruthy();
  expect(within(dialog).queryByRole('button', { name: /anyway/i })).toBeNull();
});

// Message AdmiralBot

test('Message AdmiralBot fetches the preview and shows its whole text, as text, in a yes/no confirm before anything is sent', async () => {
  const seen = server();
  open();
  press(await rowItem(), 'Message AdmiralBot');
  const dialog = await screen.findByRole('dialog');
  expect(gets(seen, `${BASE}/tell/message`)).toHaveLength(1);
  expect(dialog.textContent).toContain(TEXT);
  expect(dialog.querySelector('b')).toBeNull();
  expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeTruthy();
  expect(posts(seen, '/message')).toHaveLength(0);
});

test('Cancel sends nothing', async () => {
  const seen = server();
  open();
  press(await rowItem(), 'Message AdmiralBot');
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(dialog.textContent).toContain(TEXT));
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(posts(seen, '/message')).toHaveLength(0);
});

test('Yes posts exactly the text the confirm showed, and the page says Sent to AdmiralBot with a link to the chat', async () => {
  const seen = server();
  open();
  press(await rowItem(), 'Message AdmiralBot');
  const dialog = await screen.findByRole('dialog');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Message AdmiralBot' }));
  await waitFor(() => expect(posts(seen, `${BASE}/tell/message`)).toHaveLength(1));
  expect(posts(seen, `${BASE}/tell/message`)[0].body).toEqual({ text: TEXT });
  expect(await screen.findByText('Sent to AdmiralBot')).toBeTruthy();
  expect(document.querySelector(`a[href="${CHAT}"]`)).not.toBeNull();
  expect(screen.queryByRole('dialog')).toBeNull();
});

test('a 409 on the send fetches the preview again, shows the new text and sends nothing until it is confirmed again', async () => {
  let sends = 0;
  let previews = 0;
  const seen = server({
    preview: () => ({ body: { text: previews++ === 0 ? TEXT : NEWER, fleet: 'zulu', chat: CHAT } }),
    send: () => (sends++ === 0 ? { status: 409, body: { error: 'The message changed since it was shown; ask again' } } : { body: { sent: true, chat: CHAT } }),
  });
  open();
  press(await rowItem(), 'Message AdmiralBot');
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Message AdmiralBot' }));
  await waitFor(() => expect(gets(seen, '/message')).toHaveLength(2));
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(dialog.textContent).toContain(NEWER));
  expect(dialog.textContent).not.toContain('Fix <b>login</b>');
  expect(posts(seen, '/message')).toHaveLength(1);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Message AdmiralBot' }));
  await waitFor(() => expect(posts(seen, '/message')).toHaveLength(2));
  expect(posts(seen, '/message')[1].body).toEqual({ text: NEWER });
  expect(await screen.findByText('Sent to AdmiralBot')).toBeTruthy();
});

test.each([
  [404, 'No such fleet'],
  [502, 'The paste into the chat failed'],
])("the chat's own %i on the send shows its message, and the page does not say it was sent", async (status, error) => {
  server({ send: () => ({ status, body: { error } }) });
  open();
  press(await rowItem(), 'Message AdmiralBot');
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Message AdmiralBot' }));
  expect(await screen.findByText(error)).toBeTruthy();
  expect(screen.queryByText('Sent to AdmiralBot')).toBeNull();
});

test('a preview the server refuses shows its message and offers no yes', async () => {
  const seen = server({ preview: () => ({ status: 409, body: { error: 'The page is in error, so its message actions are off' } }) });
  open();
  press(await rowItem(), 'Message AdmiralBot');
  expect(await screen.findByText('The page is in error, so its message actions are off')).toBeTruthy();
  const dialog = screen.queryByRole('dialog');
  if (dialog) expect(within(dialog).queryByRole('button', { name: 'Message AdmiralBot' })).toBeNull();
  expect(posts(seen, '/message')).toHaveLength(0);
});

// The error state

test('while the page is in error Open link and Launch stay enabled, and the message and module actions are disabled', async () => {
  const seen = server({ state: () => 'error' });
  open('error');
  await screen.findByText('plugin crashed');
  const row = await rowItem();
  expect(within(row).getByRole('link', { name: 'Open docs' }).getAttribute('href')).toBe(docs.url);
  expect(button(row, 'Start ship').disabled).toBe(false);
  expect(button(row, 'Message AdmiralBot').disabled).toBe(true);
  expect(button(await rowItem('Ship docs'), 'Archive').disabled).toBe(true);

  press(row, 'Message AdmiralBot');
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(gets(seen, '/message')).toHaveLength(0);

  // Launch still works: its route answers from the stored page.
  press(row, 'Start ship');
  await screen.findByRole('dialog', { name: 'Launch' });
  expect(gets(seen, `${BASE}/dock/launch`)).toHaveLength(1);
});

test.each(['ready', 'auth_required'])('the message and Launch are enabled when the page is %s', async (state) => {
  server({ state: () => state });
  open(state);
  const row = await rowItem();
  expect(button(row, 'Message AdmiralBot').disabled).toBe(false);
  expect(button(row, 'Start ship').disabled).toBe(false);
});
