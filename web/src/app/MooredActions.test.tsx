import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Ship } from './api';
import { MooredActions } from './MooredActions';

afterEach(() => vi.unstubAllGlobals());

const ship = (session: string, prs: Ship['prs'] = [{ url: 'https://github.com/acme/api/pull/7', state: 'OPEN', review: '', checks: '', mergeable: '', conflicts: false }]) => ({ session, name: 'Zulu', prs }) as Ship;

function stub(status = 200, body = '{}') {
  const calls: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push(`${init.method} ${path}`);
    return new Response(body, { status });
  }));
  return calls;
}

const names = () => screen.getAllByRole('button').map((b) => b.textContent);

test('a Ships row offers Wake, Dismiss and Don\'t follow reviews, and the PR tab only Don\'t follow reviews', () => {
  const row = render(<MooredActions ship={ship('actions-row')} onError={() => undefined} />);
  expect(names()).toEqual(['Wake', 'Dismiss', "Don't follow reviews"]);
  row.unmount();
  render(<MooredActions ship={ship('actions-tab')} onError={() => undefined} tab />);
  expect(names()).toEqual(["Don't follow reviews"]);
});

test('a ship that reviews a PR offers no Don\'t follow reviews', () => {
  const reviewed = ship('actions-reviewing', [{ url: 'https://github.com/acme/api/pull/7', state: 'OPEN', review: '', checks: '', mergeable: '', conflicts: false, reviewing: true }]);
  render(<MooredActions ship={reviewed} onError={() => undefined} />);
  expect(names()).toEqual(['Wake', 'Dismiss']);
});

test('Don\'t follow reviews posts the unfollow, and every button stays off after it succeeds', async () => {
  const calls = stub();
  render(<MooredActions ship={ship('actions-unfollow')} onError={() => undefined} tab />);

  fireEvent.click(screen.getByRole('button', { name: "Don't follow reviews" }));
  await waitFor(() => expect(calls).toEqual(['POST /api/v1/ships/actions-unfollow/unfollow']));

  await waitFor(() => expect((screen.getByRole('button', { name: "Don't follow reviews" }) as HTMLButtonElement).disabled).toBe(true));
});

test('Dismiss and Wake post their own routes', async () => {
  const calls = stub();
  const first = render(<MooredActions ship={ship('actions-dismiss')} onError={() => undefined} />);
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(calls).toEqual(['POST /api/v1/ships/actions-dismiss/dismiss']));
  first.unmount();

  const wakes = stub();
  render(<MooredActions ship={ship('actions-wake')} onError={() => undefined} />);
  fireEvent.click(screen.getByRole('button', { name: 'Wake' }));
  await waitFor(() => expect(wakes).toEqual(['POST /api/v1/ships/actions-wake/wake']));
});

test('a refused request reports its message and leaves the buttons on', async () => {
  stub(500, JSON.stringify({ error: 'could not remove the moored record' }));
  const onError = vi.fn();
  render(<MooredActions ship={ship('actions-refused')} onError={onError} />);

  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(onError).toHaveBeenLastCalledWith('could not remove the moored record'));

  await waitFor(() => expect((screen.getByRole('button', { name: 'Dismiss' }) as HTMLButtonElement).disabled).toBe(false));
});
