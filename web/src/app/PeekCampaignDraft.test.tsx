import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Entry } from './inbox';
import { campaignList } from './campaignsLink';
import { Peek, peekKeys } from './Peek';

const nav = vi.hoisted(() => ({ go: vi.fn() }));
vi.mock('./router', async (orig) => ({ ...(await orig<typeof import('./router')>()), go: nav.go }));

const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const checkout = (name: string, ...remotes: string[]) => ({ path: `/work/${name}`, name, kind: 'checkout', remotes });

interface Sent {
  path: string;
  method: string;
  body: unknown;
}
let sent: Sent[];
let repos: ReturnType<typeof checkout>[];
let sources: { key: string; plugin: string; fleet: string; name: string }[];
let hosts: Record<string, string>;
let createAnswer: () => Response | Promise<Response>;
let dismissAnswer: () => Response | Promise<Response>;

beforeEach(() => {
  sent = [];
  nav.go.mockClear();
  localStorage.clear();
  campaignList.value = [];
  repos = [checkout('widgets', 'github.com/acme/widgets'), checkout('api', 'github.com/acme/api')];
  sources = [{ key: 'github', plugin: 'github', fleet: 'zulu', name: 'github' }];
  hosts = { github: 'github.com' };
  createAnswer = () => json({ campaign: { id: 'camp-9', url: 'https://github.com/acme/widgets/issues/99' } }, 201);
  dismissAnswer = () => new Response(null, { status: 204 });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const route = new URL(path, 'http://admiral.test').pathname.replace('/api/v1', '');
      const method = init.method ?? 'GET';
      sent.push({ path: route, method, body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (route === '/inbox/_admiral/draft-map/create') return createAnswer();
      if (route === '/inbox/_admiral/draft-map/dismiss') return dismissAnswer();
      if (route === '/fleets/zulu/repos') return json({ repos });
      if (route === '/campaigns') return json({ campaigns: [] });
      if (route === '/sources') return json(sources.map((s) => ({ ...s, icon_url: '', has_schema: true, state: 'ok', error: null })));
      const settings = /^\/sources\/([^/]+)\/settings$/.exec(route);
      if (settings) return json({ name: settings[1], settings: { account: 'octo', host: hosts[settings[1]] } });
      throw new Error(`unstubbed ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const entry = (draft: Record<string, unknown> = {}): Entry =>
  ({
    key: 'item:_admiral/draft-map',
    type: 'item',
    session: '_admiral',
    id: 'draft-map',
    folded: null,
    since: 0,
    fleet: { key: 'zulu', name: 'Zulu', color: '#2a9d8f' },
    item: {
      id: 'draft-map',
      kind: 'campaign_draft',
      name: 'AdmiralBot',
      title: 'AdmiralBot drafted a campaign',
      fleet: 'zulu',
      campaign_draft: { fleet: 'zulu', repo: 'acme/widgets', title: 'Speed up the fleet map', body: 'Load the map in **under** a second.', via: 'admiralbot', ...draft },
    },
  }) as unknown as Entry;

const open = (e: Entry = entry(), fold = vi.fn()) => ({ fold, ...render(<Peek entry={e} fold={fold} />) });
const title = () => screen.findByRole('textbox', { name: 'Title' }) as Promise<HTMLInputElement>;
const create = () => screen.getByRole('button', { name: 'Create campaign' }) as HTMLButtonElement;
const posts = (suffix: string) => sent.filter((s) => s.method === 'POST' && s.path.endsWith(suffix));

test('the Peek reads "AdmiralBot drafted a campaign via AdmiralBot", names the campaign below it and shows no expiry', async () => {
  open();
  await title();
  const head = screen.getByRole('heading', { level: 2 });
  expect(head.textContent).toMatch(/^AdmiralBot\s+drafted a campaign\s+via AdmiralBot\s*$/);
  expect(document.querySelector('.peek-title')!.textContent).toBe('Speed up the fleet map');
  expect(document.querySelector('.peek')!.textContent).not.toMatch(/open until|expires/i);
});

test('the Peek shows a hostile model-written title as text', async () => {
  open(entry({ title: HOSTILE }));
  await title();
  expect(document.querySelector('.peek-who img, .peek-who script')).toBeNull();
  expect(document.querySelector('.peek-title')!.textContent).toContain('<img src=x');
});

test('the item opens the modal\'s confirm step with the draft\'s title, description and repo, all editable', async () => {
  open();
  const box = await title();
  expect(box.value).toBe('Speed up the fleet map');
  const body = screen.getByRole('textbox', { name: 'Description' }) as HTMLTextAreaElement;
  expect(body.value).toBe('Load the map in **under** a second.');
  const repo = screen.getByRole('combobox', { name: 'Repo for the parent issue' }) as HTMLSelectElement;
  expect(repo.value).toBe('github.com/acme/widgets');
  fireEvent.input(box, { target: { value: 'Speed up the map' } });
  fireEvent.input(body, { target: { value: 'My words' } });
  fireEvent.change(repo, { target: { value: 'github.com/acme/api' } });
  expect(box.value).toBe('Speed up the map');
  expect(body.value).toBe('My words');
  expect(repo.value).toBe('github.com/acme/api');
});

test('the confirm step in the Peek has no Back, and does not take focus when the item opens from a link', async () => {
  open();
  const box = await title();
  expect(screen.queryByRole('button', { name: 'Back' })).toBeNull();
  expect(document.activeElement).not.toBe(box);
});

test('the draft\'s repo is matched without regard to case', async () => {
  open(entry({ repo: 'Acme/Widgets' }));
  await title();
  expect((screen.getByRole('combobox', { name: 'Repo for the parent issue' }) as HTMLSelectElement).value).toBe('github.com/acme/widgets');
});

test('only github.com projects are offered, since the create body carries no host', async () => {
  repos = [checkout('widgets', 'github.com/acme/widgets'), checkout('legacy', 'ghe.example.com/acme/legacy')];
  sources = [
    { key: 'github', plugin: 'github', fleet: 'zulu', name: 'github' },
    { key: 'ghe', plugin: 'github', fleet: 'zulu', name: 'ghe' },
  ];
  hosts = { github: 'github.com', ghe: 'ghe.example.com' };
  open();
  await title();
  const options = within(screen.getByRole('combobox', { name: 'Repo for the parent issue' })).getAllByRole('option');
  expect(options.map((o) => (o as HTMLOptionElement).value)).toEqual(['github.com/acme/widgets']);
});

test('a draft repo that is not among the offered projects preselects nothing, says so, and holds Create until the engineer picks', async () => {
  open(entry({ repo: 'acme/gone' }));
  await title();
  expect(document.querySelector('.peek')!.textContent).toContain('acme/gone');
  expect(create().disabled).toBe(true);
  fireEvent.change(screen.getByRole('combobox', { name: 'Repo for the parent issue' }), { target: { value: 'github.com/acme/api' } });
  expect(create().disabled).toBe(false);
  fireEvent.click(create());
  await waitFor(() => expect(posts('/create')).toHaveLength(1));
  expect(posts('/create')[0].body).toMatchObject({ repo: 'acme/api' });
});

test('Create sends the edited draft to the item\'s create action, not the campaign route, then opens the new campaign in the item\'s fleet', async () => {
  open();
  fireEvent.input(await title(), { target: { value: 'Speed up the map' } });
  fireEvent.click(create());
  await waitFor(() => expect(nav.go).toHaveBeenCalledWith('/app/campaigns/camp-9', '?fleet=zulu'));
  expect(posts('/create')).toEqual([{ path: '/inbox/_admiral/draft-map/create', method: 'POST', body: { repo: 'acme/widgets', title: 'Speed up the map', body: 'Load the map in **under** a second.' } }]);
  expect(sent.some((s) => s.path === '/campaigns/create')).toBe(false);
  expect(sent.some((s) => s.path === '/campaigns' && s.method === 'GET')).toBe(true);
  expect(localStorage.getItem('admiral.campaign-repo')).toBeNull();
  expect(nav.go).toHaveBeenCalledTimes(1);
});

test('Create still opens the campaign when the Inbox push has already closed the Peek', async () => {
  let answer: (r: Response) => void = () => {};
  createAnswer = () => new Promise<Response>((r) => (answer = r));
  const { unmount } = open();
  await title();
  fireEvent.click(create());
  await waitFor(() => expect(posts('/create')).toHaveLength(1));
  unmount();
  answer(json({ campaign: { id: 'camp-9', url: 'https://github.com/acme/widgets/issues/99' } }, 201));
  await waitFor(() => expect(nav.go).toHaveBeenCalledWith('/app/campaigns/camp-9', '?fleet=zulu'));
});

test('a refusal before any GitHub write shows the server\'s reason and keeps Create for another try', async () => {
  createAnswer = () => json({ error: 'repo acme/widgets is not in this fleet' }, 422);
  open();
  await title();
  fireEvent.click(create());
  expect((await screen.findByRole('alert')).textContent).toContain('repo acme/widgets is not in this fleet');
  expect(create().disabled).toBe(false);
  expect(nav.go).not.toHaveBeenCalled();
});

test('a failure with no parent keeps the item whatever the status, so Create can be pressed again', async () => {
  createAnswer = () => json({ error: 'gh is signed out' }, 502);
  open();
  await title();
  fireEvent.click(create());
  await screen.findByRole('alert');
  expect(create().disabled).toBe(false);
  fireEvent.click(create());
  await waitFor(() => expect(posts('/create')).toHaveLength(2));
});

test('a failure after the parent exists links the parent, says it was not followed and cannot file a second', async () => {
  createAnswer = () => json({ error: 'follow failed: gh is signed out', parent: 'https://github.com/acme/widgets/issues/99', answered: true }, 500);
  open();
  await title();
  fireEvent.click(create());
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toContain('follow failed: gh is signed out');
  expect(within(alert).getByRole('link').getAttribute('href')).toBe('https://github.com/acme/widgets/issues/99');
  expect(create().disabled).toBe(true);
  expect(posts('/create')).toHaveLength(1);
  expect(nav.go).not.toHaveBeenCalled();
});

test('a busy item (409) says to try again and keeps Create', async () => {
  createAnswer = () => json({ error: 'busy' }, 409);
  open();
  await title();
  fireEvent.click(create());
  expect((await screen.findByRole('alert')).textContent).toMatch(/try again/i);
  expect(create().disabled).toBe(false);
});

test('Dismiss sends one dismiss and folds the row, and opens no campaign', async () => {
  const { fold } = open();
  await title();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(fold).toHaveBeenCalledTimes(1));
  expect(posts('/dismiss')).toEqual([{ path: '/inbox/_admiral/draft-map/dismiss', method: 'POST', body: {} }]);
  expect(nav.go).not.toHaveBeenCalled();
});

test('a refused Dismiss shows the reason and folds nothing', async () => {
  dismissAnswer = () => json({ error: 'disk is full' }, 500);
  const { fold } = open();
  await title();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect((await screen.findByRole('alert')).textContent).toBe('disk is full');
  expect(fold).not.toHaveBeenCalled();
});

test('a draft has Create and Dismiss only: no Remove, and the remove key does nothing', async () => {
  open();
  await title();
  expect(screen.queryByRole('button', { name: /remove/i })).toBeNull();
  peekKeys.current?.({ kind: 'remove' });
  await Promise.resolve();
  expect(screen.queryByRole('button', { name: /remove/i })).toBeNull();
  expect(sent.some((s) => s.path.endsWith('/remove'))).toBe(false);
});
