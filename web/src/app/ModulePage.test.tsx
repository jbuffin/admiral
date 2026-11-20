// A module's page: its states, the restart, and the refetch when the source's page version moves.
import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Source } from './api';
import { ModulePage, ModuleSkeleton } from './ModulePage';
import { loc } from './router';
import { sources } from './shipsLink';

const start = { value: loc.value, url: location.href };

afterEach(() => {
  vi.unstubAllGlobals();
  sources.value = [];
  loc.value = start.value;
  history.replaceState({}, '', start.url);
});

const source = (version: number, state = 'ok'): Source =>
  ({ key: 'deploys', name: 'Deploys', fleet: 'zulu', views: [], capabilities: ['page'], state, page: { state: 'ready', waiting: 0, version } }) as unknown as Source;

const good = { state: 'ready', waiting: 2, groups: [{ id: 'g1', title: 'Needs review', rows: [{ id: 'r1', title: 'Row one', subtitle: 'sub', chips: [], reason: '' }, { id: 'r2', title: 'Row two', subtitle: '', chips: [], reason: '' }] }] };

interface Call {
  method: string;
  path: string;
}

// Serves `body` for the page; every other request is recorded.
function server(body: () => unknown) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push({ method, path });
      if (method === 'GET' && path.endsWith('/page')) return new Response(JSON.stringify(body()));
      return new Response('{}');
    }),
  );
  return calls;
}

const pageGets = (calls: Call[]) => calls.filter((c) => c.method === 'GET' && c.path.endsWith('/page'));

const open = () => render(<ModulePage sourceKey="deploys" title />);

test('asks for the page of the source, its key encoded, when it opens', async () => {
  const calls = server(() => good);
  sources.value = [{ ...source(1), key: 'my deploys/x' }];
  render(<ModulePage sourceKey="my deploys/x" title />);
  await waitFor(() => expect(pageGets(calls)).toHaveLength(1));
  expect(pageGets(calls)[0].path).toBe('/api/v1/sources/my%20deploys%2Fx/page');
});

test('shows a skeleton while the plugin is starting, and no group', async () => {
  server(() => ({ state: 'starting', waiting: 0, groups: [] }));
  sources.value = [source(0)];
  open();
  expect(await screen.findByRole('status', { name: /loading/i })).toBeTruthy();
  expect(screen.queryByText('Needs review')).toBeNull();
});

test('shows the group titles and row titles as plain text when ready', async () => {
  server(() => ({ ...good, groups: [{ id: 'g1', title: '<b>Needs review</b>', rows: [{ id: 'r1', title: '<img src=x onerror=alert(1)>', subtitle: '', chips: [], reason: '' }] }] }));
  sources.value = [source(1)];
  const { container } = open();
  expect(await screen.findByText('<b>Needs review</b>')).toBeTruthy();
  expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeTruthy();
  expect(container.querySelector('img, b')).toBeNull();
  expect(screen.queryByRole('status', { name: /loading/i })).toBeNull();
});

test('shows a sign-in card from the auth message, a web link and the code', async () => {
  server(() => ({ state: 'auth_required', waiting: 0, groups: [], auth: { message: 'Sign in to Deploys', url: 'https://deploys.example/device', code: 'ABCD-1234' } }));
  sources.value = [source(1, 'auth_required')];
  open();
  expect(await screen.findByText('Sign in to Deploys')).toBeTruthy();
  const link = screen.getByRole('link', { name: 'https://deploys.example/device' }) as HTMLAnchorElement;
  expect(link.getAttribute('href')).toBe('https://deploys.example/device');
  expect(link.getAttribute('rel')).toContain('noopener');
  expect(screen.getByText('ABCD-1234')).toBeTruthy();
});

test('makes no link of an auth url that is not http or https', async () => {
  server(() => ({ state: 'auth_required', waiting: 0, groups: [], auth: { message: 'Sign in', url: 'javascript:alert(1)', code: '' } }));
  sources.value = [source(1, 'auth_required')];
  open();
  await screen.findByText('Sign in');
  expect(screen.queryByRole('link')).toBeNull();
});

test('keeps the last good data beside the error text, with a Restart that restarts the source', async () => {
  const calls = server(() => ({ ...good, state: 'error', error: 'plugin crashed: exit 2' }));
  sources.value = [source(1, 'error')];
  open();
  expect(await screen.findByText('plugin crashed: exit 2')).toBeTruthy();
  expect(screen.getByText('Row one')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Restart' }));
  await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/sources/deploys/restart')).toBe(true));
});

test('shows the error with no rows when there has been no good page', async () => {
  server(() => ({ state: 'error', waiting: 0, groups: [], error: 'boom' }));
  sources.value = [source(1, 'error')];
  open();
  expect(await screen.findByText('boom')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Restart' })).toBeTruthy();
});

test('asks for the page again when the source\'s page version moves, once per move', async () => {
  let body: unknown = good;
  const calls = server(() => body);
  sources.value = [source(1)];
  open();
  await screen.findByText('Row one');
  expect(pageGets(calls)).toHaveLength(1);

  // A new message with the same version must not count as a change: the total below is exactly two.
  sources.value = [source(1)];

  body = { ...good, groups: [{ id: 'g1', title: 'Needs review', rows: [{ id: 'r3', title: 'Row three', subtitle: '', chips: [], reason: '' }] }] };
  sources.value = [source(2)];
  expect(await screen.findByText('Row three')).toBeTruthy();
  expect(pageGets(calls)).toHaveLength(2);
  expect(screen.queryByText('Row one')).toBeNull();
});

test('shows an error instead of an endless skeleton when the first fetch fails', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'plugin unreachable' }), { status: 502 })));
  sources.value = [source(1)];
  open();
  expect((await screen.findByRole('alert')).textContent).toContain('plugin unreachable');
  expect(screen.queryByRole('status', { name: /loading/i })).toBeNull();
});

test('shows a line when Restart is refused', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) =>
      init?.method === 'POST' ? new Response(JSON.stringify({ error: 'restart refused' }), { status: 500 }) : new Response(JSON.stringify({ state: 'error', waiting: 0, groups: [], error: 'boom' })),
    ),
  );
  sources.value = [source(1, 'error')];
  open();
  fireEvent.click(await screen.findByRole('button', { name: 'Restart' }));
  expect(await screen.findByText('restart refused')).toBeTruthy();
});

test('the pending skeleton names the module and fetches nothing', () => {
  const calls = server(() => good);
  render(<ModuleSkeleton name="my deploys/x" title />);
  expect(screen.getByRole('heading', { name: 'my deploys/x' })).toBeTruthy();
  expect(screen.getByRole('status', { name: /loading/i })).toBeTruthy();
  expect(calls).toHaveLength(0);
});

// Serves the page and the restart; the refresh POST answers through `refresh`.
function refreshServer(refresh: () => Promise<Response>, body: () => unknown = () => good) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push({ method, path });
      if (method === 'GET' && path.endsWith('/page')) return new Response(JSON.stringify(body()));
      if (method === 'POST' && path.endsWith('/page/refresh')) return refresh();
      if (method === 'POST' && path.endsWith('/restart')) return new Response(JSON.stringify({ error: 'restart refused' }), { status: 500 });
      return new Response('{}');
    }),
  );
  return calls;
}

const refreshes = (calls: Call[]) => calls.filter((c) => c.method === 'POST' && c.path.endsWith('/page/refresh'));
const ok = async () => new Response(JSON.stringify({ refreshing: 'deploys' }));

test.each([
  ['with a title', { title: true }],
  ['without a title', { title: false }],
  ['on the phone layout', { title: false, phone: true }],
])('has a Refresh now button when the page is drawn %s', async (_name, props) => {
  refreshServer(ok);
  sources.value = [source(1)];
  render(<ModulePage sourceKey="deploys" {...props} />);
  await screen.findByText('Row one');
  expect(screen.getByRole('button', { name: 'Refresh now' })).toBeTruthy();
});

test('puts the Refresh now button on the title line, after the title', async () => {
  refreshServer(ok);
  sources.value = [source(1)];
  open();
  const button = await screen.findByRole('button', { name: 'Refresh now' });
  const heading = screen.getByRole('heading', { name: 'Deploys' });
  expect(heading.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

test('Refresh now posts the page refresh of the source, its key encoded', async () => {
  const calls = refreshServer(ok);
  sources.value = [{ ...source(1), key: 'my deploys/x' }];
  render(<ModulePage sourceKey="my deploys/x" title />);
  fireEvent.click(await screen.findByRole('button', { name: 'Refresh now' }));
  await waitFor(() => expect(refreshes(calls)).toHaveLength(1));
  expect(refreshes(calls)[0].path).toBe('/api/v1/sources/my%20deploys%2Fx/page/refresh');
});

test('the button is busy and named Refreshing while the request is in flight, a second click sends nothing, then it is idle', async () => {
  let answer: (r: Response) => void = () => {};
  const calls = refreshServer(() => new Promise<Response>((res) => (answer = res)));
  sources.value = [source(1)];
  open();
  fireEvent.click(await screen.findByRole('button', { name: 'Refresh now' }));
  const busy = await screen.findByRole('button', { name: 'Refreshing' });
  expect(busy.getAttribute('aria-busy')).toBe('true');
  fireEvent.click(busy);
  expect(refreshes(calls)).toHaveLength(1);
  answer(await ok());
  await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh now' }).getAttribute('aria-busy')).not.toBe('true'));
  expect(refreshes(calls)).toHaveLength(1);
});

test('after an OK refresh the new page version draws the new rows and the earlier error alert is gone', async () => {
  let body: unknown = { ...good, state: 'error', error: 'plugin crashed' };
  const calls = refreshServer(ok, () => body);
  sources.value = [source(1, 'error')];
  open();
  // A refused Restart leaves its own line on the page.
  fireEvent.click(await screen.findByRole('button', { name: 'Restart' }));
  expect(await screen.findByText('restart refused')).toBeTruthy();

  body = { ...good, groups: [{ id: 'g1', title: 'Needs review', rows: [{ id: 'r3', title: 'Row three', subtitle: '', chips: [], reason: '' }] }] };
  const pageGets = () => calls.filter((c) => c.method === 'GET' && c.path.endsWith('/page')).length;
  fireEvent.click(screen.getByRole('button', { name: 'Refresh now' }));
  await waitFor(() => expect(screen.queryByText('restart refused')).toBeNull());
  const before = pageGets();
  await new Promise((r) => setTimeout(r, 20));
  expect(pageGets()).toBe(before);
  // The server queues page.get and the source's page version moves.
  sources.value = [source(2)];
  expect(await screen.findByText('Row three')).toBeTruthy();
  expect(screen.queryByText('Row one')).toBeNull();
});

test('a refused refresh shows its error in the alert and the button is idle again', async () => {
  const calls = refreshServer(async () => new Response(JSON.stringify({ error: 'plugin refused the refresh' }), { status: 422 }));
  sources.value = [source(1)];
  open();
  fireEvent.click(await screen.findByRole('button', { name: 'Refresh now' }));
  expect((await screen.findByRole('alert')).textContent).toContain('plugin refused the refresh');
  await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh now' }).getAttribute('aria-busy')).not.toBe('true'));
  expect(refreshes(calls)).toHaveLength(1);
});

test('an OK refresh after the first page load failed fetches the page once, with no version bump', async () => {
  const calls: Call[] = [];
  let gets = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push({ method, path });
      if (method === 'GET' && path.endsWith('/page')) return ++gets === 1 ? new Response(JSON.stringify({ error: 'page down' }), { status: 502 }) : new Response(JSON.stringify(good));
      if (method === 'POST' && path.endsWith('/page/refresh')) return ok();
      return new Response('{}');
    }),
  );
  sources.value = [source(1)];
  open();
  await screen.findByText('page down');
  fireEvent.click(screen.getByRole('button', { name: 'Refresh now' }));
  expect(await screen.findByText('Row one')).toBeTruthy();
  expect(screen.queryByText('page down')).toBeNull();
  expect(gets).toBe(2);
});
