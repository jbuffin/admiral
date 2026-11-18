// Module actions on a module page's rows and steps: confirm kinds, forms, runs, results.
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Source } from './api';
import { ModulePage } from './ModulePage';
import { go, loc } from './router';
import { sources } from './shipsLink';
import { ToastRegion } from './ToastRegion';

const start = { value: loc.value, url: location.href };

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sources.value = [];
  loc.value = start.value;
  history.replaceState({}, '', start.url);
});

const source = (version: number, state = 'ready'): Source =>
  ({ key: 'deploys', name: 'Deploys', fleet: 'zulu', views: [], capabilities: ['page'], state: 'ok', page: { state, waiting: 0, version } }) as unknown as Source;

const pass = { id: 'pass', label: 'Pass review', confirm: 'form', summary: 'Pass the review' };
const ship = { id: 'ship', label: 'Ship it', confirm: 'yesno', summary: 'Ship this to production' };
const ping = { id: 'ping', label: 'Ping owner', confirm: 'click' };
const docs = { id: 'docs', label: 'Open docs', confirm: 'click', url: 'https://tracker.example/docs' };
const skip = { id: 'skip', label: 'Skip review', confirm: 'click' };
const again = { id: 'again', label: 'Run again', confirm: 'click' };
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
        { id: 'r1', title: 'Fix login', subtitle: 'Payments team', chips: [], reason: '', action: pass, actions: [ship, ping, docs] },
        { id: 'r3', title: 'Ship docs', subtitle: 'Docs', chips: [], reason: '', action: archive },
      ],
    },
  ],
});

const detail = { sections: [{ kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [], action: skip, actions: [again] }] }] };

const form = {
  title: 'Pass this review',
  fields: [
    { id: 'whose', label: 'Whose work', type: 'choice', options: [{ id: 'a', label: 'Amy' }, { id: 'b', label: 'Bo' }], default: 'b' },
    { id: 'files', label: 'Files', type: 'checklist', options: [{ id: 'x', label: 'X file' }, { id: 'y', label: 'Y file' }], default: ['y'] },
    { id: 'time', label: 'Time', type: 'duration', default: '01:30', min: '00:15' },
  ],
};

type Answer = { status?: number; body: unknown };
type Pending = Answer | Promise<Answer>;

interface Post {
  path: string;
  body: unknown;
  init?: RequestInit;
}

interface Handlers {
  form?: () => Pending;
  run?: (row: string, action: string) => Pending;
  page?: () => unknown;
}

// Serves the page, the row and the module action routes, and records every POST.
function server(h: Handlers = {}) {
  const posts: Post[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.endsWith('/page')) return new Response(JSON.stringify(h.page ? h.page() : pageOf()));
      if (method === 'GET' && path.endsWith('/rows/r1')) return new Response(JSON.stringify(detail));
      const m = /\/rows\/([^/]+)\/actions\/([^/]+)\/(form|run)$/.exec(path);
      if (method === 'POST' && m) {
        posts.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined, init });
        const a: Answer = await (m[3] === 'form' ? (h.form ?? (() => ({ body: form })))() : (h.run ?? (() => ({ body: { message: 'Done' } })))(m[1], m[2]));
        return new Response(JSON.stringify(a.body), { status: a.status ?? 200 });
      }
      return new Response('{}', { status: 404 });
    }),
  );
  return posts;
}

const runPosts = (posts: Post[]) => posts.filter((p) => p.path.endsWith('/run'));
const formPosts = (posts: Post[]) => posts.filter((p) => p.path.endsWith('/form'));
const BASE = '/api/v1/sources/deploys/rows';

const open = (state = 'ready', selected = false) => {
  if (selected) go('/app/deploys/r1', '');
  sources.value = [source(1, state)];
  return render(
    <>
      <ModulePage sourceKey="deploys" title />
      <ToastRegion />
    </>,
  );
};

const rowItem = async (title = 'Fix login') => (await screen.findByRole('button', { name: `Open ${title}` })).closest('li') as HTMLElement;
const press = (scope: HTMLElement, name: string) => fireEvent.click(within(scope).getByRole('button', { name }));
const pane = () => screen.findByRole('complementary', { name: 'Selected row' });

const openForm = async () => {
  press(await rowItem(), 'Pass review');
  return screen.findByRole('dialog');
};

test('a click module action on a row runs at once with no values and no sheet', async () => {
  const posts = server({ run: () => ({ body: { message: 'Owner pinged' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${BASE}/r1/actions/ping/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(formPosts(posts)).toHaveLength(0);
});

test('a yesno module action shows its summary first and runs only once confirmed', async () => {
  const posts = server();
  open();
  press(await rowItem(), 'Ship it');
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText('Ship this to production')).toBeTruthy();
  expect(posts).toHaveLength(0);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Ship it' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${BASE}/r1/actions/ship/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});

test('Cancel on a yesno sheet closes it and sends nothing', async () => {
  const posts = server();
  open();
  press(await rowItem(), 'Ship it');
  const dialog = await screen.findByRole('dialog');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(posts).toHaveLength(0);
  expect(within(await rowItem()).getByRole('button', { name: 'Ship it' }).hasAttribute('disabled')).toBe(false);
});

test('a form module action shows a loading state while its form is fetched, then draws the title and every field label', async () => {
  let release!: (a: Answer) => void;
  const posts = server({ form: () => new Promise<Answer>((r) => (release = r)) });
  open();
  press(await rowItem(), 'Pass review');
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByRole('status', { name: /loading/i })).toBeTruthy();
  expect(within(dialog).queryByRole('button', { name: 'Submit' })).toBeNull();
  expect(formPosts(posts)[0].path).toBe(`${BASE}/r1/actions/pass/form`);
  await act(async () => release({ body: form }));
  const drawn = await screen.findByRole('dialog', { name: 'Pass this review' });
  for (const label of ['Whose work', 'Files', 'Time']) expect(within(drawn).getByText(label)).toBeTruthy();
  expect(within(drawn).queryByRole('status', { name: /loading/i })).toBeNull();
  expect(runPosts(posts)).toHaveLength(0);
});

test('submitting a form runs the module action with the values the engineer left or changed', async () => {
  const posts = server();
  open();
  const dialog = await openForm();
  await within(dialog).findByText('Whose work');
  fireEvent.click(within(dialog).getByRole('radio', { name: 'Amy' }));
  fireEvent.click(within(dialog).getByRole('checkbox', { name: 'X file' }));
  fireEvent.input(within(dialog).getByLabelText('Time'), { target: { value: '02:45' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${BASE}/r1/actions/pass/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: { whose: 'a', files: ['x', 'y'], time: '02:45' } });
});

test('Cancel on a form sends no run', async () => {
  const posts = server();
  open();
  const dialog = await openForm();
  await within(dialog).findByText('Whose work');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(runPosts(posts)).toHaveLength(0);
});

test('a choice starts on its default, a checklist on its default, and a duration on its default', async () => {
  server();
  open();
  const dialog = await openForm();
  await within(dialog).findByText('Whose work');
  expect((within(dialog).getByRole('radio', { name: 'Bo' }) as HTMLInputElement).checked).toBe(true);
  expect((within(dialog).getByRole('radio', { name: 'Amy' }) as HTMLInputElement).checked).toBe(false);
  expect((within(dialog).getByRole('checkbox', { name: 'Y file' }) as HTMLInputElement).checked).toBe(true);
  expect((within(dialog).getByRole('checkbox', { name: 'X file' }) as HTMLInputElement).checked).toBe(false);
  expect((within(dialog).getByLabelText('Time') as HTMLInputElement).value).toBe('01:30');
});

test('a checklist with no default starts with every option ticked, and submits them all', async () => {
  const posts = server({ form: () => ({ body: { fields: [{ id: 'files', label: 'Files', type: 'checklist', options: [{ id: 'x', label: 'X file' }, { id: 'y', label: 'Y file' }] }] } }) });
  open();
  const dialog = await openForm();
  await within(dialog).findByText('Files');
  expect(within(dialog).getAllByRole('checkbox').map((c) => (c as HTMLInputElement).checked)).toEqual([true, true]);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].body).toEqual({ values: { files: ['x', 'y'] } });
});

test('a form with no title is still drawn, named for its module action', async () => {
  server({ form: () => ({ body: { fields: [{ id: 'whose', label: 'Whose work', type: 'choice', options: [{ id: 'a', label: 'Amy' }] }] } }) });
  open();
  const dialog = await openForm();
  expect(await within(dialog).findByText('Whose work')).toBeTruthy();
  expect(screen.getByRole('dialog', { name: /Pass review/ })).toBeTruthy();
});

test('a duration below its minimum keeps Submit disabled with a note, and a valid one frees it', async () => {
  const posts = server();
  open();
  const dialog = await openForm();
  await within(dialog).findByText('Time');
  const submit = within(dialog).getByRole('button', { name: 'Submit' }) as HTMLButtonElement;
  expect(submit.disabled).toBe(false);
  fireEvent.input(within(dialog).getByLabelText('Time'), { target: { value: '00:05' } });
  expect(submit.disabled).toBe(true);
  expect(within(dialog).getByText(/00:15/)).toBeTruthy();
  fireEvent.click(submit);
  expect(runPosts(posts)).toHaveLength(0);
  fireEvent.input(within(dialog).getByLabelText('Time'), { target: { value: '00:15' } });
  expect(submit.disabled).toBe(false);
  expect(within(dialog).queryByText(/00:15/)).toBeNull();
});

test('a form that fails to load shows its message in the sheet with a way to close it, and runs nothing', async () => {
  const posts = server({ form: () => ({ status: 502, body: { error: 'plugin unreachable' } }) });
  open();
  const dialog = await openForm();
  expect((await within(dialog).findByRole('alert')).textContent).toContain('plugin unreachable');
  expect(within(dialog).queryByRole('button', { name: 'Submit' })).toBeNull();
  fireEvent.click(within(dialog).getByRole('button', { name: /close|cancel/i }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(runPosts(posts)).toHaveLength(0);
});

test('a module action on a step runs the same way, from the row detail pane', async () => {
  const posts = server({ run: () => ({ body: { message: 'Step skipped' } }) });
  open('ready', true);
  const p = await pane();
  fireEvent.click(await within(p).findByRole('button', { name: 'Skip review' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${BASE}/r1/actions/skip/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
});

test('the run waits longer than the server\'s 60 seconds before the client gives up', async () => {
  let release!: (a: Answer) => void;
  const timers = vi.spyOn(globalThis, 'setTimeout');
  const posts = server({ run: () => new Promise<Answer>((r) => (release = r)) });
  open();
  press(await rowItem(), 'Ping owner');
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(timers.mock.calls.some(([, ms]) => typeof ms === 'number' && ms > 60_000)).toBe(true);
  await act(async () => release({ body: { message: 'late' } }));
});

test('while a run is going its button spins and every module action of that row, on the row and on its steps, is disabled, but another row\'s is not', async () => {
  let release!: (a: Answer) => void;
  server({ run: () => new Promise<Answer>((r) => (release = r)) });
  open('ready', true);
  const p = await pane();
  await within(p).findByText('Code review');
  const row = await rowItem();
  press(row, 'Ping owner');
  await waitFor(() => expect(within(row).getByRole('button', { name: 'Ping owner' }).getAttribute('aria-busy')).toBe('true'));
  for (const name of ['Pass review', 'Ship it']) expect((within(row).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
  for (const name of ['Skip review', 'Run again']) expect((within(p).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
  expect((within(await rowItem('Ship docs')).getByRole('button', { name: 'Archive' }) as HTMLButtonElement).disabled).toBe(false);
  await act(async () => release({ body: { message: 'ok' } }));
});

test('a press on a disabled module action of a running row sends nothing', async () => {
  let release!: (a: Answer) => void;
  const posts = server({ run: () => new Promise<Answer>((r) => (release = r)) });
  open();
  const row = await rowItem();
  press(row, 'Ping owner');
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  fireEvent.click(within(row).getByRole('button', { name: 'Ship it' }));
  fireEvent.click(within(row).getByRole('button', { name: 'Pass review' }));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(posts).toHaveLength(1);
  await act(async () => release({ body: { message: 'ok' } }));
});

test('the server\'s 409 for a second run shows its message like any error', async () => {
  server({ run: () => ({ status: 409, body: { error: 'Another module action is running on this row' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  expect(await screen.findByText('Another module action is running on this row')).toBeTruthy();
});

test('a successful run shows its message as a toast with its link, which opens in a new tab and is its own element', async () => {
  server({ run: () => ({ body: { message: 'Review passed', url: 'https://example.com/t/1' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  expect(await screen.findByText('Review passed')).toBeTruthy();
  const link = screen.getByRole('link') as HTMLAnchorElement;
  expect(link.getAttribute('href')).toBe('https://example.com/t/1');
  expect(link.getAttribute('target')).toBe('_blank');
  expect(link.getAttribute('rel')).toBe('noopener noreferrer');
  expect(link.closest('.toast-body')).toBeNull();
  expect(link.closest('button')).toBeNull();
});

test('a result with no link, or a link that is not http or https, shows its message and no link', async () => {
  server({ run: (_r, a) => ({ body: a === 'ping' ? { message: 'Plain result' } : { message: 'Sneaky result', url: 'javascript:alert(1)' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  expect(await screen.findByText('Plain result')).toBeTruthy();
  expect(screen.queryByRole('link')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(screen.queryByText('Plain result')).toBeNull());
  press(await rowItem('Ship docs'), 'Archive');
  expect(await screen.findByText('Sneaky result')).toBeTruthy();
  expect(screen.queryByRole('link')).toBeNull();
});

test('plugin text in a result is drawn as text', async () => {
  server({ run: () => ({ body: { message: '<img src=x onerror="window.__xss=1"><b>bold</b>' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  expect(await screen.findByText('<img src=x onerror="window.__xss=1"><b>bold</b>')).toBeTruthy();
  expect(document.querySelector('.toasts img, .toasts b')).toBeNull();
});

test('a result toast does not go away on its own', async () => {
  server({ run: () => ({ body: { message: 'Still here' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  await screen.findByText('Still here');
  vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 60_000);
  await new Promise((r) => setTimeout(r, 1200));
  expect(screen.getByText('Still here')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(screen.queryByText('Still here')).toBeNull());
});

test.each([
  [422, 'The plugin refused: no such review'],
  [504, 'The plugin did not answer in time'],
  [502, 'plugin unreachable'],
])('a %i answer shows its error message as a toast, with no link', async (status, error) => {
  server({ run: () => ({ status, body: { error } }) });
  open();
  press(await rowItem(), 'Ping owner');
  expect(await screen.findByText(error)).toBeTruthy();
  expect(screen.queryByRole('link')).toBeNull();
});

test('a form run that fails shows its error toast and the row comes back, not updating', async () => {
  server({ run: () => ({ status: 422, body: { error: 'Time is too short' } }) });
  open();
  const dialog = await openForm();
  await within(dialog).findByText('Time');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  expect(await screen.findByText('Time is too short')).toBeTruthy();
  const row = await rowItem();
  expect(within(row).queryByText('updating…')).toBeNull();
  await waitFor(() => expect((within(row).getByRole('button', { name: 'Ping owner' }) as HTMLButtonElement).disabled).toBe(false));
});

test('after a successful run the row says updating… with its module actions off until the page version moves, then they come back', async () => {
  const posts = server({ run: () => ({ body: { message: 'Pinged' } }) });
  open('ready', true);
  const p = await pane();
  await within(p).findByText('Code review');
  const row = await rowItem();
  press(row, 'Ping owner');
  await screen.findByText('Pinged');
  expect(within(row).getByText('updating…')).toBeTruthy();
  for (const name of ['Pass review', 'Ship it', 'Ping owner']) expect((within(row).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
  for (const name of ['Skip review', 'Run again']) expect((within(p).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
  expect(within(await rowItem('Ship docs')).queryByText('updating…')).toBeNull();
  fireEvent.click(within(row).getByRole('button', { name: 'Ship it' }));
  expect(runPosts(posts)).toHaveLength(1);
  sources.value = [source(2)];
  await waitFor(() => expect(within(row).queryByText('updating…')).toBeNull());
  for (const name of ['Pass review', 'Ship it', 'Ping owner']) expect((within(row).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(false);
  for (const name of ['Skip review', 'Run again']) expect((within(p).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(false);
});

test('a refetch with the same page version does not end updating…', async () => {
  server({ run: () => ({ body: { message: 'Pinged' } }) });
  open();
  const row = await rowItem();
  press(row, 'Ping owner');
  await screen.findByText('Pinged');
  sources.value = [source(1)];
  await new Promise((r) => setTimeout(r, 50));
  expect(within(row).getByText('updating…')).toBeTruthy();
});

test('with no page change after a run, the row\'s module actions come back after the 30 second fallback with no refetch', async () => {
  const real = globalThis.setTimeout;
  const fallback: (() => void)[] = [];
  vi.spyOn(globalThis, 'setTimeout').mockImplementation(((fn: () => void, ms?: number, ...rest: unknown[]) => (ms === 30_000 ? (fallback.push(fn), 0) : real(fn, ms, ...rest))) as typeof setTimeout);
  const posts = server({ run: () => ({ body: { message: 'Pinged' } }) });
  open();
  const row = await rowItem();
  press(row, 'Ping owner');
  await screen.findByText('Pinged');
  expect(within(row).getByText('updating…')).toBeTruthy();
  const pageGets = () => (fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([p, i]) => String(p).endsWith('/page') && !(i as RequestInit | undefined)?.method).length;
  const before = pageGets();
  await act(async () => fallback.at(-1)!());
  expect(within(row).queryByText('updating…')).toBeNull();
  expect((within(row).getByRole('button', { name: 'Ping owner' }) as HTMLButtonElement).disabled).toBe(false);
  expect(pageGets()).toBe(before);
  expect(runPosts(posts)).toHaveLength(1);
});

test.each(['ready', 'auth_required', 'error'])('a built-in module action with a url stays a disabled button when the page is %s and a press sends nothing', async (state) => {
  const posts = server({ page: () => pageOf(state) });
  open(state);
  const row = await rowItem();
  const b = within(row).getByRole('button', { name: 'Open docs' }) as HTMLButtonElement;
  expect(b.disabled).toBe(true);
  fireEvent.click(b);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(posts).toHaveLength(0);
});

test('while the page is in error every module action on rows and steps is disabled, and a press sends nothing', async () => {
  let state = 'ready';
  const posts = server({ page: () => pageOf(state) });
  open('ready', true);
  const p = await pane();
  await within(p).findByText('Code review');
  const row = await rowItem();
  const names = [
    [row, ['Pass review', 'Ship it', 'Ping owner']],
    [p, ['Skip review', 'Run again']],
  ] as const;
  for (const [scope, list] of names) for (const name of list) expect((within(scope).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(false);
  state = 'error';
  sources.value = [source(2, 'error')];
  await screen.findByText('plugin crashed');
  for (const [scope, list] of names)
    for (const name of list) {
      const b = within(scope).getByRole('button', { name }) as HTMLButtonElement;
      expect(b.disabled).toBe(true);
      fireEvent.click(b);
    }
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(posts).toHaveLength(0);
});

test('a page that needs sign-in keeps its module actions on', async () => {
  const posts = server({ page: () => ({ ...pageOf('auth_required'), auth: { message: 'Sign in to Deploys' } }) });
  open('auth_required');
  const row = await rowItem();
  for (const name of ['Pass review', 'Ship it', 'Ping owner']) expect((within(row).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(false);
  press(row, 'Ping owner');
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
});

test('module actions come back when the page leaves the error state', async () => {
  server({ page: () => pageOf(state) });
  let state = 'error';
  open('error');
  const row = await rowItem();
  expect((within(row).getByRole('button', { name: 'Ping owner' }) as HTMLButtonElement).disabled).toBe(true);
  state = 'ready';
  sources.value = [source(2, 'ready')];
  await waitFor(() => expect((within(row).getByRole('button', { name: 'Ping owner' }) as HTMLButtonElement).disabled).toBe(false));
});
