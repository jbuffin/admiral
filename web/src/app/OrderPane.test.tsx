import { render } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Order, Source } from './api';
import { OrderPane } from './OrderPane';
import type { OrderRow } from './ordersView';

beforeEach(() => vi.stubGlobal('fetch', vi.fn(async () => new Response('{}'))));
afterEach(() => vi.unstubAllGlobals());

const pane = (excerpt: string) => {
  const order = { id: 'acme/api#7', title: 'Fix it', url: 'https://github.com/acme/api/pull/7', excerpt } as Order;
  const row = { key: 'gh:acme/api#7', source: 'gh', order, reason: '', state: '', needsYou: false, inView: true, launchable: false } as OrderRow;
  const { container } = render(<OrderPane row={row} sources={[]} now={Date.now()} heading={false} onLaunch={() => undefined} />);
  return container.querySelector('.odetail-excerpt') as HTMLElement;
};

test('the excerpt renders inline markdown', () => {
  const el = pane('Adds **bold** and `code` here');
  expect(el.querySelector('strong')?.textContent).toBe('bold');
  expect(el.querySelector('code')?.textContent).toBe('code');
  expect(el.textContent).toBe('Adds bold and code here');
});

test('the excerpt drops HTML comments and keeps raw HTML as text', () => {
  const el = pane('<!-- marker -->See <b>this</b> <details>');
  expect(el.querySelector('b')).toBeNull();
  expect(el.querySelector('details')).toBeNull();
  expect(el.textContent).toContain('<b>this</b>');
  expect(el.textContent).not.toContain('marker');
});

const PR_URL = 'https://github.com/acme/api/pull/7';
const actor = (login: string) => ({ login, bot: false });
const detail = (over: Record<string, unknown> = {}) => ({
  number: 7, url: PR_URL, title: 'Fix it', body: '', state: 'OPEN', draft: false, head: 'h', base: 'main', review: 'CHANGES_REQUESTED', checks: 'FAILURE', mergeable: 'CONFLICTING', merge_state: 'DIRTY',
  failing_checks: [{ name: 'lint-zeta', url: 'https://github.com/acme/api/actions/runs/1' }],
  author: actor('author-x'),
  reviews: [
    { author: actor('alice-approver'), state: 'APPROVED' },
    { author: actor('bob-blocker'), state: 'CHANGES_REQUESTED' },
    { author: actor('carol-commenter'), state: 'COMMENTED' },
  ],
  requested: [actor('dave-waiting')], threads: [], comments: [], stack: null, ...over,
});
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });
const calls = () => (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
const ghSources = [{ key: 'gh', plugin: 'github', views: [] }] as unknown as Source[];

const prPane = (over: Partial<OrderRow> = {}, sources: Source[] = ghSources, order: Partial<Order> = {}) => {
  const o = { id: 'acme/api#7', title: 'Fix it', url: PR_URL, pr: { state: 'OPEN', review: '', checks: '', mergeable: '', conflicts: false }, ...order } as Order;
  const row = { key: 'gh:acme/api#7', source: 'gh', order: o, reason: '', state: '', needsYou: false, inView: true, launchable: true, ...over } as OrderRow;
  return render(<OrderPane row={row} sources={sources} now={Date.now()} heading={false} onLaunch={() => undefined} />);
};
const serve = (res: () => Response) => vi.stubGlobal('fetch', vi.fn(async () => res()));

test('a PR row shows each reviewer with a verdict and a requested reviewer as Waiting', async () => {
  serve(() => json(200, detail()));
  const { findByText, container } = prPane();
  await findByText('alice-approver');
  const li = (name: string) => [...container.querySelectorAll('li')].find((l) => l.textContent?.includes(name))?.textContent ?? '';
  expect(li('alice-approver')).toContain('Approved');
  expect(li('bob-blocker')).toContain('Changes requested');
  expect(li('carol-commenter')).toContain('Commented');
  expect(li('dave-waiting')).toContain('Waiting');
});

test('a PR row shows the review decision', async () => {
  serve(() => json(200, detail({ review: 'REVIEW_REQUIRED' })));
  const { findByText } = prPane();
  await findByText('Review required');
});

test('a PR row with no decision and no reviews says it has not been reviewed yet', async () => {
  serve(() => json(200, detail({ review: '', reviews: [], requested: [] })));
  const { findByText } = prPane();
  await findByText('Not reviewed yet');
});

test('a PR row shows the failing check, merge conflicts and draft state', async () => {
  serve(() => json(200, detail({ draft: true })));
  const { findByText } = prPane();
  await findByText('lint-zeta');
  await findByText('Merge conflicts');
  await findByText('Draft');
});

test('a ready PR shows neither Draft nor Merge conflicts', async () => {
  serve(() => json(200, detail({ draft: false, mergeable: 'MERGEABLE', failing_checks: [], checks: 'SUCCESS' })));
  const { findByText, queryByText } = prPane();
  await findByText('All checks passed');
  expect(queryByText('Draft')).toBeNull();
  expect(queryByText('Merge conflicts')).toBeNull();
});

test('the flyout reads the PR through its source', async () => {
  serve(() => json(200, detail()));
  const { findByText } = prPane();
  await findByText('alice-approver');
  expect(calls().filter((u) => u.includes('/sources/gh/pr?url='))).toEqual([expect.stringContaining(`/sources/gh/pr?url=${encodeURIComponent(PR_URL)}`)]);
});

test('the flyout says it is loading before the reply', async () => {
  let release: (r: Response) => void = () => undefined;
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((r) => (release = r))));
  const { findByText, queryByText } = prPane();
  await findByText('Loading review status…');
  release(json(200, detail()));
  await findByText('alice-approver');
  expect(queryByText('Loading review status…')).toBeNull();
});

test('a failed read shows its message and leaves the flyout working', async () => {
  serve(() => json(502, { error: 'github is sulking' }));
  const { findByText } = prPane();
  await findByText('github is sulking');
  await findByText('Launch ship');
});

test('an issue row makes no PR request', async () => {
  prPane({}, ghSources, { url: 'https://github.com/acme/api/issues/7', pr: undefined });
  await new Promise((r) => setTimeout(r, 20));
  expect(calls().filter((u) => u.includes('/pr?url='))).toEqual([]);
});

test('a stack row makes no PR request', async () => {
  prPane({ stackRow: true });
  await new Promise((r) => setTimeout(r, 20));
  expect(calls().filter((u) => u.includes('/pr?url='))).toEqual([]);
});

test('a PR row on a source that is not GitHub makes no PR request', async () => {
  prPane({}, [{ key: 'gh', plugin: 'other', views: [] }] as unknown as Source[]);
  await new Promise((r) => setTimeout(r, 20));
  expect(calls().filter((u) => u.includes('/pr?url='))).toEqual([]);
});
