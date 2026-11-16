import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { MooredPR, Ship } from './api';
import { ReReviewBody, ReReviewSheet } from './ReReview';

afterEach(() => vi.unstubAllGlobals());

const url = 'https://github.com/acme/api/pull/7';
const responded: MooredPR = { url, state: 'OPEN', review: 'APPROVED', checks: '', mergeable: '', conflicts: false, reviewing: true, responded: true, author: 'bob' };
const ship = (session: string, over: Partial<Ship> = {}) => ({ session, name: 'Zulu', status: 'moored', prs: [responded], ...over }) as Ship;

function stub(status = 201, body = '{}') {
  const calls: { path: string; body: string }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push({ path, body: String(init.body ?? '') });
    return new Response(body, { status });
  }));
  return calls;
}

test('the sheet says who responded, shows /commodore with the PR\'s URL, and the confirm posts that URL', async () => {
  const calls = stub();
  const onClose = vi.fn();
  render(<ReReviewSheet ship={ship('rr-sheet')} onClose={onClose} />);

  expect(screen.getByRole('dialog', { name: 'Re-review' })).toBeTruthy();
  expect(screen.getByText('@bob has responded since your review. Commodore reviews #7 again.')).toBeTruthy();
  expect(screen.getByText('Wakes Zulu under the same name with:')).toBeTruthy();
  expect(screen.getByText(`/commodore ${url}`).tagName).toBe('CODE');

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(calls).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Re-review' }));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(2));
  expect(calls).toEqual([{ path: '/api/v1/ships/rr-sheet/rereview', body: JSON.stringify({ url }) }]);
});

test('a reviewed stack re-reviews the whole stack by its URL', () => {
  const stack = 'https://github.com/acme/api/stack/4';
  const second = { ...responded, url: 'https://github.com/acme/api/pull/8', responded: false };
  render(<ReReviewSheet ship={ship('rr-stack', { url: stack, prs: [responded, second] })} onClose={() => undefined} />);

  expect(screen.getByRole('dialog', { name: 'Re-review stack' })).toBeTruthy();
  expect(screen.getByText('@bob has responded since your review. Commodore reviews the whole stack again.')).toBeTruthy();
  expect(screen.getByText(`/commodore ${stack}`)).toBeTruthy();
});

test('a refusal shows its message and keeps the sheet open', async () => {
  stub(409, JSON.stringify({ error: 'the ship is live' }));
  const onClose = vi.fn();
  render(<ReReviewSheet ship={ship('rr-refused')} onClose={onClose} />);

  fireEvent.click(screen.getByRole('button', { name: 'Re-review' }));
  expect((await screen.findByRole('alert')).textContent).toBe('the ship is live');
  expect(onClose).not.toHaveBeenCalled();
});

test('Peek\'s body is the confirmation: one tap posts the URL and folds the entry as Re-reviewing', async () => {
  const calls = stub();
  const fold = vi.fn();
  render(<ReReviewBody ship={ship('rr-peek')} fold={fold} />);

  expect(screen.getByText(`/commodore ${url}`)).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Re-review' }));

  await waitFor(() => expect(fold).toHaveBeenCalledWith('Re-reviewing'));
  expect(calls).toEqual([{ path: '/api/v1/ships/rr-peek/rereview', body: JSON.stringify({ url }) }]);
});
