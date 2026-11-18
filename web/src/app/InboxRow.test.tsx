import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Entry } from './inbox';
import { Row } from './InboxPanel';

afterEach(() => vi.unstubAllGlobals());

const notice = (id: string, over: Partial<Entry> = {}) =>
  ({ key: `item:_admiral/${id}`, type: 'item', session: '_admiral', id, folded: null, since: 0, item: { id, kind: 'notice', title: 'Heads up about the fleet', notice: { body: 'Plain words here' } }, ...over }) as unknown as Entry;

const row = (e: Entry, fold = vi.fn()) => render(<ul><Row e={e} now={0} phone={false} selected={false} fold={fold} /></ul>);

test('a notice opens in place to its body, and Dismiss sends one remove and folds the row', async () => {
  const sent: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string) => (sent.push(path), new Response('{}'))));
  const fold = vi.fn();
  row(notice('n-heads-up'), fold);
  expect(screen.queryByText('Plain words here')).toBeNull();
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  expect(screen.getByText('Plain words here')).toBeTruthy();
  expect(sent).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(fold).toHaveBeenCalledTimes(1));
  expect(sent).toEqual(['/api/v1/inbox/_admiral/n-heads-up/remove']);
});

test('a notice the server will not remove shows why and stays', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'disk is full' }), { status: 500 })));
  const fold = vi.fn();
  row(notice('n-refused'), fold);
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect((await screen.findByRole('alert')).textContent).toBe('disk is full');
  expect(fold).not.toHaveBeenCalled();
});

test('a folded row shows only its confirmation', () => {
  row(notice('n-folded', { folded: 'Removed from the inbox' }));
  expect(screen.getByRole('status').textContent).toBe('Removed from the inbox');
  expect(screen.queryByRole('button')).toBeNull();
});

const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';
const homeport = { key: 'alpha', name: 'Homeport', color: '#2a9d8f' };
const westwind = { key: 'zulu', name: 'Westwind', color: '#e76f51' };

test('a question row names its ship, that it asked, the question and its fleet, and links to its item in Peek', () => {
  const e = {
    key: 'item:s-v/q-port',
    type: 'item',
    session: 's-v',
    id: 'q-port',
    folded: null,
    since: 0,
    fleet: homeport,
    ship: { session: 's-v', name: 'Visitor' },
    item: { id: 'q-port', kind: 'question', name: 'Visitor', title: 'Which port should the visitor use?' },
  } as unknown as Entry;
  row(e);
  const li = screen.getByRole('listitem');
  for (const t of ['Visitor', 'asked', 'Which port should the visitor use?', 'Homeport']) expect(li.textContent).toContain(t);
  expect(screen.getByRole('link').getAttribute('href')).toBe('/app/inbox/s-v/item/q-port');
});

test('a stop row says what the ship wants and carries its own fleet, so two fleets read apart', () => {
  const stop = (fleet: typeof homeport) =>
    ({ key: `stop:${fleet.key}`, type: 'stop', session: `s-${fleet.key}`, folded: null, since: 0, fleet, ship: { session: `s-${fleet.key}`, name: `Ship ${fleet.key}`, status: 'needs_you', reason: 'permission:Bash' } }) as unknown as Entry;
  row(stop(westwind));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain('Ship zulu');
  expect(li.textContent).toMatch(/wants to run bash/i);
  expect(li.textContent).toContain('Westwind');
  expect(li.textContent).not.toContain('Homeport');
  expect(screen.getByRole('link').getAttribute('href')).toBe('/app/inbox/s-zulu');
});

test('a notice carries no fleet, and its body shows as text with nothing in it running', () => {
  row(notice('n-hostile', { item: { id: 'n-hostile', kind: 'notice', title: 'Heads up', notice: { body: HOSTILE } } } as unknown as Partial<Entry>));
  const li = screen.getByRole('listitem');
  expect(li.textContent).not.toMatch(/Homeport|Westwind/);
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  expect(li.textContent).toContain(HOSTILE);
  expect(li.querySelector('img, script')).toBeNull();
  expect((window as unknown as { __xss?: number }).__xss).toBeUndefined();
});

test('a notice body renders as markdown, and a script in it stays text', () => {
  const md = '## Heading\n\n- first point\n\n[the docs](https://example.com/docs) and `inline_code`\n\n```\nfenced line\n```\n\n<script>window.__xss=1</script>';
  const { container } = row(notice('n-md', { item: { id: 'n-md', kind: 'notice', title: 'Release notes', notice: { body: md } } } as unknown as Partial<Entry>));
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  expect(container.querySelector('h1, h2, h3, h4')?.textContent).toBe('Heading');
  expect(container.querySelector('.md li')?.textContent).toBe('first point');
  expect(container.querySelector('a[href="https://example.com/docs"]')?.textContent).toBe('the docs');
  expect(container.querySelector('pre > code')?.textContent).toContain('fenced line');
  expect(container.querySelector('script')).toBeNull();
  expect(container.textContent).toContain('<script>window.__xss=1</script>');
});

test('a notice title stays plain text', () => {
  const { container } = row(notice('n-title', { item: { id: 'n-title', kind: 'notice', title: 'Use **care** now', notice: { body: 'b' } } } as unknown as Partial<Entry>));
  expect(container.textContent).toContain('Use **care** now');
  expect(container.querySelector('strong')).toBeNull();
});

const titled = { session: 'calm-otter', name: 'Calm Otter', title: 'Fix the login redirect', status: 'moored', prs: [] };
const mergeRow = (ship: Record<string, unknown>, folded: string | null = null) =>
  ({ key: 'merge:calm-otter', type: 'merge', session: 'calm-otter', folded, since: 0, ship }) as unknown as Entry;

test('a ready-to-merge row shows the ship name and its title', () => {
  row(mergeRow(titled));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain('Calm Otter');
  expect(li.textContent).toContain('is ready to merge');
  expect(screen.getByText('Fix the login redirect')).toBeTruthy();
});

test('a ready-to-merge row of a ship with no title shows its name alone, with nothing empty beside it', () => {
  row(mergeRow({ ...titled, title: '' }));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain('Calm Otter');
  expect(Array.from(li.querySelectorAll('.ship-main *')).filter((n) => n.textContent?.trim() === '')).toEqual([]);
});

test('a folded ready-to-merge row keeps the ship name and its title beside the confirmation', () => {
  row(mergeRow(titled, 'Merged'));
  const li = screen.getByRole('listitem');
  expect(screen.getByRole('status').textContent).toBe('Merged');
  expect(li.textContent).toContain('Calm Otter');
  expect(li.textContent).toContain('Fix the login redirect');
});

test('a folded row of a ship with no title shows its name and confirmation alone', () => {
  row(mergeRow({ ...titled, title: '' }, 'Merged'));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain('Calm Otter');
  expect(Array.from(li.querySelectorAll('*')).filter((n) => n.textContent?.trim() === '')).toEqual([]);
});

const questionOf = (over: Record<string, unknown>) =>
  ({
    key: 'item:s-v/q-title',
    type: 'item',
    session: 's-v',
    id: 'q-title',
    folded: null,
    since: 0,
    ship: { session: 's-v', name: 'Visitor' },
    item: { id: 'q-title', kind: 'question', name: 'Visitor', title: 'Which port should the visitor use?', ...over },
  }) as unknown as Entry;

test("a question row shows its ship's title beside the name and keeps the question's own heading", () => {
  row(questionOf({ ship_title: 'Fix the login redirect' }));
  const li = screen.getByRole('listitem');
  for (const t of ['Visitor', 'asked', 'Fix the login redirect', 'Which port should the visitor use?']) expect(li.textContent).toContain(t);
});

test('a question row of a ship with no title shows the name and the heading alone', () => {
  row(questionOf({}));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain('Visitor');
  expect(li.textContent).toContain('Which port should the visitor use?');
  expect(Array.from(li.querySelectorAll('.ship-main *')).filter((n) => n.textContent?.trim() === '')).toEqual([]);
});

test('a ship title is shown as text, never as markup', () => {
  row(questionOf({ ship_title: HOSTILE }));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain(HOSTILE);
  expect(li.querySelector('img, script')).toBeNull();
});

const wakeOf = (over: Record<string, unknown>) =>
  ({
    key: 'item:_admiral/wake-1',
    type: 'item',
    session: '_admiral',
    id: 'wake-1',
    folded: null,
    since: 0,
    item: { id: 'wake-1', kind: 'wake_proposal', name: 'Admiral', title: "Wake Saltpaw for @stranger's review?", wake_proposal: { ship: 'Saltpaw', session: 'saltpaw', pr: 'https://github.com/acme/widgets/pull/9', login: 'stranger', ...over } },
  }) as unknown as Entry;

test("a wake proposal row shows the moored ship's title", () => {
  row(wakeOf({ ship_title: 'Fix the login redirect' }));
  const li = screen.getByRole('listitem');
  const text = li.textContent ?? '';
  const heading = text.indexOf("Wake Saltpaw for @stranger's review?");
  expect(heading).toBeGreaterThan(text.indexOf('Admiral'));
  expect(text.indexOf('Fix the login redirect')).toBeGreaterThan(heading);
});

test("a wake proposal row of a ship with no title shows the proposal's own words alone", () => {
  row(wakeOf({}));
  const li = screen.getByRole('listitem');
  expect(li.textContent).toContain('Saltpaw');
  expect(Array.from(li.querySelectorAll('.ship-main *')).filter((n) => n.textContent?.trim() === '')).toEqual([]);
});
