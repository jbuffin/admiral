import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { MooredPR, PRDetail, Ship } from './api';
import { SPINNER_MIN_MS } from './busy';
import { PRTab } from './ShipPRs';

// The PR header's Refresh now button: its place, spinner, calls and what the tab shows after.

// Each test uses its own PR URLs: the tab remembers the last detail it read per URL.
const pullURL = (n: number) => `https://github.com/acme/api/pull/${n}`;
const pr = (url: string): MooredPR => ({ url, state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, behind: false, merge_reasons: [] }) as MooredPR;
const ship = (session: string, ...urls: string[]): Ship => ({ session, name: 'Zulu', status: 'moored', no_resume: true, merge_method: 'squash', prs: urls.map(pr) }) as unknown as Ship;
const detail = (url: string, over: Partial<PRDetail> = {}): PRDetail =>
  ({ number: Number(url.split('/').pop()), url, title: `Title of ${url.split('/').pop()}`, body: '', state: 'OPEN', draft: false, head: 'thing', base: 'main', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', merge_state: 'CLEAN', failing_checks: [], reviews: [], requested: [], threads: [], comments: [], stack: null, ...over }) as PRDetail;

type Answer = PRDetail | 'fail' | 'hold';
/** Each GET of a PR's detail takes the next answer for that URL (the last one repeats); 'hold' waits for release(). */
function serve(script: Record<string, Answer[]>) {
  const calls: { method: string; path: string }[] = [];
  const served: Record<string, number> = {};
  const held: Record<string, (d: PRDetail | 'fail') => void> = {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      calls.push({ method: init.method ?? 'GET', path });
      if (path.endsWith('/stack')) return new Response('{"error":"none"}', { status: 404 });
      if (!path.includes('/pr?url=')) return new Response('{}', { status: 202 });
      const url = decodeURIComponent(path.split('/pr?url=')[1]);
      const list = script[url];
      const n = (served[url] = (served[url] ?? 0) + 1);
      const answer = list[Math.min(n, list.length) - 1];
      const settle = (a: PRDetail | 'fail') => (a === 'fail' ? new Response('{"error":"gh failed"}', { status: 502 }) : new Response(JSON.stringify(a)));
      if (answer !== 'hold') return settle(answer);
      return new Promise<Response>((resolve) => {
        held[url] = (a) => resolve(settle(a));
      });
    }),
  );
  return { calls, release: (url: string, a: PRDetail | 'fail') => held[url](a) };
}
const gets = (calls: { method: string; path: string }[], url: string) => calls.filter((c) => c.method === 'GET' && c.path.includes(`/pr?url=${encodeURIComponent(url)}`));

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'], shouldAdvanceTime: true });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const advance = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
const settleSpinner = () => advance(SPINNER_MIN_MS + 1);
const tap = () => fireEvent.click(screen.getByRole('button', { name: 'Refresh now' }));

test('the PR header offers Refresh now while the first read is still pending', async () => {
  const U70 = pullURL(70);
  const { calls } = serve({ [U70]: ['hold'] });
  render(<PRTab ship={ship('rf-pending', U70)} />);
  await waitFor(() => expect(gets(calls, U70)).toHaveLength(1));
  expect(screen.getByRole('button', { name: 'Refresh now' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Open on GitHub' })).toBeNull();
});

test('the PR header offers Refresh now when the first read failed', async () => {
  const U72 = pullURL(72);
  serve({ [U72]: ['fail'] });
  render(<PRTab ship={ship('rf-failed', U72)} />);
  await screen.findByText('Trying again…');
  expect(screen.getByRole('button', { name: 'Refresh now' })).toBeTruthy();
});

test('the PR header offers Refresh now once the detail has loaded, idle', async () => {
  const U74 = pullURL(74);
  serve({ [U74]: [detail(U74)] });
  render(<PRTab ship={ship('rf-loaded', U74)} />);
  await screen.findByText('Title of 74');
  const button = screen.getByRole('button', { name: 'Refresh now' });
  expect(button.getAttribute('aria-busy')).not.toBe('true');
});

test('a tap re-reads the shown PR from GitHub and kicks the server poller', async () => {
  const U76 = pullURL(76);
  const { calls } = serve({ [U76]: [detail(U76)] });
  render(<PRTab ship={ship('rf-calls', U76)} />);
  await screen.findByText('Title of 76');
  const before = gets(calls, U76).length;
  tap();
  await waitFor(() => expect(gets(calls, U76)).toHaveLength(before + 1));
  expect(calls.filter((c) => c.method === 'POST' && c.path === '/api/v1/moored/refresh')).toHaveLength(1);
  expect(gets(calls, U76).at(-1)!.path).toBe(`/api/v1/ships/rf-calls/pr?url=${encodeURIComponent(U76)}`);
});

test('the button is Refreshing and aria-busy while the re-read is pending, and idle once it settles', async () => {
  const U78 = pullURL(78);
  const { release } = serve({ [U78]: [detail(U78), 'hold'] });
  render(<PRTab ship={ship('rf-busy', U78)} />);
  await screen.findByText('Title of 78');
  tap();
  const busy = await screen.findByRole('button', { name: 'Refreshing' });
  expect(busy.getAttribute('aria-busy')).toBe('true');
  expect(screen.queryByRole('button', { name: 'Refresh now' })).toBeNull();

  release(U78, detail(U78));
  await settleSpinner();
  expect((await screen.findByRole('button', { name: 'Refresh now' })).getAttribute('aria-busy')).not.toBe('true');
});

test('the button is idle again after a failed re-read', async () => {
  const U80 = pullURL(80);
  const { release } = serve({ [U80]: [detail(U80), 'hold'] });
  render(<PRTab ship={ship('rf-busy-fail', U80)} />);
  await screen.findByText('Title of 80');
  tap();
  await screen.findByRole('button', { name: 'Refreshing' });

  release(U80, 'fail');
  await settleSpinner();
  expect(await screen.findByRole('button', { name: 'Refresh now' })).toBeTruthy();
});

test('the re-read detail shows on the tab', async () => {
  const U82 = pullURL(82);
  serve({ [U82]: [detail(U82), detail(U82, { title: 'Renamed on GitHub' })] });
  render(<PRTab ship={ship('rf-shows', U82)} />);
  await screen.findByText('Title of 82');
  tap();
  await screen.findByText('Renamed on GitHub');
  expect(screen.queryByText('Title of 82')).toBeNull();
});

test('a failed re-read keeps the last good detail on screen', async () => {
  const U84 = pullURL(84);
  const { calls } = serve({ [U84]: [detail(U84), 'fail'] });
  render(<PRTab ship={ship('rf-keeps', U84)} />);
  await screen.findByText('Title of 84');
  tap();
  await waitFor(() => expect(gets(calls, U84)).toHaveLength(2));
  await settleSpinner();
  expect(await screen.findByRole('button', { name: 'Refresh now' })).toBeTruthy();
  expect(screen.getByText('Title of 84')).toBeTruthy();
});

test('busy belongs to the PR shown: another PR of the ship shows its own button idle mid-refresh', async () => {
  const U86 = pullURL(86);
  const U87 = pullURL(87);
  const { release } = serve({ [U86]: [detail(U86), 'hold'], [U87]: [detail(U87)] });
  render(<PRTab ship={ship('rf-two', U86, U87)} />);
  await screen.findByRole('heading', { name: 'Title of 86' });
  tap();
  await screen.findByRole('button', { name: 'Refreshing' });

  fireEvent.click(screen.getByRole('tab', { name: /#87/ }));
  await screen.findByRole('heading', { name: 'Title of 87' });
  expect(screen.getByRole('button', { name: 'Refresh now' }).getAttribute('aria-busy')).not.toBe('true');
  expect(screen.queryByRole('button', { name: 'Refreshing' })).toBeNull();

  fireEvent.click(screen.getByRole('tab', { name: /#86/ }));
  await screen.findByRole('button', { name: 'Refreshing' });
  release(U86, detail(U86));
  await settleSpinner();
  await screen.findByRole('button', { name: 'Refresh now' });
});

test('an older poll answer landing after a refresh does not put the stale detail back', async () => {
  const U88 = pullURL(88);
  const { release } = serve({ [U88]: [detail(U88), 'hold', detail(U88, { title: 'Renamed on GitHub' })] });
  render(<PRTab ship={ship('rf-order', U88)} />);
  await screen.findByText('Title of 88');
  await advance(30_000);
  tap();
  await screen.findByText('Renamed on GitHub');

  release(U88, detail(U88));
  await settleSpinner();
  expect(screen.getByText('Renamed on GitHub')).toBeTruthy();
  expect(screen.queryByText('Title of 88')).toBeNull();
});
