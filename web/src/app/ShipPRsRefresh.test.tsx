import { act, cleanup, fireEvent, render, screen } from '@testing-library/preact';
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
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
};
const settleSpinner = async () => {
  await flush();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(SPINNER_MIN_MS + 1);
  });
  await flush();
};
const refreshButton = () => screen.queryByRole('button', { name: /^Refresh/ });
const tap = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Refresh now' }));
  await flush();
};

test('the PR header offers Refresh now while the first read is still pending', async () => {
  const U70 = pullURL(70);
  serve({ [U70]: ['hold'] });
  render(<PRTab ship={ship('rf-pending', U70)} />);
  await flush();
  expect(screen.getByRole('button', { name: 'Refresh now' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Open on GitHub' })).toBeNull();
});

test('the PR header offers Refresh now when the first read failed', async () => {
  const U72 = pullURL(72);
  serve({ [U72]: ['fail'] });
  render(<PRTab ship={ship('rf-failed', U72)} />);
  await flush();
  expect(screen.getByText('Trying again…')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Refresh now' })).toBeTruthy();
});

test('the PR header offers Refresh now once the detail has loaded, idle', async () => {
  const U74 = pullURL(74);
  serve({ [U74]: [detail(U74)] });
  render(<PRTab ship={ship('rf-loaded', U74)} />);
  await flush();
  expect(screen.getByText('Title of 74')).toBeTruthy();
  const button = screen.getByRole('button', { name: 'Refresh now' });
  expect(button.getAttribute('aria-busy')).not.toBe('true');
});

test('a tap re-reads the shown PR from GitHub and kicks the server poller', async () => {
  const U76 = pullURL(76);
  const { calls } = serve({ [U76]: [detail(U76)] });
  render(<PRTab ship={ship('rf-calls', U76)} />);
  await flush();
  const before = gets(calls, U76).length;
  await tap();
  expect(gets(calls, U76)).toHaveLength(before + 1);
  expect(calls.filter((c) => c.method === 'POST' && c.path === '/api/v1/moored/refresh')).toHaveLength(1);
  expect(gets(calls, U76).at(-1)!.path).toBe(`/api/v1/ships/rf-calls/pr?url=${encodeURIComponent(U76)}`);
});

test('the button is Refreshing and aria-busy while the re-read is pending, and idle once it settles', async () => {
  const U78 = pullURL(78);
  const { release } = serve({ [U78]: [detail(U78), 'hold'] });
  render(<PRTab ship={ship('rf-busy', U78)} />);
  await flush();
  await tap();
  const busy = screen.getByRole('button', { name: 'Refreshing' });
  expect(busy.getAttribute('aria-busy')).toBe('true');
  expect(screen.queryByRole('button', { name: 'Refresh now' })).toBeNull();

  release(U78, detail(U78));
  await settleSpinner();
  expect(screen.getByRole('button', { name: 'Refresh now' }).getAttribute('aria-busy')).not.toBe('true');
});

test('the button is idle again after a failed re-read', async () => {
  const U80 = pullURL(80);
  const { release } = serve({ [U80]: [detail(U80), 'hold'] });
  render(<PRTab ship={ship('rf-busy-fail', U80)} />);
  await flush();
  await tap();
  expect(screen.getByRole('button', { name: 'Refreshing' })).toBeTruthy();

  release(U80, 'fail');
  await settleSpinner();
  expect(screen.getByRole('button', { name: 'Refresh now' })).toBeTruthy();
});

test('the re-read detail shows on the tab', async () => {
  const U82 = pullURL(82);
  serve({ [U82]: [detail(U82), detail(U82, { title: 'Renamed on GitHub' })] });
  render(<PRTab ship={ship('rf-shows', U82)} />);
  await flush();
  expect(screen.getByText('Title of 82')).toBeTruthy();
  await tap();
  await settleSpinner();
  expect(screen.getByText('Renamed on GitHub')).toBeTruthy();
  expect(screen.queryByText('Title of 82')).toBeNull();
});

test('a failed re-read keeps the last good detail on screen', async () => {
  const U84 = pullURL(84);
  serve({ [U84]: [detail(U84), 'fail'] });
  render(<PRTab ship={ship('rf-keeps', U84)} />);
  await flush();
  await tap();
  await settleSpinner();
  expect(screen.getByText('Title of 84')).toBeTruthy();
  expect(refreshButton()?.getAttribute('aria-label') ?? refreshButton()?.textContent).toBeTruthy();
});

test('busy belongs to the PR shown: another PR of the ship shows its own button idle mid-refresh', async () => {
  const U86 = pullURL(86);
  const U87 = pullURL(87);
  const { release } = serve({ [U86]: [detail(U86), 'hold'], [U87]: [detail(U87)] });
  render(<PRTab ship={ship('rf-two', U86, U87)} />);
  await flush();
  await tap();
  expect(screen.getByRole('button', { name: 'Refreshing' })).toBeTruthy();

  fireEvent.click(screen.getByRole('tab', { name: /#87/ }));
  await flush();
  expect(screen.getByRole('button', { name: 'Refresh now' }).getAttribute('aria-busy')).not.toBe('true');
  expect(screen.queryByRole('button', { name: 'Refreshing' })).toBeNull();

  fireEvent.click(screen.getByRole('tab', { name: /#86/ }));
  await flush();
  expect(screen.getByRole('button', { name: 'Refreshing' })).toBeTruthy();
  release(U86, detail(U86));
  await settleSpinner();
});
