import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
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

const URL7 = 'https://github.com/acme/api/pull/7';
const behindPR: MooredPR = { url: URL7, state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, behind: true, merge_reasons: ['behind'] };
const ship = (session: string, pr: MooredPR): Ship => ({ session, name: 'Zulu', status: 'moored', no_resume: true, merge_method: 'squash', prs: [pr] }) as unknown as Ship;
const detail = (over: Partial<PRDetail> = {}): PRDetail =>
  ({ number: 7, url: URL7, title: 'Add the thing', body: '', state: 'OPEN', draft: false, head: 'thing', base: 'main', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', merge_state: 'BEHIND', failing_checks: [], reviews: [], requested: [], threads: [], comments: [], stack: null, ...over }) as PRDetail;

function serve(d: PRDetail, update: () => Response = () => new Response('{"updated":true}', { status: 202 })) {
  const sent: { method: string; path: string; body: string; token: string }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const headers = new Headers(init.headers);
      sent.push({ method: init.method ?? 'GET', path, body: String(init.body ?? ''), token: headers.get('X-Admiral-Merge-Token') ?? '' });
      if (path.includes('/pr?url=')) return new Response(JSON.stringify(d));
      if (path.endsWith('/update-branch')) return update();
      return new Response('{}', { status: 202 });
    }),
  );
  return sent;
}
const posts = (sent: ReturnType<typeof serve>) => sent.filter((s) => s.method === 'POST');

test('a behind PR on a moored ship says why it cannot merge, and offers Update branch naming its base', async () => {
  serve(detail());
  render(<PRTab ship={ship('upd-offer', behindPR)} />);
  expect(await screen.findByText(/Behind main/)).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Update branch' })).toBeTruthy();
});

test('Update branch posts the PR and the merge token once, however often it is tapped', async () => {
  const sent = serve(detail());
  render(<PRTab ship={ship('upd-post', behindPR)} />);
  const button = await screen.findByRole('button', { name: 'Update branch' });
  fireEvent.click(button);
  fireEvent.click(button);
  await waitFor(() => expect(posts(sent).some((s) => s.path === '/api/v1/ships/upd-post/update-branch')).toBe(true));
  const updates = posts(sent).filter((s) => s.path.endsWith('/update-branch'));
  expect(updates).toHaveLength(1);
  expect(JSON.parse(updates[0].body)).toEqual({ pr: URL7 });
  expect(updates[0].token).toBe(TOKEN);
});

test('a branch update gh refuses shows its message in the tab', async () => {
  serve(detail(), () => new Response(JSON.stringify({ error: 'Head branch was modified. Review and try the update again.' }), { status: 502 }));
  render(<PRTab ship={ship('upd-fail', behindPR)} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Update branch' }));
  expect(await screen.findByText('Head branch was modified. Review and try the update again.')).toBeTruthy();
});

test('a layer of a GitHub stack gets no Update branch, since the stack is updated as a whole', async () => {
  serve(detail({ stack: { number: 3, size: 2, base: 'main', url: 'https://github.com/acme/api/pull/3', entries: [{ position: 1, number: 7, url: URL7, state: 'OPEN', head: 'thing' }] } as unknown as PRDetail['stack'] }));
  render(<PRTab ship={ship('upd-stack', behindPR)} />);
  await screen.findByText('Add the thing');
  expect(screen.queryByRole('button', { name: 'Update branch' })).toBeNull();
});

test('a PR that is not behind gets no Update branch', async () => {
  serve(detail({ merge_state: 'CLEAN' }));
  render(<PRTab ship={ship('upd-clean', { ...behindPR, behind: false, merge_reasons: ['checks_pending'] })} />);
  await screen.findByText('Add the thing');
  expect(screen.queryByRole('button', { name: 'Update branch' })).toBeNull();
  expect(screen.getByText(/Checks still running/)).toBeTruthy();
});

test('the up-to-date rule is named only when the server says the base requires it', async () => {
  serve(detail());
  const { unmount } = render(<PRTab ship={ship('upd-strict', behindPR)} />);
  expect(await screen.findByText(/requires branches to be up to date/)).toBeTruthy();
  unmount();

  serve(detail());
  render(<PRTab ship={ship('upd-loose', { ...behindPR, behind: false })} />);
  await screen.findByText(/Behind main/);
  expect(screen.queryByText(/requires branches to be up to date/)).toBeNull();
});
