import { cleanup, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Ship } from './api';
import type { Entry } from './inbox';
import { Peek } from './Peek';
import { ships } from './shipsLink';

const start = ships.value;
beforeEach(() => vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ shape: 'single', prs: [] })))));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  ships.value = start;
});

const TITLE = 'Fix the login redirect';
const shipOf = (title: string) => ({ session: 'calm-otter', name: 'Calm Otter', title, status: 'moored', stack_ready: true, merge_method: 'squash', crew: [], prs: [{ url: 'https://github.com/o/api/pull/7' }] }) as unknown as Ship;
const merge = (ship: Ship) => ({ key: 'merge:calm-otter', type: 'merge', session: 'calm-otter', ship, folded: null, since: 0 }) as unknown as Entry;
const peek = (e: Entry) => render(<Peek entry={e} fold={vi.fn()} />);
const whoIsEmptyFree = () => expect(Array.from(document.querySelectorAll('.peek-who *')).filter((n) => n.textContent?.trim() === '')).toEqual([]);

test("Peek of a ready-to-merge ship shows the ship's name and its title", () => {
  const ship = shipOf(TITLE);
  ships.value = [ship];
  peek(merge(ship));
  expect(screen.getByRole('heading', { level: 2 }).textContent).toContain('Calm Otter');
  expect(screen.getByText(TITLE)).toBeTruthy();
});

test('Peek of a ready-to-merge ship with no title shows its name alone', () => {
  const ship = shipOf('');
  ships.value = [ship];
  peek(merge(ship));
  expect(screen.getByRole('heading', { level: 2 }).textContent).toContain('Calm Otter');
  whoIsEmptyFree();
});

const question = (over: Record<string, unknown>) =>
  ({ key: 'item:s-v/q-1', type: 'item', session: 's-v', id: 'q-1', folded: null, since: 0, item: { id: 'q-1', kind: 'question', name: 'Visitor', title: 'Which port?', question: { questions: [] }, ...over } }) as unknown as Entry;

test("Peek of a question shows the ship's title beside its name and keeps the question's heading", () => {
  peek(question({ ship_title: TITLE }));
  expect(screen.getByRole('heading', { level: 2 }).textContent).toContain('Visitor');
  expect(screen.getByText(TITLE)).toBeTruthy();
  expect(screen.getByText('Which port?')).toBeTruthy();
});

test('Peek of a question of a ship with no title shows the name and the heading alone', () => {
  peek(question({}));
  expect(screen.getByText('Which port?')).toBeTruthy();
  whoIsEmptyFree();
});

const wake = (over: Record<string, unknown>) =>
  ({ key: 'item:_admiral/wake-1', type: 'item', session: '_admiral', id: 'wake-1', folded: null, since: 0, item: { id: 'wake-1', kind: 'wake_proposal', name: 'Admiral', title: "Wake Saltpaw for @stranger's review?", wake_proposal: { ship: 'Saltpaw', session: 'saltpaw', pr: 'https://github.com/acme/widgets/pull/9', login: 'stranger', events: [], ...over } } }) as unknown as Entry;

test("Peek of a wake proposal shows the moored ship's title", () => {
  peek(wake({ ship_title: TITLE }));
  expect(screen.getByText(TITLE)).toBeTruthy();
});

test('Peek of a wake proposal of a ship with no title shows the proposal alone', () => {
  peek(wake({}));
  expect(screen.getByText("Wake Saltpaw for @stranger's review?")).toBeTruthy();
  whoIsEmptyFree();
});
