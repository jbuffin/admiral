// A module page's notices: what is drawn, in what order, and the starting skeleton under them.
import { render, screen } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Source } from './api';
import { ModulePage } from './ModulePage';
import { loc } from './router';
import { sources } from './shipsLink';

const start = { value: loc.value, url: location.href };

afterEach(() => {
  vi.unstubAllGlobals();
  sources.value = [];
  loc.value = start.value;
  history.replaceState({}, '', start.url);
});

const source = (state = 'ready'): Source =>
  ({ key: 'deploys', name: 'Deploys', fleet: 'zulu', views: [], capabilities: ['page'], state: 'ok', page: { state, waiting: 0, version: 1 } }) as unknown as Source;

const groups = [{ id: 'g1', title: 'Needs review', rows: [{ id: 'r1', title: 'Row one', subtitle: '', chips: [], reason: '' }] }];

function show(body: unknown, state = 'ready') {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body))));
  sources.value = [source(state)];
  return render(<ModulePage sourceKey="deploys" title />);
}

const before = (a: Node, b: Node) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
const toneOf = (text: string) => screen.getByText(text).closest('[data-tone]')?.getAttribute('data-tone');

test('draws every notice above the first group, in the order served', async () => {
  show({ state: 'ready', waiting: 0, groups, notices: [{ id: 'a', text: 'First notice', tone: 'info' }, { id: 'b', text: 'Second notice', tone: 'warn' }, { id: 'c', text: 'Third notice', tone: 'error' }] });
  const first = await screen.findByText('First notice');
  const second = screen.getByText('Second notice');
  const third = screen.getByText('Third notice');
  const group = screen.getByText('Needs review');
  expect(before(first, second)).toBe(true);
  expect(before(second, third)).toBe(true);
  expect(before(third, group)).toBe(true);
  expect(screen.getByText('Row one')).toBeTruthy();
});

test('styles a notice by its tone, and an unknown tone draws as info', async () => {
  show({ state: 'ready', waiting: 0, groups, notices: [{ id: 'a', text: 'An info', tone: 'info' }, { id: 'b', text: 'A warning', tone: 'warn' }, { id: 'c', text: 'An error', tone: 'error' }, { id: 'd', text: 'Odd tone', tone: 'red' }, { id: 'e', text: 'No tone' }] });
  await screen.findByText('An info');
  expect(toneOf('An info')).toBe('info');
  expect(toneOf('A warning')).toBe('warn');
  expect(toneOf('An error')).toBe('error');
  expect(toneOf('Odd tone')).toBe('info');
  expect(toneOf('No tone')).toBe('info');
});

test('draws notice text as plain text', async () => {
  const { container } = show({ state: 'ready', waiting: 0, groups: [], notices: [{ id: 'a', text: '<img src=x onerror=alert(1)>', tone: 'info' }] });
  expect(await screen.findByText('<img src=x onerror=alert(1)>')).toBeTruthy();
  expect(container.querySelector('img')).toBeNull();
});

test('opens a notice link in a new tab without handing the page to the opener', async () => {
  show({ state: 'ready', waiting: 0, groups, notices: [{ id: 'a', text: 'An update is waiting', tone: 'warn', link: { label: 'Release notes', url: 'https://example.com/notes' } }, { id: 'b', text: 'No link here' }] });
  const link = (await screen.findByRole('link', { name: 'Release notes' })) as HTMLAnchorElement;
  expect(link.getAttribute('href')).toBe('https://example.com/notes');
  expect(link.getAttribute('target')).toBe('_blank');
  expect(link.getAttribute('rel')).toContain('noopener');
  expect(screen.getAllByRole('link')).toHaveLength(1);
  expect(screen.getByText('An update is waiting').closest('[data-tone]')?.contains(link)).toBe(true);
});

test('makes no link of a notice url that is not http or https', async () => {
  show({ state: 'ready', waiting: 0, groups, notices: [{ id: 'a', text: 'A notice', tone: 'info', link: { label: 'Go', url: 'javascript:alert(1)' } }] });
  await screen.findByText('A notice');
  expect(screen.queryByRole('link')).toBeNull();
});

test('gives the engineer no way to dismiss a notice', async () => {
  show({ state: 'ready', waiting: 0, groups, notices: [{ id: 'a', text: 'Stays put', tone: 'info' }] });
  const notice = (await screen.findByText('Stays put')).closest('[data-tone]') as HTMLElement;
  expect(notice.querySelector('button, [role="button"]')).toBeNull();
});

test('draws nothing extra for a page with no notices', async () => {
  const { container } = show({ state: 'ready', waiting: 0, groups, notices: [] });
  await screen.findByText('Row one');
  expect(container.querySelector('.module-page [data-tone]')).toBeNull();
});

test('draws a page from a server that sends no notices field', async () => {
  const { container } = show({ state: 'ready', waiting: 0, groups });
  expect(await screen.findByText('Row one')).toBeTruthy();
  expect(container.querySelector('.module-page [data-tone]')).toBeNull();
});

test('draws the notices above the starting skeleton', async () => {
  show({ state: 'starting', waiting: 0, groups: [], notices: [{ id: 'pick', text: 'Pick your project', tone: 'warn' }] }, 'starting');
  const notice = await screen.findByText('Pick your project');
  const skeleton = await screen.findByRole('status', { name: /loading/i });
  expect(before(notice, skeleton)).toBe(true);
  expect(screen.queryByText('Needs review')).toBeNull();
});

test('draws the groups and no skeleton once a page without loading arrives', async () => {
  show({ state: 'ready', waiting: 1, groups, notices: [{ id: 'pick', text: 'Pick your project', tone: 'warn' }] });
  await screen.findByText('Row one');
  expect(screen.queryByRole('status', { name: /loading/i })).toBeNull();
  expect(before(screen.getByText('Pick your project'), screen.getByText('Needs review'))).toBe(true);
});

test('keeps the notices above the sign-in card and above the error', async () => {
  const notices = [{ id: 'keep', text: 'Kept notice', tone: 'info' }];
  const auth = show({ state: 'auth_required', waiting: 0, groups, auth: { message: 'Sign in to Deploys' }, notices }, 'auth_required');
  expect(await auth.findByText('Kept notice')).toBeTruthy();
  expect(before(screen.getByText('Kept notice'), screen.getByText('Needs review'))).toBe(true);
  expect(screen.getByText('Sign in to Deploys')).toBeTruthy();
  auth.unmount();

  show({ state: 'error', waiting: 0, groups, error: 'plugin crashed', notices }, 'error');
  expect(await screen.findByText('Kept notice')).toBeTruthy();
  expect(screen.getByText('plugin crashed')).toBeTruthy();
  expect(before(screen.getByText('Kept notice'), screen.getByText('Needs review'))).toBe(true);
});
