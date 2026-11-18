// A module page's rows, filter, selection and detail view.
import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Source } from './api';
import { ModulePage } from './ModulePage';
import { go, loc } from './router';
import { sources } from './shipsLink';

const start = { value: loc.value, url: location.href };

afterEach(() => {
  vi.unstubAllGlobals();
  sources.value = [];
  loc.value = start.value;
  history.replaceState({}, '', start.url);
});

const source = (version: number, rows?: string[], state = 'ready'): Source =>
  ({ key: 'deploys', name: 'Deploys', fleet: 'zulu', views: [], capabilities: ['page'], state: 'ok', page: { state, waiting: 0, version, ...(rows ? { rows } : {}) } }) as unknown as Source;

const act = { id: 'pass', label: 'Pass review', confirm: 'form', summary: 'Pass the review' };

const page = {
  state: 'ready',
  waiting: 2,
  groups: [
    {
      id: 'review',
      title: 'Needs review',
      rows: [
        { id: 'r1', title: 'Fix login', subtitle: 'Payments team', chips: [{ label: 'P1', tone: 'warn' }, { label: 'shipped', tone: 'good' }, { label: 'stale', tone: 'muted' }, { label: 'odd', tone: 'red' }, { label: 'plain' }], reason: 'Review is open', action: act },
        { id: 'r2', title: 'Rotate keys', subtitle: '', chips: [{ label: 'security', tone: 'neutral' }], reason: '' },
      ],
    },
    { id: 'done', title: 'Done', rows: [{ id: 'r3', title: 'Ship docs', subtitle: 'Docs', chips: [], reason: 'Merged last week' }] },
  ],
};

const detail = {
  sections: [
    { kind: 'links', title: 'Links', links: [{ label: 'Ticket', url: 'https://tracker.example/t/1' }, { label: 'Sneaky', url: 'javascript:alert(1)' }] },
    { kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [{ label: 'open', tone: 'warn' }], actions: [act, { id: 'skip', label: 'Skip review', confirm: 'click' }] }] },
    { kind: 'fields', title: 'Details', fields: [{ label: 'Owner', value: 'amy' }, { label: 'Note', value: '<b>bold</b>' }] },
  ],
};

interface Call {
  method: string;
  path: string;
}

type Row = { status?: number; body: unknown };

// Serves the page and each row's answer; `rows` is read on each request so a test can change it.
function server(rows: Record<string, Row | (() => Row)> = { r1: { body: detail } }) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push({ method, path });
      if (method === 'GET' && path.endsWith('/page')) return new Response(JSON.stringify(page));
      const m = /\/rows\/([^/?]+)$/.exec(path);
      if (method === 'GET' && m) {
        const r = rows[decodeURIComponent(m[1])];
        const a = typeof r === 'function' ? r() : r;
        return a ? new Response(JSON.stringify(a.body), { status: a.status ?? 200 }) : new Response(JSON.stringify({ error: 'The page has no such row' }), { status: 404 });
      }
      return new Response('{}');
    }),
  );
  return calls;
}

const rowGets = (calls: Call[]) => calls.filter((c) => c.method === 'GET' && c.path.includes('/rows/'));
const pageGets = (calls: Call[]) => calls.filter((c) => c.method === 'GET' && c.path.endsWith('/page'));
const open = (phone = false) => render(<ModulePage sourceKey="deploys" title phone={phone} />);
const select = (id: string) => go(`/app/deploys/${encodeURIComponent(id)}`, '');
const filterBox = () => screen.getByRole('searchbox', { name: /filter/i });

test('a row shows its title, subtitle, chips and reason, and a group its title', async () => {
  server();
  sources.value = [source(1)];
  open();
  expect(await screen.findByRole('heading', { name: /Needs review/ })).toBeTruthy();
  const row = screen.getByRole('button', { name: 'Open Fix login' }).closest('li') as HTMLElement;
  for (const t of ['Fix login', 'Payments team', 'P1', 'shipped', 'Review is open']) expect(within(row).getByText(t)).toBeTruthy();
  expect(screen.getByRole('heading', { name: /Done/ })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Open Ship docs' })).toBeTruthy();
});

test('a chip is toned good, warn or muted, and a missing or unknown tone is neutral', async () => {
  server();
  sources.value = [source(1)];
  open();
  await screen.findByText('P1');
  const tone = (label: string) => screen.getByText(label).getAttribute('data-tone');
  expect([tone('shipped'), tone('P1'), tone('stale'), tone('security'), tone('odd'), tone('plain')]).toEqual(['good', 'warn', 'muted', 'neutral', 'neutral', 'neutral']);
});

test('typing in the filter narrows the rows and hides a group with none left, without a request', async () => {
  const calls = server();
  sources.value = [source(1)];
  open();
  await screen.findByText('Fix login');
  const before = calls.length;
  fireEvent.input(filterBox(), { target: { value: 'docs' } });
  expect(screen.queryByText('Fix login')).toBeNull();
  expect(screen.queryByRole('heading', { name: /Needs review/ })).toBeNull();
  expect(screen.getByText('Ship docs')).toBeTruthy();
  expect(screen.getByRole('heading', { name: /Done/ })).toBeTruthy();
  fireEvent.input(filterBox(), { target: { value: '' } });
  expect(screen.getByText('Fix login')).toBeTruthy();
  expect(calls).toHaveLength(before);
});

test('on a phone the filter shows above the groups', async () => {
  server();
  sources.value = [source(1)];
  open(true);
  const first = await screen.findByRole('heading', { name: /Needs review/ });
  expect(filterBox().compareDocumentPosition(first) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

test('selecting a row puts its id, encoded, in the URL and marks it selected', async () => {
  server();
  sources.value = [source(1)];
  open();
  await screen.findByText('Fix login');
  fireEvent.click(screen.getByRole('button', { name: 'Open Fix login' }));
  await waitFor(() => expect(loc.value.path).toBe('/app/deploys/r1'));
  expect(screen.getByRole('button', { name: 'Open Fix login' }).getAttribute('aria-pressed')).toBe('true');
});

test('a row id with a slash and a space stays in one path segment, in the URL and in the request', async () => {
  const calls = server({ 'a/b c': { body: detail } });
  select('a/b c');
  sources.value = [source(1)];
  open();
  await waitFor(() => expect(rowGets(calls)).toHaveLength(1));
  expect(loc.value.path).toBe('/app/deploys/a%2Fb%20c');
  expect(rowGets(calls)[0].path).toBe('/api/v1/sources/deploys/rows/a%2Fb%20c');
});

test('an open row is fetched with no include, and drawn in the side pane on desktop and iPad', async () => {
  const calls = server();
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  expect(await within(pane).findByText('Code review')).toBeTruthy();
  expect(rowGets(calls)).toHaveLength(1);
  expect(rowGets(calls)[0].path).toBe('/api/v1/sources/deploys/rows/r1');
  // The list stays beside the pane.
  expect(screen.getByRole('button', { name: 'Open Rotate keys' })).toBeTruthy();
});

test('a links section lists links that open outside the app, and a url that is not http or https is no link', async () => {
  server();
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  const link = (await within(pane).findByRole('link', { name: 'Ticket' })) as HTMLAnchorElement;
  expect(link.getAttribute('href')).toBe('https://tracker.example/t/1');
  expect(link.getAttribute('target')).toBe('_blank');
  expect(link.getAttribute('rel')).toContain('noopener');
  expect(within(pane).queryByRole('link', { name: 'Sneaky' })).toBeNull();
  expect(within(pane).getAllByRole('link')).toHaveLength(1);
});

test('a steps section shows each step with its title and chips, and a fields section its label and value as text', async () => {
  server();
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  await within(pane).findByText('Code review');
  expect(within(pane).getByText('open').getAttribute('data-tone')).toBe('warn');
  expect(within(pane).getByText('Owner')).toBeTruthy();
  expect(within(pane).getByText('amy')).toBeTruthy();
  expect(within(pane).getByText('<b>bold</b>')).toBeTruthy();
  expect(pane.querySelector('b')).toBeNull();
});

test('a built-in action with a url is drawn disabled on a row and on a step, and a press makes no request', async () => {
  const docs = { id: 'docs', label: 'Open docs', confirm: 'click', url: 'https://tracker.example/docs' };
  const steps = { sections: [{ kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [], actions: [docs] }] }] };
  const calls = server({ r1: { body: steps } });
  (page.groups[0].rows[0] as { actions?: unknown }).actions = [docs];
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  await within(pane).findByText('Code review');
  const buttons = [within(screen.getByRole('button', { name: 'Open Fix login' }).closest('li') as HTMLElement).getByRole('button', { name: 'Open docs' }), within(pane).getByRole('button', { name: 'Open docs' })];
  const before = calls.length;
  for (const b of buttons) {
    expect((b as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(b);
  }
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(calls).toHaveLength(before);
  delete (page.groups[0].rows[0] as { actions?: unknown }).actions;
});

test('a module action on a row is enabled and opens its form when pressed', async () => {
  server();
  sources.value = [source(1)];
  open();
  const row = (await screen.findByRole('button', { name: 'Open Fix login' })).closest('li') as HTMLElement;
  const b = within(row).getByRole('button', { name: 'Pass review' }) as HTMLButtonElement;
  expect(b.disabled).toBe(false);
  fireEvent.click(b);
  expect(await screen.findByRole('dialog')).toBeTruthy();
});

test('a row no longer on the page, a 404, clears the selection and says so', async () => {
  server({ r1: { status: 404, body: { error: 'The page has no such row' } } });
  select('r1');
  sources.value = [source(1)];
  open();
  expect(await screen.findByText('That row is no longer on this page.')).toBeTruthy();
  await waitFor(() => expect(loc.value.path).toBe('/app/deploys'));
  expect(screen.queryByRole('complementary')).toBeNull();
  expect(screen.getByRole('button', { name: 'Open Fix login' }).getAttribute('aria-pressed')).toBe('false');
});

test('a 502 shows the plugin error inside the pane and keeps the selection', async () => {
  server({ r1: { status: 502, body: { error: 'plugin unreachable' } } });
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  expect((await within(pane).findByRole('alert')).textContent).toContain('plugin unreachable');
  expect(loc.value.path).toBe('/app/deploys/r1');
});

test('the open row is fetched again when the page change names it, and redrawn', async () => {
  let body: unknown = detail;
  const calls = server({ r1: () => ({ body }) });
  select('r1');
  sources.value = [source(1)];
  open();
  await screen.findByText('Code review');
  body = { sections: [{ kind: 'fields', title: 'Details', fields: [{ label: 'Owner', value: 'bo' }] }] };
  sources.value = [source(2, ['r1'])];
  expect(await screen.findByText('bo')).toBeTruthy();
  expect(rowGets(calls)).toHaveLength(2);
  expect(screen.queryByText('Code review')).toBeNull();
});

test('on a phone the selected row is its own screen with a way back to the list', async () => {
  server();
  select('r1');
  sources.value = [source(1)];
  open(true);
  expect(await screen.findByText('Code review')).toBeTruthy();
  expect(screen.queryByRole('complementary')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Open Fix login' })).toBeNull();
  expect(screen.queryByRole('heading', { name: /Needs review/ })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  await waitFor(() => expect(loc.value.path).toBe('/app/deploys'));
  expect(await screen.findByRole('button', { name: 'Open Fix login' })).toBeTruthy();
});

test('on a phone a row opens as its own screen from the list', async () => {
  server();
  sources.value = [source(1)];
  open(true);
  fireEvent.click(await screen.findByRole('button', { name: 'Open Fix login' }));
  expect(await screen.findByText('Code review')).toBeTruthy();
  expect(loc.value.path).toBe('/app/deploys/r1');
});

test('a row with a section that has no array draws the pane without throwing', async () => {
  server({ r1: { body: { sections: [{ kind: 'links', title: 'Links' }, { kind: 'steps', title: 'Steps' }, { kind: 'fields', title: 'Details' }] } } });
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  expect(await within(pane).findByText('Details')).toBeTruthy();
  expect(within(pane).getByText('Links')).toBeTruthy();
});

test('while the page is starting a row that answers 404 keeps the selection, and it is fetched once the page is ready', async () => {
  let ready = false;
  const calls = server({ r1: () => (ready ? { body: detail } : { status: 404, body: { error: 'The page has no such row' } }) });
  select('r1');
  sources.value = [source(1, undefined, 'starting')];
  open();
  await screen.findByRole('complementary', { name: 'Selected row' });
  expect(rowGets(calls)).toHaveLength(0);
  expect(screen.queryByText('That row is no longer on this page.')).toBeNull();
  expect(loc.value.path).toBe('/app/deploys/r1');
  ready = true;
  sources.value = [source(2)];
  expect(await screen.findByText('Code review')).toBeTruthy();
  expect(rowGets(calls)).toHaveLength(1);
  expect(loc.value.path).toBe('/app/deploys/r1');
});

test('a late answer for a row that is no longer selected is not drawn', async () => {
  let late!: (r: Response) => void;
  const fields = (v: string) => ({ sections: [{ kind: 'fields', fields: [{ label: 'Owner', value: v }] }] });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string) => {
      if (path.endsWith('/page')) return new Response(JSON.stringify(page));
      if (path.endsWith('/rows/r1')) return new Promise<Response>((res) => (late = res));
      return new Response(JSON.stringify(fields('cy')));
    }),
  );
  select('r1');
  sources.value = [source(1)];
  open();
  await screen.findByText('Fix login', { selector: 'h2' });
  fireEvent.click(screen.getByRole('button', { name: 'Open Rotate keys' }));
  expect(await screen.findByText('cy')).toBeTruthy();
  late(new Response(JSON.stringify(fields('amy'))));
  await new Promise((r) => setTimeout(r, 0));
  expect(screen.queryByText('amy')).toBeNull();
  expect(screen.getByText('cy')).toBeTruthy();
});

test('the pane and the phone row screen name the row with its title, subtitle and chips', async () => {
  server();
  select('r1');
  sources.value = [source(1)];
  const { unmount } = open();
  const pane = await screen.findByRole('complementary', { name: 'Selected row' });
  expect(await within(pane).findByRole('heading', { name: 'Fix login' })).toBeTruthy();
  expect(within(pane).getByText('Payments team')).toBeTruthy();
  expect(within(pane).getByText('P1')).toBeTruthy();
  unmount();
  open(true);
  expect(await screen.findByRole('heading', { name: 'Fix login' })).toBeTruthy();
});

test('closing a row opened from the list goes back in history, and closing a deep-linked row replaces the entry with the list', async () => {
  server();
  sources.value = [source(1)];
  const back = vi.spyOn(history, 'back').mockImplementation(() => {});
  const { unmount } = open();
  fireEvent.click(await screen.findByRole('button', { name: 'Open Fix login' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Close' }));
  expect(back).toHaveBeenCalledTimes(1);
  unmount();
  back.mockClear();
  history.replaceState(null, '', '/app/deploys/r1');
  select('r1');
  const before = history.length;
  open();
  fireEvent.click(await screen.findByRole('button', { name: 'Close' }));
  expect(back).not.toHaveBeenCalled();
  expect(loc.value.path).toBe('/app/deploys');
  expect(history.length).toBe(before);
  back.mockRestore();
});

test('a row whose id is a dot segment cannot be opened', async () => {
  server();
  sources.value = [source(1)];
  page.groups[1].rows.push({ id: '..', title: 'Dotted', subtitle: 'x', chips: [], reason: '' } as never);
  open();
  const btn = (await screen.findByRole('button', { name: 'Open Dotted' })) as HTMLButtonElement;
  expect(btn.disabled).toBe(true);
  page.groups[1].rows.pop();
});
