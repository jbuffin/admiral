import { render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, Order, Source } from './api';
import { fleets } from './fleets';
import { OrdersPanel } from './OrdersScreen';
import { sources } from './shipsLink';

const zulu = { key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude' }], limits: {}, sentences: [] } as unknown as Fleet;
const order = (n: number, draft: boolean | undefined): Order => ({
  id: `acme/api#${n}`,
  title: `Order ${n}`,
  url: `https://github.com/acme/api/pull/${n}`,
  repo: 'acme/api',
  author: 'zulu',
  pr: { state: 'OPEN', review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, ...(draft === undefined ? {} : { draft }) },
});
const gh = (orders: Order[]): Source =>
  ({ key: 'gh', name: 'GitHub', plugin: 'github', fleet: 'zulu', icon_url: '', has_schema: false, state: 'running', error: null, warnings: [], capabilities: ['refresh'], views: [{ id: 'my-prs', title: 'My PRs', orders }] }) as unknown as Source;

beforeEach(() => {
  fleets.value = [zulu];
  history.replaceState(null, '', '/app/orders?fleet=zulu&tab=gh%2Fmy-prs');
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}')));
});
afterEach(() => {
  sources.value = [];
  vi.unstubAllGlobals();
});

test("the row's button names whether the order is a draft or ready for review", () => {
  sources.value = [gh([order(1, true), order(2, false)])];
  render(<OrdersPanel phone={false} title={false} />);
  expect(screen.getByRole('button', { name: 'Open Order 1, draft pull request' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Open Order 2, pull request ready for review' })).toBeTruthy();
  expect(screen.queryByRole('img')).toBeNull();
});

test('an order whose draft state is not known names neither state', () => {
  sources.value = [gh([order(3, undefined), order(4, true)])];
  render(<OrdersPanel phone={false} title={false} />);
  expect(screen.getByRole('button', { name: 'Open Order 3' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Open Order 4, draft pull request' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: /ready for review/ })).toBeNull();
});
