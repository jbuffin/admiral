import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Ship } from './api';
import { MergeControl } from './PeekMerge';

afterEach(() => vi.unstubAllGlobals());

const ship = (session: string, method: Ship['merge_method'] = 'squash') => ({ session, name: 'Zulu', merge_method: method }) as Ship;

function stub(merge: () => Response) {
  const calls: { path: string; method: string; body: string }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push({ path, method: init.method ?? 'GET', body: String(init.body ?? '') });
    return path.endsWith('/merge') ? merge() : new Response('{}', { status: 202 });
  }));
  return calls;
}

test('Merge names the repository\'s method, and one tap posts one merge, then asks for a poll and reports it merged', async () => {
  const calls = stub(() => new Response('{}', { status: 202 }));
  const onMerged = vi.fn();
  render(<MergeControl ship={ship('merge-ok')} onMerged={onMerged} onError={() => undefined} />);
  expect(screen.getByText('squash')).toBeTruthy();

  const merge = screen.getByRole('button', { name: 'Merge' });
  fireEvent.click(merge);
  fireEvent.click(merge);
  await waitFor(() => expect(onMerged).toHaveBeenCalledTimes(1));

  expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/v1/ships/merge-ok/merge', 'POST /api/v1/moored/refresh']);
});

test('a merge of one independent PR sends that PR alone, and a stack\'s hint says the whole stack merges', async () => {
  const calls = stub(() => new Response('{}', { status: 202 }));
  const { unmount } = render(<MergeControl ship={ship('merge-pr')} pr="https://github.com/acme/api/pull/8" onMerged={() => undefined} onError={() => undefined} />);
  fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
  await waitFor(() => expect(calls[0]).toBeTruthy());
  expect(JSON.parse(calls[0].body)).toEqual({ pr: 'https://github.com/acme/api/pull/8' });
  unmount();

  render(<MergeControl ship={ship('merge-stack')} stack onMerged={() => undefined} onError={() => undefined} />);
  expect(screen.getByText('squash · whole stack')).toBeTruthy();
});

test('a merge gh refuses reports its message and merges nothing more', async () => {
  const calls = stub(() => new Response(JSON.stringify({ error: 'GraphQL: Pull request is not mergeable (mergePullRequest)' }), { status: 502 }));
  const onError = vi.fn();
  const onMerged = vi.fn();
  render(<MergeControl ship={ship('merge-fails')} onMerged={onMerged} onError={onError} />);

  fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
  await waitFor(() => expect(onError).toHaveBeenLastCalledWith('GraphQL: Pull request is not mergeable (mergePullRequest)'));

  expect(onMerged).not.toHaveBeenCalled();
  expect(calls.map((c) => c.path)).toEqual(['/api/v1/ships/merge-fails/merge']);
  expect(screen.queryByRole('button', { name: 'Reload' })).toBeNull();
});

test('a merge the server answers 403 says Admiral restarted and offers Reload', async () => {
  stub(() => new Response(JSON.stringify({ error: 'forbidden' }), { status: 403 }));
  const onError = vi.fn();
  render(<MergeControl ship={ship('merge-stale')} onMerged={() => undefined} onError={onError} />);

  fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
  await waitFor(() => expect(onError).toHaveBeenLastCalledWith('Admiral restarted, reload to merge'));

  expect(screen.getByRole('button', { name: 'Reload' })).toBeTruthy();
});
