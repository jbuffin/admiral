import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { MooredPR, PRDetail, Ship } from './api';
import { PRTab } from './ShipPRs';

const TOKEN = 'fake-token-work-2';
beforeEach(() => {
  const meta = document.createElement('meta');
  meta.name = 'admiral-merge-token';
  meta.content = TOKEN;
  document.head.append(meta);
});
afterEach(() => {
  document.querySelector('meta[name="admiral-merge-token"]')?.remove();
  vi.unstubAllGlobals();
});

const url = (n: number) => `https://github.com/acme/api/pull/${n}`;
const pr = (n: number, over: Partial<MooredPR> = {}): MooredPR => ({ url: url(n), state: 'OPEN', review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, draft: true, ...over });
const ship = (session: string, prs: MooredPR[], over: Partial<Ship> = {}): Ship => ({ session, name: 'Zulu', status: 'moored', no_resume: true, merge_method: 'squash', prs, ...over }) as unknown as Ship;
const detail = (n: number, over: Partial<PRDetail> = {}): PRDetail =>
  ({ number: n, url: url(n), title: `Layer ${n}`, body: '', state: 'OPEN', draft: true, head: `h${n}`, base: 'main', review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE', merge_state: 'CLEAN', failing_checks: [], reviews: [], requested: [], threads: [], comments: [], stack: null, ...over }) as PRDetail;

interface Sent {
  method: string;
  path: string;
  body: string;
  token: string;
}
function serve(details: Record<string, PRDetail>, ready: () => Response = () => new Response('{"results":[]}')) {
  const sent: Sent[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      sent.push({ method: init.method ?? 'GET', path, body: String(init.body ?? ''), token: new Headers(init.headers).get('X-Admiral-Merge-Token') ?? '' });
      const hit = /\/pr\?url=(.+)$/.exec(path);
      if (hit) return new Response(JSON.stringify(details[decodeURIComponent(hit[1])]));
      if (path.endsWith('/stack')) return new Response(JSON.stringify({ shape: 'independent', prs: Object.keys(details) }));
      if (path.endsWith('/ready-for-review')) return ready();
      return new Response('{}', { status: 202 });
    }),
  );
  return sent;
}
const readyPosts = (sent: Sent[]) => sent.filter((s) => s.method === 'POST' && s.path.endsWith('/ready-for-review'));
const results = (rows: { url: string; outcome: string; error?: string }[]) => new Response(JSON.stringify({ results: rows }));

test('an open draft is labelled Draft and an open PR that is out of draft Ready for review, and a merged PR neither', async () => {
  serve({ [url(1)]: detail(1, { draft: true }) });
  const a = render(<PRTab ship={ship('chip-draft', [pr(1)])} />);
  await screen.findByText('Layer 1');
  expect(screen.getAllByText('Draft').length).toBeGreaterThan(0);
  expect(screen.queryByText('Ready for review')).toBeNull();
  a.unmount();

  serve({ [url(2)]: detail(2, { draft: false }) });
  const b = render(<PRTab ship={ship('chip-ready', [pr(2, { draft: false })])} />);
  await screen.findByText('Layer 2');
  expect(screen.getByText('Ready for review')).toBeTruthy();
  expect(screen.queryByText('Draft')).toBeNull();
  b.unmount();

  serve({ [url(3)]: detail(3, { draft: false, state: 'MERGED' }) });
  render(<PRTab ship={ship('chip-merged', [pr(3, { draft: false, state: 'MERGED' })])} />);
  await screen.findByText('Layer 3');
  expect(screen.queryByText('Ready for review')).toBeNull();
  expect(screen.queryByText('Draft')).toBeNull();
});

test('the sub-tabs tell a draft from a PR that is ready for review by their accessible names', async () => {
  serve({ [url(1)]: detail(1), [url(2)]: detail(2, { draft: false }) });
  render(<PRTab ship={ship('tabs-draft', [pr(1), pr(2, { draft: false })])} />);
  const tabs = within(await screen.findByRole('tablist'));
  expect(tabs.getByRole('tab', { name: /#1.*draft/i })).toBeTruthy();
  expect(tabs.getByRole('tab', { name: /#2.*ready for review/i })).toBeTruthy();
});

test('a draft on the ship own open PR offers Mark ready for review, and a PR already out of draft does not', async () => {
  serve({ [url(1)]: detail(1) });
  const a = render(<PRTab ship={ship('mark-offer', [pr(1)])} />);
  expect(await screen.findByRole('button', { name: 'Mark ready for review' })).toBeTruthy();
  a.unmount();

  serve({ [url(2)]: detail(2, { draft: false }) });
  render(<PRTab ship={ship('mark-none', [pr(2, { draft: false })])} />);
  await screen.findByText('Layer 2');
  expect(screen.queryByRole('button', { name: 'Mark ready for review' })).toBeNull();
});

test('a review ship and a merged draft get no Mark ready for review', async () => {
  serve({ [url(1)]: detail(1) });
  const own = render(<PRTab ship={ship('mark-own', [pr(1)])} />);
  expect(await screen.findByRole('button', { name: 'Mark ready for review' })).toBeTruthy();
  own.unmount();

  serve({ [url(1)]: detail(1) });
  const a = render(<PRTab ship={ship('mark-review', [pr(1, { reviewing: true })])} />);
  await screen.findByText('Layer 1');
  expect(screen.queryByRole('button', { name: 'Mark ready for review' })).toBeNull();
  a.unmount();

  serve({ [url(2)]: detail(2, { state: 'MERGED' }) });
  render(<PRTab ship={ship('mark-merged', [pr(2, { state: 'MERGED' })])} />);
  await screen.findByText('Layer 2');
  expect(screen.queryByRole('button', { name: 'Mark ready for review' })).toBeNull();
});

test('Mark ready for review posts nothing until the sheet is confirmed, then posts the PR with the merge token', async () => {
  const sent = serve({ [url(1)]: detail(1) }, () => results([{ url: url(1), outcome: 'marked_ready' }]));
  render(<PRTab ship={ship('mark-post', [pr(1)])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark ready for review' }));
  const sheet = await screen.findByRole('dialog');
  expect(readyPosts(sent)).toHaveLength(0);
  fireEvent.click(within(sheet).getByRole('button', { name: 'Mark ready for review' }));
  await waitFor(() => expect(readyPosts(sent)).toHaveLength(1));
  const [post] = readyPosts(sent);
  expect(post.path).toBe('/api/v1/ships/mark-post/ready-for-review');
  expect(JSON.parse(post.body)).toEqual({ prs: [url(1)] });
  expect(post.token).toBe(TOKEN);
});

test('cancelling the sheet posts nothing', async () => {
  const sent = serve({ [url(1)]: detail(1) });
  render(<PRTab ship={ship('mark-cancel', [pr(1)])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark ready for review' }));
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }));
  expect(readyPosts(sent)).toHaveLength(0);
});

test('Mark all ready for review needs two PRs and an own draft, and lists every URL bottom to top on confirm', async () => {
  const sent = serve({ [url(5)]: detail(5), [url(6)]: detail(6), [url(7)]: detail(7, { draft: false }) }, () => results([{ url: url(5), outcome: 'marked_ready' }, { url: url(6), outcome: 'marked_ready' }, { url: url(7), outcome: 'already_ready_for_review' }]));
  render(<PRTab ship={ship('mark-all', [pr(5), pr(6), pr(7, { draft: false })])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark all ready for review' }));
  const sheet = await screen.findByRole('dialog');
  expect(readyPosts(sent)).toHaveLength(0);
  fireEvent.click(within(sheet).getByRole('button', { name: 'Mark all ready for review' }));
  await waitFor(() => expect(readyPosts(sent)).toHaveLength(1));
  expect(JSON.parse(readyPosts(sent)[0].body)).toEqual({ prs: [url(5), url(6), url(7)] });
});

test('independent PRs with no stack line still offer Mark all ready for review, and one PR does not', async () => {
  serve({ [url(5)]: detail(5), [url(6)]: detail(6) });
  const a = render(<PRTab ship={ship('mark-all-indep', [pr(5), pr(6)])} />);
  expect(await screen.findByRole('button', { name: 'Mark all ready for review' })).toBeTruthy();
  a.unmount();

  serve({ [url(5)]: detail(5) });
  render(<PRTab ship={ship('mark-all-one', [pr(5)])} />);
  await screen.findByText('Layer 5');
  expect(screen.queryByRole('button', { name: 'Mark all ready for review' })).toBeNull();
});

test('a partial failure keeps the sheet open with a line per PR and the message gh gave', async () => {
  serve(
    { [url(5)]: detail(5), [url(6)]: detail(6) },
    () => results([{ url: url(5), outcome: 'marked_ready' }, { url: url(6), outcome: 'failed', error: 'GraphQL: Pull request is in a state that cannot be marked ready' }]),
  );
  render(<PRTab ship={ship('mark-partial', [pr(5), pr(6)])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark all ready for review' }));
  const sheet = await screen.findByRole('dialog');
  fireEvent.click(within(sheet).getByRole('button', { name: 'Mark all ready for review' }));
  expect(await within(await screen.findByRole('dialog')).findByText(/cannot be marked ready/)).toBeTruthy();
  const lines = within(screen.getByRole('dialog'));
  expect(lines.getByText(/#5/)).toBeTruthy();
  expect(lines.getByText(/#6/)).toBeTruthy();
});

test('a refused merge token tells the engineer to reload, in words about marking ready for review', async () => {
  serve({ [url(1)]: detail(1) }, () => new Response(JSON.stringify({ error: 'forbidden' }), { status: 403 }));
  render(<PRTab ship={ship('mark-stale', [pr(1)])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark ready for review' }));
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Mark ready for review' }));
  expect(await screen.findByText('Admiral restarted, reload to mark ready for review')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Reload' })).toBeTruthy();
});

test('the result sheet stays until it is closed, even when the re-read finds no draft left', async () => {
  const details: Record<string, PRDetail> = { [url(41)]: detail(41), [url(42)]: detail(42) };
  serve(details, () => {
    details[url(41)] = detail(41, { draft: false });
    details[url(42)] = detail(42, { draft: false });
    return results([{ url: url(41), outcome: 'marked_ready' }, { url: url(42), outcome: 'failed', error: 'gh did not finish in time, so whether the pull request is ready for review is unknown until the next refresh' }]);
  });
  render(<PRTab ship={ship('mark-sheet-stays', [pr(41), pr(42)])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark all ready for review' }));
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Mark all ready for review' }));
  expect(await within(await screen.findByRole('dialog')).findByText(/is unknown until the next refresh/)).toBeTruthy();
  // Give the re-read time to land: with no draft left the tab drops its own button, and the sheet must outlive it.
  await waitFor(() => expect(screen.queryByText('Draft')).toBeNull());
  expect(within(screen.getByRole('dialog')).getByText(/is unknown until the next refresh/)).toBeTruthy();
  // A button stays ignoring taps for the spinner's minimum after the request, so retry the tap until it lands.
  await waitFor(() => {
    const close = screen.queryByRole('dialog') && within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' });
    if (close) fireEvent.click(close);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

test('Mark all needs two of the ship own open PRs, so a merged PR beside one draft offers only Mark ready for review', async () => {
  serve({ [url(51)]: detail(51, { state: 'MERGED', draft: false }), [url(52)]: detail(52) });
  render(<PRTab ship={ship('mark-all-merged', [pr(51, { state: 'MERGED', draft: false }), pr(52)])} />);
  expect(await screen.findByRole('button', { name: 'Mark ready for review' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Mark all ready for review' })).toBeNull();
});

test('the confirm sheet does not call a PR whose detail did not load a draft', async () => {
  serve({ [url(61)]: detail(61) });
  render(<PRTab ship={ship('mark-unknown', [pr(61), pr(62)])} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Mark all ready for review' }));
  const lines = within(await screen.findByRole('dialog')).getAllByRole('listitem');
  expect(lines[0].textContent).toMatch(/#61.*draft/);
  expect(lines[1].textContent).toMatch(/#62.*state unknown/);
  expect(lines[1].textContent).not.toMatch(/draft/);
});
