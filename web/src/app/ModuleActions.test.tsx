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
  detail?: () => unknown;
}

// Serves the page, the row and the module action routes, and records every POST.
function server(h: Handlers = {}) {
  const posts: Post[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.endsWith('/page')) return new Response(JSON.stringify(h.page ? h.page() : pageOf()));
      if (method === 'GET' && path.endsWith('/rows/r1')) return new Response(JSON.stringify(h.detail ? h.detail() : detail));
      const m = /\/(?:rows|notices)\/([^/]+)\/actions\/([^/]+)\/(form|run)$/.exec(path);
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

// The row's own Open link is also a link, so a result's link is looked for in the toasts.
const toasts = () => within(document.querySelector('.toasts') as HTMLElement);

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

test('a duration takes a typed colon, as a plain text input an iPhone keypad can type ":" into', async () => {
  const posts = server();
  open();
  const dialog = await openForm();
  const time = (await within(dialog).findByLabelText('Time')) as HTMLInputElement;
  expect(time.inputMode).not.toBe('numeric');
  fireEvent.input(time, { target: { value: '00:45' } });
  expect(time.value).toBe('00:45');
  expect((within(dialog).getByRole('button', { name: 'Submit' }) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  await waitFor(() => expect(runPosts(posts)[0].body).toMatchObject({ values: { time: '00:45' } }));
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
  expect(within(dialog).getByText('At least 15 min.')).toBeTruthy();
  fireEvent.click(submit);
  expect(runPosts(posts)).toHaveLength(0);
  fireEvent.input(within(dialog).getByLabelText('Time'), { target: { value: '00:15' } });
  expect(submit.disabled).toBe(false);
  expect(within(dialog).queryByText('At least 15 min.')).toBeNull();
});

const durationForm = (extra: Record<string, unknown> = {}) => () => ({ body: { title: 'Close task', fields: [{ id: 'time', label: 'Time spent', type: 'duration', ...extra }] } });

test('a duration reads 1:30, 0:1, 90, 1h30 and 1.5 alike, says what it read, and sends the plugin hh:mm', async () => {
  const posts = server({ form: durationForm() });
  open();
  const dialog = await openForm();
  const time = await within(dialog).findByLabelText('Time spent');
  for (const [typed, read] of [['1:30', '= 1 h 30 min'], ['0:1', '= 1 min'], ['90', '= 1 h 30 min'], ['1h30', '= 1 h 30 min'], ['1.5', '= 1 h 30 min']]) {
    fireEvent.input(time, { target: { value: typed } });
    expect(within(dialog).getByText(read)).toBeTruthy();
  }
  fireEvent.input(time, { target: { value: '1h 5m' } });
  expect(within(dialog).getByText(/Saved as 01:05/)).toBeTruthy();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].body).toEqual({ values: { time: '01:05' } });
});

test('an empty duration keeps Submit disabled with no note, and one it cannot read says how to type it', async () => {
  server({ form: durationForm() });
  open();
  const dialog = await openForm();
  const time = await within(dialog).findByLabelText('Time spent');
  const submit = within(dialog).getByRole('button', { name: 'Submit' }) as HTMLButtonElement;
  expect(submit.disabled).toBe(true);
  expect(dialog.querySelector('.mform-note')).toBeNull();
  fireEvent.input(time, { target: { value: 'soon' } });
  expect(within(dialog).getByText(/Couldn't read that/)).toBeTruthy();
  expect(submit.disabled).toBe(true);
  fireEvent.input(time, { target: { value: '100:00' } });
  expect(submit.disabled).toBe(true);
});

test('a quick pick fills the duration and shows as picked, and no pick under the minimum is offered', async () => {
  const posts = server({ form: durationForm({ min: '00:30' }) });
  open();
  const dialog = await openForm();
  await within(dialog).findByLabelText('Time spent');
  expect(within(dialog).queryByRole('button', { name: '15m' })).toBeNull();
  fireEvent.click(within(dialog).getByRole('button', { name: '1h' }));
  expect((within(dialog).getByLabelText('Time spent') as HTMLInputElement).value).toBe('1h');
  expect(within(dialog).getByRole('button', { name: '1h' }).getAttribute('aria-pressed')).toBe('true');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  await waitFor(() => expect(runPosts(posts)[0].body).toEqual({ values: { time: '01:00' } }));
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
  const link = toasts().getByRole('link') as HTMLAnchorElement;
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
  expect(toasts().queryByRole('link')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(screen.queryByText('Plain result')).toBeNull());
  press(await rowItem('Ship docs'), 'Archive');
  expect(await screen.findByText('Sneaky result')).toBeTruthy();
  expect(toasts().queryByRole('link')).toBeNull();
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
  expect(toasts().queryByRole('link')).toBeNull();
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

test('when the page version moves while a run is pending, the answer drops the run at once with no updating…', async () => {
  let release!: (a: Answer) => void;
  server({ run: () => new Promise<Answer>((r) => (release = r)) });
  open();
  const row = await rowItem();
  press(row, 'Ping owner');
  await waitFor(() => expect(within(row).getByRole('button', { name: 'Ping owner' }).getAttribute('aria-busy')).toBe('true'));
  sources.value = [source(2)];
  await act(async () => release({ body: { message: 'Pinged' } }));
  await screen.findByText('Pinged');
  expect(within(row).queryByText('updating…')).toBeNull();
  for (const name of ['Pass review', 'Ship it', 'Ping owner']) expect((within(row).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(false);
});

test('a module action whose row id or module action id is . or .. is disabled and a press sends nothing', async () => {
  const dots = { id: '..', label: 'Dotted', confirm: 'click' };
  const odd = {
    state: 'ready',
    waiting: 0,
    groups: [{ id: 'g', title: 'G', rows: [{ id: '.', title: 'Dot row', subtitle: '', chips: [], reason: '', action: ping }, { id: 'r9', title: 'Plain row', subtitle: '', chips: [], reason: '', actions: [dots, ping] }] }],
  };
  const posts = server({ page: () => odd });
  open();
  const dotRow = await rowItem('Dot row');
  const plain = await rowItem('Plain row');
  const buttons = [within(dotRow).getByRole('button', { name: 'Ping owner' }), within(plain).getByRole('button', { name: 'Dotted' })] as HTMLButtonElement[];
  for (const b of buttons) {
    expect(b.disabled).toBe(true);
    fireEvent.click(b);
  }
  expect((within(plain).getByRole('button', { name: 'Ping owner' }) as HTMLButtonElement).disabled).toBe(false);
  expect(posts).toHaveLength(0);
});

// Actions on a notice: the same confirm kinds, form and run as a row's, addressed by notice id.
const NBASE = '/api/v1/sources/deploys/notices';
const pick = { id: 'project', label: 'Pick your project', confirm: 'form', summary: 'Choose one' };
const sync = { id: 'sync', label: 'Sync now', confirm: 'click' };
const reset = { id: 'reset', label: 'Reset setup', confirm: 'yesno', summary: 'Forget the setup?' };
const withNotices = (state = 'ready', notices: unknown[] = []) => () => ({ ...pageOf(state), notices });
const notices = [
  { id: 'pick', text: 'Setup needed', tone: 'warn', action: pick },
  { id: 'sync', text: 'Out of date', tone: 'info', action: sync },
  { id: 'reset', text: 'Old setup', tone: 'info', action: reset },
  { id: 'docs', text: 'Read the docs', tone: 'info', action: docs },
  { id: 'plain', text: 'Nothing to press', tone: 'info' },
  { id: 'r1', text: 'Same id as a row', tone: 'info', action: sync },
];
const noticeItem = async (text: string) => (await screen.findByText(text)).closest('li') as HTMLElement;

test('a notice with no action draws no button', async () => {
  server({ page: withNotices('ready', notices) });
  open();
  expect(within(await noticeItem('Nothing to press')).queryByRole('button')).toBeNull();
});

test('a click action on a notice runs at once with no values and no sheet', async () => {
  const posts = server({ page: withNotices('ready', notices), run: () => ({ body: { message: 'Synced' } }) });
  open();
  press(await noticeItem('Out of date'), 'Sync now');
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${NBASE}/sync/actions/sync/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(formPosts(posts)).toHaveLength(0);
  expect(await screen.findByText('Synced')).toBeTruthy();
});

test('a yesno action on a notice shows its summary first and runs only once confirmed', async () => {
  const posts = server({ page: withNotices('ready', notices) });
  open();
  press(await noticeItem('Old setup'), 'Reset setup');
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText('Forget the setup?')).toBeTruthy();
  expect(posts).toHaveLength(0);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Reset setup' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${NBASE}/reset/actions/reset/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});

test('a form action on a notice fetches its form from the notice and submitting runs it with the values', async () => {
  const posts = server({ page: withNotices('ready', notices), run: () => ({ body: { message: 'Project set' } }) });
  open();
  press(await noticeItem('Setup needed'), 'Pick your project');
  const dialog = await screen.findByRole('dialog', { name: 'Pass this review' });
  expect(formPosts(posts)[0].path).toBe(`${NBASE}/pick/actions/project/form`);
  await within(dialog).findByText('Whose work');
  fireEvent.click(within(dialog).getByRole('radio', { name: 'Amy' }));
  fireEvent.click(within(dialog).getByRole('button', { name: 'Submit' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${NBASE}/pick/actions/project/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: { whose: 'a', files: ['y'], time: '01:30' } });
  expect(await screen.findByText('Project set')).toBeTruthy();
});

test('a notice run that fails shows its error toast and the notice comes back, not updating', async () => {
  server({ page: withNotices('ready', notices), run: () => ({ status: 422, body: { error: 'No such project' } }) });
  open();
  const item = await noticeItem('Out of date');
  press(item, 'Sync now');
  expect(await screen.findByText('No such project')).toBeTruthy();
  expect(within(item).queryByText('updating…')).toBeNull();
  await waitFor(() => expect((within(item).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement).disabled).toBe(false));
});

test('after a successful run the notice says updating… beside its disabled button until the page version moves', async () => {
  const posts = server({ page: withNotices('ready', notices), run: () => ({ body: { message: 'Synced' } }) });
  open();
  const item = await noticeItem('Out of date');
  press(item, 'Sync now');
  await screen.findByText('Synced');
  expect(within(item).getByText('updating…')).toBeTruthy();
  expect((within(item).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement).disabled).toBe(true);
  expect(within(await noticeItem('Old setup')).queryByText('updating…')).toBeNull();
  fireEvent.click(within(item).getByRole('button', { name: 'Sync now' }));
  expect(runPosts(posts)).toHaveLength(1);
  sources.value = [source(2)];
  await waitFor(() => expect(within(item).queryByText('updating…')).toBeNull());
  expect((within(item).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement).disabled).toBe(false);
});

test('a second press on a notice is refused while its run is pending', async () => {
  let release!: (a: Answer) => void;
  const posts = server({ page: withNotices('ready', notices), run: () => new Promise<Answer>((r) => (release = r)) });
  open();
  const item = await noticeItem('Out of date');
  press(item, 'Sync now');
  await waitFor(() => expect(within(item).getByRole('button', { name: 'Sync now' }).getAttribute('aria-busy')).toBe('true'));
  fireEvent.click(within(item).getByRole('button', { name: 'Sync now' }));
  expect(runPosts(posts)).toHaveLength(1);
  await act(async () => release({ body: { message: 'Synced' } }));
});

test('a running row does not disable or mark a notice with the same id, and the other way round', async () => {
  let release!: (a: Answer) => void;
  const posts = server({ page: withNotices('ready', notices), run: () => new Promise<Answer>((r) => (release = r)) });
  open();
  const row = await rowItem();
  const item = await noticeItem('Same id as a row');
  press(row, 'Ping owner');
  await waitFor(() => expect(within(row).getByRole('button', { name: 'Ping owner' }).getAttribute('aria-busy')).toBe('true'));
  expect((within(item).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement).disabled).toBe(false);
  press(item, 'Sync now');
  await waitFor(() => expect(runPosts(posts)).toHaveLength(2));
  expect(runPosts(posts).map((p) => p.path)).toEqual([`${BASE}/r1/actions/ping/run`, `${NBASE}/r1/actions/sync/run`]);
  await act(async () => release({ body: { message: 'Done one' } }));
});

test('a finished row run does not show updating… on a notice with the same id, and a finished notice run not on the row', async () => {
  server({ page: withNotices('ready', notices) });
  open();
  const row = await rowItem();
  const item = await noticeItem('Same id as a row');
  press(row, 'Ping owner');
  await screen.findByText('Done');
  expect(within(row).getByText('updating…')).toBeTruthy();
  expect(within(item).queryByText('updating…')).toBeNull();
  expect((within(item).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement).disabled).toBe(false);
  sources.value = [source(2)];
  await waitFor(() => expect(within(row).queryByText('updating…')).toBeNull());
  press(item, 'Sync now');
  await waitFor(() => expect(within(item).getByText('updating…')).toBeTruthy());
  expect(within(row).queryByText('updating…')).toBeNull();
  expect((within(row).getByRole('button', { name: 'Ping owner' }) as HTMLButtonElement).disabled).toBe(false);
});

test('a row and a notice with the same id each keep their own result toast', async () => {
  server({ page: withNotices('ready', notices), run: (_id, action) => ({ body: { message: action === 'ping' ? 'Row pinged' : 'Notice synced' } }) });
  open();
  press(await rowItem(), 'Ping owner');
  await screen.findByText('Row pinged');
  press(await noticeItem('Same id as a row'), 'Sync now');
  await screen.findByText('Notice synced');
  expect(screen.getByText('Row pinged')).toBeTruthy();
});

test.each([
  ['Open link', { id: 'o', label: 'Open it', builtin: 'open', url: 'https://tracker.example/docs' }],
  ['Launch', { id: 'l', label: 'Start ship', builtin: 'launch', url: 'https://github.com/acme/widgets/issues/7' }],
  ['Message AdmiralBot', { id: 'm', label: 'Tell the bot', builtin: 'message', kind: 'review', ref: 'A-1', title: 'T', link: 'https://tracker.example/t/1' }],
])('a %s action on a notice is not drawn: built-in actions belong to rows', async (_kind, action) => {
  const posts = server({ page: withNotices('ready', [{ id: 'bi', text: 'Built-in here', tone: 'info', action }]) });
  open();
  const item = await noticeItem('Built-in here');
  expect(within(item).queryByText(action.label)).toBeNull();
  expect(within(item).queryByRole('button')).toBeNull();
  expect(posts).toHaveLength(0);
});

test('while the page is in error a notice action is disabled and a press sends nothing', async () => {
  const posts = server({ page: withNotices('error', notices) });
  open('error');
  const b = within(await noticeItem('Out of date')).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement;
  expect(b.disabled).toBe(true);
  fireEvent.click(b);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(posts).toHaveLength(0);
});

test('a notice whose id is . or .. is disabled and a press sends nothing', async () => {
  const odd = [
    { id: '.', text: 'Dot notice', tone: 'info', action: sync },
    { id: '..', text: 'Dotdot notice', tone: 'info', action: sync },
    { id: 'ok', text: 'Fine notice', tone: 'info', action: sync },
  ];
  const posts = server({ page: withNotices('ready', odd) });
  open();
  for (const text of ['Dot notice', 'Dotdot notice']) {
    const b = within(await noticeItem(text)).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement;
    expect(b.disabled).toBe(true);
    fireEvent.click(b);
  }
  expect((within(await noticeItem('Fine notice')).getByRole('button', { name: 'Sync now' }) as HTMLButtonElement).disabled).toBe(false);
  expect(posts).toHaveLength(0);
});

const snooze = { id: 'snooze', label: 'Snooze', confirm: 'click', menu: true };
const wipe = { id: 'wipe', label: 'Wipe cache', confirm: 'yesno', summary: 'Wipe the build cache', menu: true };
const tag = { id: 'tag', label: 'Tag release', confirm: 'click', menu: true };
const trigger = (scope: HTMLElement) => within(scope).getByRole('button', { name: 'More actions' }) as HTMLButtonElement;
const itemNames = () => screen.queryAllByRole('menuitem').map((i) => i.textContent?.trim());
const closeMenu = async () => {
  fireEvent.keyDown(document.body, { key: 'Escape' });
  await waitFor(() => expect(screen.queryAllByRole('menuitem')).toHaveLength(0));
};

const menuPage = (row: Record<string, unknown>, state = 'ready') => () => ({
  state,
  waiting: 0,
  ...(state === 'error' ? { error: 'plugin crashed' } : {}),
  groups: [{ id: 'review', title: 'Needs review', rows: [{ id: 'r1', title: 'Fix login', subtitle: '', chips: [], reason: '', ...row }] }],
});

test('a row with two button actions and three menu actions shows two buttons and a ⋯ trigger that lists the three in order', async () => {
  server({ page: menuPage({ actions: [ping, snooze, ship, wipe, tag] }) });
  open();
  const row = await rowItem();
  const buttons = within(row).getAllByRole('button').map((b) => b.getAttribute('aria-label') ?? b.textContent?.trim());
  expect(buttons).toEqual(['Open Fix login', 'Ping owner', 'Ship it', 'More actions']);
  expect(within(row).queryByRole('button', { name: 'Snooze' })).toBeNull();
  expect(screen.queryByRole('menu')).toBeNull();
  fireEvent.click(trigger(row));
  expect(await screen.findByRole('menu')).toBeTruthy();
  expect(itemNames()).toEqual(['Snooze', 'Wipe cache', 'Tag release']);
});

test('a group with exactly one menu action still shows the ⋯ trigger and not a button', async () => {
  server({ page: menuPage({ actions: [snooze] }) });
  open();
  const row = await rowItem();
  expect(within(row).queryByRole('button', { name: 'Snooze' })).toBeNull();
  fireEvent.click(trigger(row));
  await screen.findByRole('menu');
  expect(itemNames()).toEqual(['Snooze']);
});

test('a menu action with no button actions beside it still draws only the ⋯ trigger', async () => {
  server({ page: menuPage({ action: pass, actions: [snooze] }) });
  open();
  const row = await rowItem();
  expect(within(row).getByRole('button', { name: 'Pass review' })).toBeTruthy();
  expect(trigger(row)).toBeTruthy();
});

test('choosing a click menu item runs it at once with no sheet', async () => {
  const posts = server({ page: menuPage({ actions: [ping, snooze] }) });
  open();
  const row = await rowItem();
  fireEvent.click(trigger(row));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Snooze' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${BASE}/r1/actions/snooze/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
  expect(screen.queryByRole('dialog')).toBeNull();
});

test('choosing a yesno menu item opens the same confirm its button would, and confirming sends the same run', async () => {
  const posts = server({ page: menuPage({ actions: [wipe] }) });
  open();
  fireEvent.click(trigger(await rowItem()));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Wipe cache' }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText('Wipe the build cache')).toBeTruthy();
  expect(posts).toHaveLength(0);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Wipe cache' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  expect(runPosts(posts)[0].path).toBe(`${BASE}/r1/actions/wipe/run`);
  expect(runPosts(posts)[0].body).toEqual({ values: {} });
});

test('choosing a form menu item fetches its form like its button would', async () => {
  const posts = server({ page: menuPage({ actions: [{ ...pass, menu: true }] }) });
  open();
  fireEvent.click(trigger(await rowItem()));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Pass review' }));
  await screen.findByRole('dialog');
  expect(formPosts(posts)[0].path).toBe(`${BASE}/r1/actions/pass/form`);
});

test('on a row with a target a menu action of the target step goes in the menu beside the target, one with no step in the menu after the divider, and one naming another step only on that step\'s card', async () => {
  const onTarget = { id: 'on-target', label: 'Hold deploy', confirm: 'click', menu: true, step: 's1' };
  const rowLevel = { id: 'row-level', label: 'Mute row', confirm: 'click', menu: true };
  const other = { id: 'on-other', label: 'Rerun lint', confirm: 'click', menu: true, step: 's2' };
  const twoSteps = {
    sections: [{ kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [] }, { id: 's2', title: 'Lint', chips: [] }] }],
  };
  server({ page: menuPage({ target: { label: 'Deploy', step: 's1' }, actions: [onTarget, rowLevel, other] }), detail: () => twoSteps });
  open();
  const row = await rowItem();
  const triggers = within(row).getAllByRole('button', { name: 'More actions' });
  expect(triggers).toHaveLength(2);
  const targetGroup = within(row).getByRole('group', { name: 'Acts on Deploy' });
  expect(targetGroup.contains(triggers[0])).toBe(true);
  expect(targetGroup.contains(triggers[1])).toBe(false);
  expect(row.querySelector('[role="separator"]')?.compareDocumentPosition(triggers[1])).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  fireEvent.click(triggers[0]);
  await screen.findByRole('menu');
  expect(itemNames()).toEqual(['Hold deploy']);
  await closeMenu();
  fireEvent.click(triggers[1]);
  await screen.findByRole('menu');
  expect(itemNames()).toEqual(['Mute row']);
  await closeMenu();

  cleanup();
  go('/app/deploys/r1', '');
  open('ready', true);
  const p = await pane();
  const card = (await within(p).findByText('Lint')).closest('li') as HTMLElement;
  fireEvent.click(trigger(card));
  await screen.findByRole('menu');
  expect(itemNames()).toEqual(['Rerun lint']);
  await closeMenu();
  const code = within(p).getByText('Code review').closest('li') as HTMLElement;
  expect(within(code).queryByRole('button', { name: 'More actions' })).toBeNull();
});

test('a step card draws its own menu when the row has no target', async () => {
  const stepMenu = { id: 'step-menu', label: 'Defer step', confirm: 'click', menu: true };
  server({ detail: () => ({ sections: [{ kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [], actions: [again, stepMenu] }] }] }) });
  open('ready', true);
  const p = await pane();
  const card = (await within(p).findByText('Code review')).closest('li') as HTMLElement;
  expect(within(card).getByRole('button', { name: 'Run again' })).toBeTruthy();
  fireEvent.click(trigger(card));
  await screen.findByRole('menu');
  expect(itemNames()).toEqual(['Defer step']);
});

test('the ⋯ trigger is disabled while the page is in error', async () => {
  const posts = server({ page: menuPage({ actions: [ping, snooze] }, 'error') });
  open('error');
  await screen.findByText('plugin crashed');
  const t = trigger(await rowItem());
  expect(t.disabled).toBe(true);
  fireEvent.click(t);
  expect(screen.queryByRole('menu')).toBeNull();
  expect(posts).toHaveLength(0);
});

test('the ⋯ trigger is disabled while a run is in progress on its row, but not on another row', async () => {
  let release!: (a: Answer) => void;
  server({
    page: () => ({
      state: 'ready',
      waiting: 0,
      groups: [{ id: 'g', title: 'G', rows: [
        { id: 'r1', title: 'Fix login', chips: [], actions: [ping, snooze] },
        { id: 'r3', title: 'Ship docs', chips: [], actions: [archive, snooze] },
      ] }],
    }),
    run: () => new Promise<Answer>((r) => (release = r)),
  });
  open();
  const row = await rowItem();
  press(row, 'Ping owner');
  await waitFor(() => expect(trigger(row).disabled).toBe(true));
  expect(trigger(await rowItem('Ship docs')).disabled).toBe(false);
  await act(async () => release({ body: { message: 'ok' } }));
});

test('while a menu item runs the trigger shows the spinner and aria-busy', async () => {
  let release!: (a: Answer) => void;
  const posts = server({ page: menuPage({ actions: [snooze] }), run: () => new Promise<Answer>((r) => (release = r)) });
  open();
  const row = await rowItem();
  expect(trigger(row).getAttribute('aria-busy')).toBeNull();
  fireEvent.click(trigger(row));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Snooze' }));
  await waitFor(() => expect(runPosts(posts)).toHaveLength(1));
  await waitFor(() => expect(trigger(row).getAttribute('aria-busy')).toBe('true'));
  expect(trigger(row).querySelector('.spinner')).not.toBeNull();
  expect(trigger(row).disabled).toBe(true);
  await act(async () => release({ body: { message: 'ok' } }));
});

test('a menu item whose id is . is disabled on its own while the trigger stays usable, and choosing it sends nothing', async () => {
  const posts = server({ page: menuPage({ actions: [{ ...snooze, id: '.' }, tag] }) });
  open();
  const row = await rowItem();
  expect(trigger(row).disabled).toBe(false);
  fireEvent.click(trigger(row));
  const dot = await screen.findByRole('menuitem', { name: 'Snooze' });
  expect(dot.hasAttribute('disabled') || dot.getAttribute('aria-disabled') === 'true').toBe(true);
  fireEvent.click(dot);
  expect(posts).toHaveLength(0);
  const ok = screen.getByRole('menuitem', { name: 'Tag release' });
  expect(ok.hasAttribute('disabled') || ok.getAttribute('aria-disabled') === 'true').toBe(false);
});

test('Escape and a press outside close the menu', async () => {
  server({ page: menuPage({ actions: [snooze] }) });
  open();
  fireEvent.click(trigger(await rowItem()));
  await screen.findByRole('menu');
  await closeMenu();
  fireEvent.click(trigger(await rowItem()));
  await screen.findByRole('menu');
  fireEvent.pointerDown(document.body);
  await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
});
