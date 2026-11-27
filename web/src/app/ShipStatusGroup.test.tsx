import { cleanup, fireEvent, render, screen, within } from '@testing-library/preact';
import { beforeEach, expect, test, vi } from 'vitest';
import type { MooredPR, Ship } from './api';
import { ShipStatusGroup } from './ShipStatusGroup';
import type { ShipGroup } from './shipList';

const ship = (name: string, extra: Partial<Ship> = {}): Ship => ({
  session: `s-${name}`,
  name,
  fleet: 'zulu',
  harness: 'claude',
  status: 'underway',
  order_id: '',
  title: '',
  tail: '',
  fleet_color: '',
  launched: '',
  crew_live: 0,
  dir: `/src/${name}`,
  repo: name,
  origin_repo: 'acme/widgets',
  exit_code: null,
  exit_signal: null,
  crew: [],
  ...extra,
});

beforeEach(() => {
  localStorage.clear();
});

const show = (ships: Ship[], byRepo: boolean, status: ShipGroup['status'] = 'underway') => {
  const group: ShipGroup = { status, ships };
  return render(<ShipStatusGroup group={group} bar="#369" now={Date.now()} byRepo={byRepo} />);
};

const dotfilesPR = { url: 'https://github.com/jbuffin/dotfiles/pull/3', state: 'OPEN' } as MooredPR;

test('keeps the status heading and count, with a sub-heading per repo and its count, most ships first', () => {
  show([ship('a', { origin_repo: 'b/small' }), ship('b'), ship('c'), ship('d', { origin_repo: 'b/small' }), ship('e')], true);
  const head = screen.getByRole('heading', { level: 2 });
  expect(head.textContent).toContain('Underway');
  expect(head.textContent).toContain('5');
  const sections = screen.getAllByRole('list');
  expect(sections).toHaveLength(2);
  expect(screen.getByText('acme/widgets').textContent).toBe('acme/widgets');
  const text = document.body.textContent ?? '';
  expect(text.indexOf('acme/widgets')).toBeLessThan(text.indexOf('b/small'));
  expect(within(sections[0]).getAllByRole('listitem')).toHaveLength(3);
  expect(within(sections[1]).getAllByRole('listitem')).toHaveLength(2);
  expect(screen.getByText('acme/widgets').closest('.repo-head')?.textContent).toContain('3');
  expect(screen.getByText('b/small').closest('.repo-head')?.textContent).toContain('2');
});

test('rows under a sub-heading show no repo label', () => {
  show([ship('a'), ship('b', { origin_repo: 'b/small' })], true);
  expect(document.querySelectorAll('.repo-head')).toHaveLength(2);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(document.querySelector('.ship-repo')).toBeNull();
});

test('a re-homed row says "from <folder>" in place of the repo label', () => {
  show([ship('zephyr', { origin_repo: undefined, prs: [dotfilesPR], repo: 'personal', dir: '/src/personal' }), ship('b')], true);
  const label = document.querySelector('.ship-repo') as HTMLElement;
  expect(label.textContent).toContain('from personal');
  expect(label.getAttribute('title')).toBe('/src/personal');
  expect(screen.getByText('jbuffin/dotfiles').closest('.repo-head')).not.toBeNull();
  expect(document.querySelectorAll('.ship-repo')).toHaveLength(1);
});

test('a re-homed row with no folder name shows nothing in the label place', () => {
  show([ship('zephyr', { origin_repo: undefined, prs: [dotfilesPR], repo: '', dir: '' }), ship('b')], true);
  expect(screen.getByText('jbuffin/dotfiles').closest('.repo-head')).not.toBeNull();
  expect(document.querySelector('.ship-repo')).toBeNull();
});

test('without sub-headings the rows keep their repo label and sit in one list', () => {
  show([ship('a'), ship('b')], false);
  expect(document.querySelector('.repo-head')).toBeNull();
  expect(screen.getAllByRole('list')).toHaveLength(1);
  expect(document.querySelectorAll('.ship-repo')).toHaveLength(2);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
});

const repoButton = (name: RegExp, within_: HTMLElement | Document = document.body) => within(within_ as HTMLElement).getByRole('button', { name });
const STORE = 'admiral.app.ships.collapsedRepos';
const small = (n: string) => ship(n, { origin_repo: 'b/small' });

test('each repo sub-heading is a button that collapses and expands its group', () => {
  show([ship('a'), ship('b'), small('c')], true);
  const btn = repoButton(/^acme\/widgets/);
  expect(btn.getAttribute('aria-expanded')).toBe('true');
  fireEvent.click(btn);
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('false');
  fireEvent.click(repoButton(/^acme\/widgets/));
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('true');
  expect(screen.getByRole('link', { name: 'a' })).toBeTruthy();
});

test('a collapsed group hides its rows but keeps its repo name and count, and leaves other repos alone', () => {
  show([ship('a'), ship('b'), small('c')], true);
  fireEvent.click(repoButton(/^acme\/widgets/));
  expect(screen.queryByRole('link', { name: 'a' })).toBeNull();
  expect(screen.queryByRole('link', { name: 'b' })).toBeNull();
  const btn = repoButton(/^acme\/widgets/);
  expect(btn.textContent).toContain('acme/widgets');
  expect(btn.textContent).toContain('2');
  expect(screen.getByRole('link', { name: 'c' })).toBeTruthy();
  expect(repoButton(/^b\/small/).getAttribute('aria-expanded')).toBe('true');
});

test('collapsing a repo under one status leaves the same repo under another status expanded', () => {
  render(
    <>
      <ShipStatusGroup group={{ status: 'underway', ships: [ship('a'), small('x')] }} bar="#369" now={Date.now()} byRepo />
      <ShipStatusGroup group={{ status: 'needs_you', ships: [ship('b'), small('y')] }} bar="#369" now={Date.now()} byRepo />
    </>,
  );
  const [first, second] = screen.getAllByRole('region');
  fireEvent.click(repoButton(/^acme\/widgets/, first));
  expect(within(first).queryByRole('link', { name: 'a' })).toBeNull();
  expect(within(second).getByRole('link', { name: 'b' })).toBeTruthy();
  expect(repoButton(/^acme\/widgets/, second).getAttribute('aria-expanded')).toBe('true');
});

test('the repo is matched case-insensitively across a status group', () => {
  show([ship('a'), ship('b', { origin_repo: 'ACME/Widgets' })], true);
  fireEvent.click(repoButton(/^acme\/widgets/i));
  expect(screen.queryByRole('link', { name: 'a' })).toBeNull();
  expect(screen.queryByRole('link', { name: 'b' })).toBeNull();
});

test('a one-repo layout has no collapse control', () => {
  show([ship('a'), ship('b')], false);
  expect(screen.queryByRole('button')).toBeNull();
});

test('a click writes the collapsed state to this device', () => {
  show([ship('a'), small('c')], true);
  expect(localStorage.getItem(STORE)).toBeNull();
  fireEvent.click(repoButton(/^acme\/widgets/));
  expect(JSON.parse(localStorage.getItem(STORE) ?? 'null')).toHaveLength(1);
});

test('a fresh page load finds the stored state and collapses only that status and repo pair', async () => {
  show([ship('a'), small('c')], true);
  fireEvent.click(repoButton(/^acme\/widgets/));
  const stored = localStorage.getItem(STORE) as string;
  cleanup();
  vi.resetModules();
  localStorage.setItem(STORE, stored);
  const fresh = await import('./ShipStatusGroup');
  render(
    <>
      <fresh.ShipStatusGroup group={{ status: 'underway', ships: [ship('a'), small('x')] }} bar="#369" now={Date.now()} byRepo />
      <fresh.ShipStatusGroup group={{ status: 'needs_you', ships: [ship('b'), small('y')] }} bar="#369" now={Date.now()} byRepo />
    </>,
  );
  const [first, second] = screen.getAllByRole('region');
  expect(repoButton(/^acme\/widgets/, first).getAttribute('aria-expanded')).toBe('false');
  expect(within(first).queryByRole('link', { name: 'a' })).toBeNull();
  expect(repoButton(/^b\/small/, first).getAttribute('aria-expanded')).toBe('true');
  expect(repoButton(/^acme\/widgets/, second).getAttribute('aria-expanded')).toBe('true');
  expect(within(second).getByRole('link', { name: 'b' })).toBeTruthy();
});

test('unreadable stored state starts every group expanded', async () => {
  vi.resetModules();
  localStorage.setItem(STORE, '{not json');
  const fresh = await import('./ShipStatusGroup');
  render(<fresh.ShipStatusGroup group={{ status: 'underway', ships: [ship('a'), small('c')] }} bar="#369" now={Date.now()} byRepo />);
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('true');
  expect(screen.getByRole('link', { name: 'a' })).toBeTruthy();
});
