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
        {
          id: 'r1',
          title: 'Fix login',
          subtitle: 'Payments team',
          chips: [{ label: 'P1', tone: 'warn' }, { label: 'shipped', tone: 'good' }, { label: 'stale', tone: 'muted' }, { label: 'odd', tone: 'red' }, { label: 'plain' }],
          reason: 'Review is open',
          action: act,
        },
        {
          id: 'r2',
          title: 'Rotate keys',
          subtitle: '',
          chips: [{ label: 'security', tone: 'neutral' }],
          reason: '',
        },
      ],
    },
    {
      id: 'done',
      title: 'Done',
      rows: [
        {
          id: 'r3',
          title: 'Ship docs',
          subtitle: 'Docs',
          chips: [],
          reason: 'Merged last week',
        },
      ],
    },
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
function server(rows: Record<string, Row | (() => Row)> = { r1: { body: detail } }, served: unknown = page) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push({ method, path });
      if (method === 'GET' && path.endsWith('/page')) return new Response(JSON.stringify(served));
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
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
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
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  const link = (await within(pane).findByRole('link', {
    name: 'Ticket',
  })) as HTMLAnchorElement;
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
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  await within(pane).findByText('Code review');
  expect(within(pane).getByText('open').getAttribute('data-tone')).toBe('warn');
  expect(within(pane).getByText('Owner')).toBeTruthy();
  expect(within(pane).getByText('amy')).toBeTruthy();
  expect(within(pane).getByText('<b>bold</b>')).toBeTruthy();
  expect(pane.querySelector('b')).toBeNull();
});

test('Open link is drawn as a link on a row and on a step, and a press makes no request', async () => {
  const docs = {
    id: 'docs',
    label: 'Open docs',
    confirm: 'click',
    url: 'https://tracker.example/docs',
  };
  const steps = {
    sections: [
      {
        kind: 'steps',
        title: 'Steps',
        steps: [{ id: 's1', title: 'Code review', chips: [], actions: [docs] }],
      },
    ],
  };
  const withDocs = {
    ...page,
    groups: [
      {
        ...page.groups[0],
        rows: [{ ...page.groups[0].rows[0], actions: [docs] }, ...page.groups[0].rows.slice(1)],
      },
      page.groups[1],
    ],
  };
  const calls = server({ r1: { body: steps } }, withDocs);
  select('r1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  await within(pane).findByText('Code review');
  const links = [within(screen.getByRole('button', { name: 'Open Fix login' }).closest('li') as HTMLElement).getByRole('link', { name: 'Open docs' }), within(pane).getByRole('link', { name: 'Open docs' })];
  const before = calls.length;
  for (const a of links) {
    expect(a.getAttribute('href')).toBe('https://tracker.example/docs');
    fireEvent.click(a);
  }
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(calls).toHaveLength(before);
});

test('a module action on a row is enabled and opens its form when pressed', async () => {
  server();
  sources.value = [source(1)];
  open();
  const row = (await screen.findByRole('button', { name: 'Open Fix login' })).closest('li') as HTMLElement;
  const b = within(row).getByRole('button', {
    name: 'Pass review',
  }) as HTMLButtonElement;
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
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
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
  body = {
    sections: [
      {
        kind: 'fields',
        title: 'Details',
        fields: [{ label: 'Owner', value: 'bo' }],
      },
    ],
  };
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
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  expect(await within(pane).findByText('Details')).toBeTruthy();
  expect(within(pane).getByText('Links')).toBeTruthy();
});

test('while the page is starting a row that answers 404 keeps the selection, and it is fetched once the page is ready', async () => {
  let ready = false;
  const calls = server({
    r1: () => (ready ? { body: detail } : { status: 404, body: { error: 'The page has no such row' } }),
  });
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
  const fields = (v: string) => ({
    sections: [{ kind: 'fields', fields: [{ label: 'Owner', value: v }] }],
  });
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
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
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
  page.groups[1].rows.push({
    id: '..',
    title: 'Dotted',
    subtitle: 'x',
    chips: [],
    reason: '',
  } as never);
  open();
  const btn = (await screen.findByRole('button', {
    name: 'Open Dotted',
  })) as HTMLButtonElement;
  expect(btn.disabled).toBe(true);
  page.groups[1].rows.pop();
});

const resume = { id: 'resume', label: 'Resume', confirm: 'click', step: 's2' };
const closeAll = {
  id: 'close-all',
  label: 'Close 3 from before today',
  confirm: 'click',
};
const claim = { id: 'claim', label: 'Claim', confirm: 'click', step: 's1' };
const openDocs = {
  id: 'docs',
  label: 'Open docs',
  confirm: 'click',
  url: 'https://tracker.example/docs',
};

// A row targeting step s2: Pass review and Resume act on it, Claim on s1, Close all on the whole row.
const targetedRow = (over: Record<string, unknown> = {}) => ({
  id: 't1',
  title: 'Fix login',
  subtitle: 'T-1',
  url: 'https://tracker.example/t/1',
  chips: [],
  reason: 'You approved the change',
  target: {
    label: 'Code review',
    chips: [{ label: 'Paused', tone: 'warn' }],
    step: 's2',
  },
  action: { ...act, step: 's2' },
  actions: [resume, closeAll, claim, openDocs],
  ...over,
});
const withRow = (row: unknown) => ({
  state: 'ready',
  waiting: 1,
  groups: [{ id: 'review', title: 'Needs review', rows: [row] }],
});
const targeted = withRow(targetedRow());
const steps = (...extra: unknown[]) => ({
  sections: [
    {
      kind: 'steps',
      title: 'Tasks',
      steps: [
        {
          id: 's1',
          title: 'Write docs',
          subtitle: 'Docs pass',
          chips: [{ label: 'Unclaimed', tone: 'muted' }],
          actions: [{ id: 'own1', label: 'Own one', confirm: 'click' }],
        },
        {
          id: 's2',
          title: 'Code review',
          subtitle: 'With a pair, Amy Lee',
          chips: [{ label: 'Paused', tone: 'warn' }],
          actions: [{ id: 'assign', label: 'Assign…', confirm: 'form' }],
        },
      ],
    },
    ...extra,
  ],
});
const targetedDetail = steps();

const names = (el: HTMLElement) =>
  within(el)
    .getAllByRole('button')
    .map((b) => b.textContent);
const before = (a: Node, b: Node) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
const rowOf = (title: string) => screen.getByRole('button', { name: `Open ${title}` }).closest('li') as HTMLElement;
const cardsOf = (pane: HTMLElement) =>
  within(pane)
    .getAllByRole('listitem')
    .filter((li) => li.classList.contains('mstep'));
const openPane = async (row = targeted, body: unknown = targetedDetail) => {
  server({ t1: { body } }, row);
  select('t1');
  sources.value = [source(1)];
  open();
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  await within(pane).findByText('Write docs');
  return pane;
};

test('a targeted row holds the module actions naming its step beside the target, leading with the row action', async () => {
  server({ t1: { body: targetedDetail } }, targeted);
  sources.value = [source(1)];
  open();
  const group = await screen.findByRole('group', {
    name: 'Acts on Code review',
  });
  expect(within(group).getByText('Paused')).toBeTruthy();
  expect(names(group)).toEqual(['Pass review', 'Resume']);
  expect(within(group).getByRole('button', { name: 'Pass review' }).classList.contains('primary')).toBe(true);
  expect(within(group).queryByRole('link', { name: 'Open docs' })).toBeNull();
});

test('a targeted row puts its module actions with no step after a divider, outside the Acts on group, before the built-in Open link', async () => {
  server({ t1: { body: targetedDetail } }, targeted);
  sources.value = [source(1)];
  open();
  const group = await screen.findByRole('group', {
    name: 'Acts on Code review',
  });
  const row = rowOf('Fix login');
  const all = within(row).getByRole('button', {
    name: 'Close 3 from before today',
  });
  const divider = within(row).getByRole('separator');
  const link = within(row).getByRole('link', { name: 'Open docs' });
  expect(group.contains(all)).toBe(false);
  expect(group.contains(divider)).toBe(false);
  expect(before(group, divider) && before(divider, all) && before(all, link)).toBe(true);
  expect(within(row).queryByRole('button', { name: 'Claim' })).toBeNull();
});

test("a targeted row's own row action with no step leads the row-level actions, not the target line", async () => {
  server({ t1: { body: targetedDetail } }, withRow(targetedRow({ action: act, actions: [resume, closeAll] })));
  sources.value = [source(1)];
  open();
  const group = await screen.findByRole('group', {
    name: 'Acts on Code review',
  });
  expect(names(group)).toEqual(['Resume']);
  const row = rowOf('Fix login');
  const pass = within(row).getByRole('button', { name: 'Pass review' });
  expect(group.contains(pass)).toBe(false);
  expect(pass.classList.contains('primary')).toBe(true);
  expect(before(within(row).getByRole('separator'), pass)).toBe(true);
  expect(before(pass, within(row).getByRole('button', { name: 'Close 3 from before today' }))).toBe(true);
});

test('a target with no step holds no module actions: all of them are row-level and none is shown next', async () => {
  const noStep = targetedRow({
    target: { label: 'Code review', chips: [] },
    actions: [resume, closeAll],
  });
  const pane = await openPane(withRow(noStep) as never);
  const group = screen.getByRole('group', { name: 'Acts on Code review' });
  expect(within(group).queryAllByRole('button')).toEqual([]);
  const row = rowOf('Fix login');
  for (const name of ['Pass review', 'Resume', 'Close 3 from before today']) {
    const btn = within(row).getByRole('button', { name });
    expect(group.contains(btn)).toBe(false);
    expect(before(within(row).getByRole('separator'), btn)).toBe(true);
  }
  expect(within(pane).queryByText('Next up')).toBeNull();
});

test('a row with no module action outside its target line shows no divider', async () => {
  server({ t1: { body: targetedDetail } }, withRow(targetedRow({ actions: [resume, openDocs] })));
  sources.value = [source(1)];
  open();
  const group = await screen.findByRole('group', {
    name: 'Acts on Code review',
  });
  expect(names(group)).toEqual(['Pass review', 'Resume']);
  expect(within(rowOf('Fix login')).queryByRole('separator')).toBeNull();
});

test('a row with no target shows all its module actions as before, whatever step they name', async () => {
  const plain = withRow({
    id: 't1',
    title: 'Fix login',
    subtitle: '',
    chips: [],
    reason: '',
    action: { ...act, step: 's2' },
    actions: [
      { ...closeAll, step: 's1' },
      { id: 'x', label: 'Other', confirm: 'click' },
    ],
  });
  server({ t1: { body: targetedDetail } }, plain);
  sources.value = [source(1)];
  open();
  await screen.findByRole('button', { name: 'Open Fix login' });
  const row = rowOf('Fix login');
  expect(
    within(row)
      .getAllByRole('button')
      .filter((b) => !b.classList.contains('orow-open'))
      .map((b) => b.textContent),
  ).toEqual(['Pass review', 'Close 3 from before today', 'Other']);
  expect(within(row).queryByRole('separator')).toBeNull();
  expect(within(row).queryByRole('group')).toBeNull();
});

test('in the detail the target step comes first, marked Next up, with the row module actions naming it and its own, each once', async () => {
  const pane = await openPane();
  const cards = cardsOf(pane);
  expect(cards.map((c) => c.querySelector('.mstep-title')?.textContent)).toEqual(['Code review', 'Write docs']);
  expect(within(cards[0]).getByText('Next up')).toBeTruthy();
  expect(within(cards[0]).getByText('With a pair, Amy Lee')).toBeTruthy();
  expect(within(cards[0]).getByText('You approved the change')).toBeTruthy();
  expect(names(cards[0])).toEqual(['Pass review', 'Resume', 'Assign…']);
  expect(within(cards[0]).getByRole('button', { name: 'Pass review' }).classList.contains('lead')).toBe(true);
  expect(within(cards[1]).queryByText('Next up')).toBeNull();
});

test('in the detail a step card shows the row module actions naming it before its own, and the next card no longer shows the others', async () => {
  const pane = await openPane();
  const [next, other] = cardsOf(pane);
  expect(names(other)).toEqual(['Claim', 'Own one']);
  expect(within(next).queryByRole('button', { name: 'Claim' })).toBeNull();
  expect(within(next).queryByRole('button', { name: 'Close 3 from before today' })).toBeNull();
  expect(within(other).queryByRole('button', { name: 'Resume' })).toBeNull();
});

test('in the detail the row-level module actions sit after the steps section, once', async () => {
  const pane = await openPane();
  const all = within(pane).getAllByRole('button', {
    name: 'Close 3 from before today',
  });
  expect(all).toHaveLength(1);
  const cards = cardsOf(pane);
  expect(before(cards[cards.length - 1], all[0])).toBe(true);
  expect(cards.some((c) => c.contains(all[0]))).toBe(false);
});

test('a row module action naming a step the detail lacks is row-level in the detail, after the steps', async () => {
  const ghost = { id: 'ghost', label: 'Ghost', confirm: 'click', step: 'gone' };
  const pane = await openPane(withRow(targetedRow({ actions: [resume, ghost] })) as never);
  const cards = cardsOf(pane);
  expect(cards.some((c) => within(c).queryByRole('button', { name: 'Ghost' }))).toBe(false);
  expect(before(cards[cards.length - 1], within(pane).getByRole('button', { name: 'Ghost' }))).toBe(true);
  expect(within(rowOf('Fix login')).queryByRole('button', { name: 'Ghost' })).toBeNull();
});

test("a module action whose id already showed earlier shows nowhere else, the row's own copy first", async () => {
  const detail = steps();
  const s1 = (detail.sections[0] as { steps: { actions: unknown[] }[] }).steps;
  s1[0].actions.unshift({
    id: 'resume',
    label: 'Resume again',
    confirm: 'form',
  });
  s1[1].actions.unshift({
    id: 'close-all',
    label: 'Close again',
    confirm: 'click',
  });
  const pane = await openPane(targeted, detail);
  const [next, other] = cardsOf(pane);
  expect(names(next)).toEqual(['Pass review', 'Resume', 'Assign…']);
  expect(names(other)).toEqual(['Claim', 'Own one']);
  expect(within(pane).queryByRole('button', { name: 'Resume again' })).toBeNull();
  expect(within(pane).queryByRole('button', { name: 'Close again' })).toBeNull();
});

test("a step's own module action stays on its card whatever step it names", async () => {
  const detail = steps();
  (detail.sections[0] as { steps: { actions: unknown[] }[] }).steps[0].actions.push({
    id: 'foreign',
    label: 'Foreign',
    confirm: 'click',
    step: 's2',
  });
  const pane = await openPane(targeted, detail);
  const [next, other] = cardsOf(pane);
  expect(names(other)).toEqual(['Claim', 'Own one', 'Foreign']);
  expect(within(next).queryByRole('button', { name: 'Foreign' })).toBeNull();
});

test('a link in the detail shows its chips before the label and its subtitle as a second line; a bare link is unchanged', async () => {
  const links = {
    kind: 'links',
    title: 'Pull requests',
    links: [
      {
        label: 'PR 7',
        url: 'https://tracker.example/pr/7',
        subtitle: 'Add retries to the client',
        chips: [{ label: 'Merged', tone: 'good' }],
      },
      { label: 'Ticket', url: 'https://tracker.example/t/1' },
    ],
  };
  const pane = await openPane(targeted, {
    sections: [links, ...steps().sections],
  });
  const pr = within(pane).getByRole('link', { name: /PR 7/ }).closest('li') as HTMLElement;
  const chip = within(pr).getByText('Merged');
  expect(chip.classList.contains('mchip')).toBe(true);
  expect(before(chip, within(pr).getByRole('link', { name: /PR 7/ }))).toBe(true);
  expect(within(pr).getByText('Add retries to the client')).toBeTruthy();
  const bare = within(pane)
    .getByRole('link', { name: /Ticket/ })
    .closest('li') as HTMLElement;
  expect(bare.querySelector('.mchip')).toBeNull();
  expect(bare.textContent).toBe('Ticket↗');
});

test('the row\'s link is its subtitle at the top of the detail, opening outside the app, and a link that is not http is plain text', async () => {
  server({ t1: { body: targetedDetail } }, targeted);
  select('t1');
  sources.value = [source(1)];
  const { unmount } = open();
  const pane = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  const link = (await within(pane).findByRole('link', {
    name: /T-1/,
  })) as HTMLAnchorElement;
  expect(link.getAttribute('href')).toBe('https://tracker.example/t/1');
  expect(link.getAttribute('target')).toBe('_blank');
  unmount();
  const bad = structuredClone(targeted);
  (bad.groups[0].rows[0] as { url: string }).url = 'javascript:alert(1)';
  server({ t1: { body: targetedDetail } }, bad);
  open();
  const pane2 = await screen.findByRole('complementary', {
    name: 'Selected row',
  });
  await within(pane2).findByText('Write docs');
  expect(within(pane2).queryByRole('link', { name: /T-1/ })).toBeNull();
  expect(within(pane2).getByText('T-1')).toBeTruthy();
});
