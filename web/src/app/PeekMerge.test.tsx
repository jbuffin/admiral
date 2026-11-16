import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Ship } from './api';
import { ShipMerge } from './PeekMerge';

afterEach(() => vi.unstubAllGlobals());

const prs = (n: number) => Array.from({ length: n }, (_, i) => ({ url: `https://github.com/o/api/pull/${7 + i}` }));
const ship = (session: string, count: number) => ({ session, status: 'moored', stack_ready: true, merge_method: 'squash', prs: prs(count) }) as unknown as Ship;

function serve(shape?: string) {
  const sent: { method: string; path: string }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      sent.push({ method: init?.method ?? 'GET', path });
      if (path.endsWith('/stack')) return new Response(JSON.stringify({ shape, prs: [] }));
      return new Response('{}', { status: 202 });
    }),
  );
  return sent;
}

test('a ship of one PR merges here with its method and sends nothing until tapped', async () => {
  const sent = serve();
  const onMerged = vi.fn();
  render(<ShipMerge ship={ship('shipmerge-one', 1)} onMerged={onMerged} onError={vi.fn()} />);
  expect(screen.getByText('squash')).toBeTruthy();
  expect(sent).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
  await waitFor(() => expect(onMerged).toHaveBeenCalledTimes(1));
  expect(sent.filter((s) => s.path.endsWith('/merge'))).toEqual([{ method: 'POST', path: '/api/v1/ships/shipmerge-one/merge' }]);
});

test('a GitHub stack merges as a whole from here, once its shape is read', async () => {
  const sent = serve('stacked');
  const onMerged = vi.fn();
  render(<ShipMerge ship={ship('shipmerge-stack', 2)} onMerged={onMerged} onError={vi.fn()} />);
  expect(screen.queryByRole('button', { name: 'Merge' })).toBeNull();
  expect(await screen.findByText('squash · whole stack')).toBeTruthy();

  fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
  await waitFor(() => expect(onMerged).toHaveBeenCalledTimes(1));
  expect(sent.filter((s) => s.path.endsWith('/merge'))).toEqual([{ method: 'POST', path: '/api/v1/ships/shipmerge-stack/merge' }]);
});

test.each([
  ['independent', "Merge each PR from the ship's PR tab"],
  ['chained', "Stack these PRs from the ship's PR tab"],
])('%s PRs have no Merge here, only a pointer to the ship\'s PR tab that merges nothing', async (shape, label) => {
  const sent = serve(shape);
  render(<ShipMerge ship={ship(`shipmerge-${shape}`, 2)} onMerged={vi.fn()} onError={vi.fn()} />);
  const pointer = await screen.findByRole('link', { name: label });
  expect(pointer.getAttribute('href')).toContain(`/shipmerge-${shape}`);
  expect(pointer.getAttribute('href')).toContain('tab=pr');
  expect(screen.queryByRole('button', { name: /^Merge/ })).toBeNull();
  expect(sent.filter((s) => s.method === 'POST')).toEqual([]);
});
