import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { beforeEach, expect, test, vi } from 'vitest';
import type { MooredPR, Ship } from './api';
import { collapsed } from './collapsedRepoGroups';
import { repoOrder } from './repoGroupOrder';
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
  collapsed.value = new Set();
  repoOrder.value = [];
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

test('the repo is matched case-insensitively across renders', () => {
  const { unmount } = show([ship('a'), small('c')], true);
  fireEvent.click(repoButton(/^acme\/widgets/));
  unmount();
  show([ship('b', { origin_repo: 'ACME/Widgets' }), small('c')], true);
  expect(repoButton(/^ACME\/Widgets/).getAttribute('aria-expanded')).toBe('false');
  expect(screen.queryByRole('link', { name: 'b' })).toBeNull();
});

test('a button controls the list holding its rows, which is hidden when collapsed, even for near-identical repo names', () => {
  show([ship('a', { origin_repo: 'acme/foo.js' }), ship('b', { origin_repo: 'acme/foo_js' })], true);
  const listOf = (name: RegExp) => document.getElementById(repoButton(name).getAttribute('aria-controls') as string) as HTMLElement;
  const dot = listOf(/^acme\/foo\.js/);
  const under = listOf(/^acme\/foo_js/);
  expect(dot).not.toBe(under);
  expect(within(dot).getByRole('link', { name: 'a' })).toBeTruthy();
  expect(within(under).getByRole('link', { name: 'b' })).toBeTruthy();
  fireEvent.click(repoButton(/^acme\/foo\.js/));
  expect(listOf(/^acme\/foo\.js/).hidden).toBe(true);
  expect(listOf(/^acme\/foo_js/).hidden).toBe(false);
});

test('a one-repo layout has no collapse control', () => {
  show([ship('a'), ship('b')], false);
  expect(screen.queryByRole('button')).toBeNull();
});

test('a click is kept for a fresh page load, which collapses only that status and repo pair', async () => {
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

// Drag to reorder repo groups.
const ORDER_STORE = 'admiral.app.ships.repoOrder';
const widgets = (n: string) => ship(n);
const third = (n: string) => ship(n, { origin_repo: 'c/third' });
const other = (n: string) => ship(n, { origin_repo: 'd/other' });
// Default order: acme/widgets (3 ships), b/small (2), c/third (1).
const three = () => [widgets('w1'), widgets('w2'), widgets('w3'), small('s1'), small('s2'), third('t1')];

const heads = (scope: HTMLElement | Document = document.body) =>
  [...scope.querySelectorAll('.repo-head .repo-name')].map((n) => n.textContent);
const handle = (repo: string, scope: HTMLElement | Document = document.body) => within(scope as HTMLElement).getByRole('button', { name: `Move ${repo}` });
const groupLinks = (repo: string) =>
  within(handle(repo).closest('.repo-group') as HTMLElement)
    .queryAllByRole('link')
    .map((a) => a.textContent);

// happy-dom has no layout: stack the section's groups 100px tall, so midpoints sit at 50, 150, 250...
const stack = (section: Element) =>
  section.querySelectorAll('.repo-group').forEach((el, i) => {
    el.getBoundingClientRect = () => ({ top: i * 100, bottom: i * 100 + 100, height: 100, left: 0, right: 300, width: 300, x: 0, y: i * 100, toJSON: () => ({}) }) as DOMRect;
  });

// Presses the handle at its group's midpoint, moves to `toY` and lets go there.
function drag(repo: string, toY: number, end: 'pointerUp' | 'pointerCancel' | 'lostPointerCapture' = 'pointerUp', scope: HTMLElement | Document = document.body, pointerType = 'mouse') {
  const h = handle(repo, scope);
  const section = h.closest('section') as HTMLElement;
  stack(section);
  const from = (h.closest('.repo-group') as HTMLElement).getBoundingClientRect().top + 50;
  const init = { pointerId: 1, pointerType };
  fireEvent.pointerDown(h, { ...init, clientY: from, button: 0 });
  fireEvent.pointerMove(h, { ...init, clientY: (from + toY) / 2 });
  fireEvent.pointerMove(h, { ...init, clientY: toY });
  if (end === 'pointerUp') fireEvent.pointerUp(h, { ...init, clientY: toY });
  else if (end === 'pointerCancel') fireEvent.pointerCancel(h, init);
  else fireEvent.lostPointerCapture(h, init);
}

const W = 'acme/widgets';
const S = 'b/small';
const T = 'c/third';

test('dragging a group past another one moves the whole group there', () => {
  show(three(), true);
  expect(heads()).toEqual([W, S, T]);
  drag(W, 160);
  expect(heads()).toEqual([S, W, T]);
});

test('a group dropped below every other midpoint goes to the end, and one dropped above all goes first', () => {
  show(three(), true);
  drag(W, 290);
  expect(heads()).toEqual([S, T, W]);
  drag(W, 10);
  expect(heads()).toEqual([W, S, T]);
  drag(T, 10);
  expect(heads()).toEqual([T, W, S]);
});

test('a group dropped short of the next midpoint stays where it was', () => {
  show(three(), true);
  drag(W, 120);
  expect(heads()).toEqual([W, S, T]);
});

test('a moved group takes its header and its ships with it, and the other groups keep their own ships in order', () => {
  show(three(), true);
  drag(W, 290);
  expect(groupLinks(W)).toEqual(['w1', 'w2', 'w3']);
  expect(groupLinks(S)).toEqual(['s1', 's2']);
  expect(groupLinks(T)).toEqual(['t1']);
  const text = [...document.querySelectorAll('.repo-group')].map((g) => g.textContent);
  expect(text[2]).toContain('w1');
  expect(text[0]).toContain('s1');
});

test('ship rows have no move handle', () => {
  show(three(), true);
  for (const row of screen.getAllByRole('listitem')) expect(within(row).queryByRole('button', { name: /^Move/ })).toBeNull();
  expect(screen.getAllByRole('button', { name: /^Move/ })).toHaveLength(3);
});

test('the handle sits beside the collapse button, not inside it', () => {
  show(three(), true);
  const h = handle(W);
  expect(h.closest('.repo-head')).toBeNull();
  expect(h.closest('.repo-group')?.querySelector('.repo-head')).not.toBeNull();
  expect(h.closest('.repo-group')?.querySelector('ul.ship-list')).not.toBeNull();
});

test('the groups reorder only on the drop, not while the pointer is moving', () => {
  show(three(), true);
  const h = handle(W);
  stack(h.closest('section') as HTMLElement);
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 50, button: 0 });
  fireEvent.pointerMove(h, { pointerId: 1, clientY: 160 });
  expect(heads()).toEqual([W, S, T]);
  fireEvent.pointerUp(h, { pointerId: 1, clientY: 160 });
  expect(heads()).toEqual([S, W, T]);
});

test('a touch drag moves a group the same way', () => {
  show(three(), true);
  drag(W, 160, 'pointerUp', document.body, 'touch');
  expect(heads()).toEqual([S, W, T]);
});

test('a cancelled pointer leaves the order and the stored order unchanged', () => {
  show(three(), true);
  drag(W, 290, 'pointerCancel');
  expect(heads()).toEqual([W, S, T]);
  expect(localStorage.getItem(ORDER_STORE)).toBeNull();
});

test('losing pointer capture mid-drag leaves the order unchanged', () => {
  show(three(), true);
  drag(W, 290, 'lostPointerCapture');
  expect(heads()).toEqual([W, S, T]);
  expect(localStorage.getItem(ORDER_STORE)).toBeNull();
});

test('a press on the handle that does not move changes nothing', () => {
  show(three(), true);
  const h = handle(S);
  stack(h.closest('section') as HTMLElement);
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 150, button: 0 });
  fireEvent.pointerUp(h, { pointerId: 1, clientY: 150 });
  expect(heads()).toEqual([W, S, T]);
  expect(localStorage.getItem(ORDER_STORE)).toBeNull();
});

test('a move is written to storage as the status heading\'s whole order', () => {
  show(three(), true);
  drag(W, 290);
  expect(JSON.parse(localStorage.getItem(ORDER_STORE) as string)).toEqual([S, T, W]);
});

test('a stored order is shown after a fresh page load', async () => {
  show(three(), true);
  drag(W, 290);
  const stored = localStorage.getItem(ORDER_STORE) as string;
  cleanup();
  vi.resetModules();
  localStorage.setItem(ORDER_STORE, stored);
  const fresh = await import('./ShipStatusGroup');
  render(<fresh.ShipStatusGroup group={{ status: 'underway', ships: three() }} bar="#369" now={Date.now()} byRepo />);
  expect(heads()).toEqual([S, T, W]);
});

test('unreadable stored order shows the default order', async () => {
  vi.resetModules();
  localStorage.setItem(ORDER_STORE, '{not json');
  const fresh = await import('./ShipStatusGroup');
  render(<fresh.ShipStatusGroup group={{ status: 'underway', ships: three() }} bar="#369" now={Date.now()} byRepo />);
  expect(heads()).toEqual([W, S, T]);
  expect(screen.getAllByRole('button', { name: /^Move/ })).toHaveLength(3);
});

test('placed repos come first in their stored order, and repos never placed follow most ships first', () => {
  repoOrder.value = [T];
  show(three(), true);
  expect(heads()).toEqual([T, W, S]);
});

test('stored repos are matched case-insensitively', () => {
  repoOrder.value = ['B/Small'];
  show(three(), true);
  expect(heads()).toEqual([S, W, T]);
});

const twoStatuses = (first: Ship[], second: Ship[]) =>
  render(
    <>
      <ShipStatusGroup group={{ status: 'underway', ships: first }} bar="#369" now={Date.now()} byRepo />
      <ShipStatusGroup group={{ status: 'needs_you', ships: second }} bar="#369" now={Date.now()} byRepo />
    </>,
  );

test('a move under one status heading orders the same repos under another heading', () => {
  twoStatuses(three(), [widgets('a'), small('b'), small('c'), third('d')]);
  const [first, second] = screen.getAllByRole('region');
  expect(heads(second)).toEqual([S, W, T]);
  drag(T, 10, 'pointerUp', first);
  expect(heads(first)).toEqual([T, W, S]);
  expect(heads(second)).toEqual([T, W, S]);
});

test('a stored repo absent from the moved heading follows that heading\'s repos', () => {
  repoOrder.value = ['d/other'];
  twoStatuses(three(), [other('o1'), other('o2'), widgets('a'), small('b')]);
  const [first, second] = screen.getAllByRole('region');
  expect(heads(second)).toEqual(['d/other', W, S]);
  drag(S, 10, 'pointerUp', first);
  expect(JSON.parse(localStorage.getItem(ORDER_STORE) as string)).toEqual([S, W, T, 'd/other']);
  expect(heads(second)).toEqual([S, W, 'd/other']);
});

test('a repo never placed follows the placed ones under another heading', () => {
  twoStatuses(three(), [other('o1'), other('o2'), widgets('a'), small('b')]);
  const [first, second] = screen.getAllByRole('region');
  drag(S, 10, 'pointerUp', first);
  expect(heads(second)).toEqual([S, W, 'd/other']);
});

test('a click on the repo header still collapses and expands it', () => {
  show(three(), true);
  fireEvent.click(repoButton(/^acme\/widgets/));
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('false');
  fireEvent.click(repoButton(/^acme\/widgets/));
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('true');
});

test('a tap or a drag on the handle never changes whether a group is expanded', () => {
  show(three(), true);
  const h = handle(W);
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 50, button: 0 });
  fireEvent.pointerUp(h, { pointerId: 1, clientY: 50 });
  fireEvent.click(h);
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('true');
  drag(W, 290);
  fireEvent.click(handle(W));
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('true');
  expect(heads()).toEqual([S, T, W]);
});

test('a collapsed group drags like any other and stays collapsed', () => {
  show(three(), true);
  fireEvent.click(repoButton(/^acme\/widgets/));
  drag(W, 290);
  expect(heads()).toEqual([S, T, W]);
  expect(repoButton(/^acme\/widgets/).getAttribute('aria-expanded')).toBe('false');
  expect(screen.queryByRole('link', { name: 'w1' })).toBeNull();
  expect(screen.getByRole('link', { name: 's1' })).toBeTruthy();
});

test('with no stored order the default order holds and every group has a handle', () => {
  show(three(), true);
  expect(heads()).toEqual([W, S, T]);
  expect(screen.getAllByRole('button', { name: /^Move/ })).toHaveLength(3);
});

test('without sub-headings there is no move handle', () => {
  show(three(), false);
  expect(screen.queryByRole('button', { name: /^Move/ })).toBeNull();
});

test('a status heading with one repo group has no move handle, while one with two does', () => {
  twoStatuses([widgets('a'), widgets('b')], [widgets('c'), small('d')]);
  const [first, second] = screen.getAllByRole('region');
  expect(within(first).queryByRole('button', { name: /^Move/ })).toBeNull();
  expect(within(second).getAllByRole('button', { name: /^Move/ })).toHaveLength(2);
});

const press = (repo: string, key: string) => fireEvent.keyDown(handle(repo), { key });

test('ArrowDown on a focused handle moves the group one place down, and ArrowUp one up', () => {
  show(three(), true);
  handle(W).focus();
  press(W, 'ArrowDown');
  expect(heads()).toEqual([S, W, T]);
  press(W, 'ArrowDown');
  expect(heads()).toEqual([S, T, W]);
  press(W, 'ArrowUp');
  expect(heads()).toEqual([S, W, T]);
});

test('ArrowUp on the first group and ArrowDown on the last change nothing', () => {
  show(three(), true);
  press(W, 'ArrowUp');
  press(T, 'ArrowDown');
  expect(heads()).toEqual([W, S, T]);
  expect(localStorage.getItem(ORDER_STORE)).toBeNull();
});

test('a keyboard move is stored and leaves focus on the moved group\'s handle', async () => {
  show(three(), true);
  handle(W).focus();
  press(W, 'ArrowDown');
  expect(JSON.parse(localStorage.getItem(ORDER_STORE) as string)).toEqual([S, W, T]);
  await waitFor(() => expect(document.activeElement).toBe(handle(W)));
});

const outside = (ships: Ship[]) => (
  <>
    <input aria-label="outside" />
    <ShipStatusGroup group={{ status: 'underway', ships }} bar="#369" now={Date.now()} byRepo />
  </>
);

test('after a keyboard move the handle never takes focus back when its group becomes movable again', () => {
  const { rerender } = render(outside(three()));
  handle(W).focus();
  press(W, 'ArrowDown');
  const input = screen.getByLabelText('outside');
  input.focus();
  rerender(outside([widgets('w1')]));
  expect(screen.queryByRole('button', { name: /^Move/ })).toBeNull();
  rerender(outside(three()));
  expect(document.activeElement).toBe(input);
});

const dragTo = (repo: string, y: number) => {
  const h = handle(repo);
  stack(h.closest('section') as HTMLElement);
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 50, button: 0 });
  fireEvent.pointerMove(h, { pointerId: 1, clientY: y });
};
const marks = () => [...document.querySelectorAll('.lifted, .drop-before, .drop-after')];

test('a drag in progress lifts the dragged group and marks the drop place in front of the group it would land before', () => {
  show(three(), true);
  dragTo(W, 160);
  expect(handle(W).closest('.repo-group')?.classList.contains('lifted')).toBe(true);
  expect(handle(T).closest('.repo-group')?.classList.contains('drop-before')).toBe(true);
  expect(marks()).toHaveLength(2);
});

test('a drag below the last group marks the drop place behind it', () => {
  show(three(), true);
  dragTo(W, 290);
  expect(handle(T).closest('.repo-group')?.classList.contains('drop-after')).toBe(true);
  expect(marks()).toHaveLength(2);
});

test('a drag whose group goes away leaves no lifted look or drop marker, even when the group comes back', () => {
  const { rerender } = render(outside(three()));
  dragTo(W, 160);
  expect(marks()).toHaveLength(2);
  rerender(outside([small('s1'), small('s2'), third('t1')]));
  expect(marks()).toHaveLength(0);
  rerender(outside(three()));
  expect(marks()).toHaveLength(0);
});
