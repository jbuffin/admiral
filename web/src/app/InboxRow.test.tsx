import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Entry } from './inbox';
import { Row } from './InboxPanel';

afterEach(() => vi.unstubAllGlobals());

const notice = (id: string, over: Partial<Entry> = {}) =>
  ({ key: `item:_admiral/${id}`, type: 'item', session: '_admiral', id, folded: null, since: 0, item: { id, kind: 'notice', title: 'Heads up about the fleet', notice: { body: 'Plain words here' } }, ...over }) as unknown as Entry;

const row = (e: Entry, fold = vi.fn()) => render(<ul><Row e={e} now={0} phone={false} selected={false} fold={fold} /></ul>);

test('a notice opens in place to its body, and Dismiss sends one remove and folds the row', async () => {
  const sent: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string) => (sent.push(path), new Response('{}'))));
  const fold = vi.fn();
  row(notice('n-heads-up'), fold);
  expect(screen.queryByText('Plain words here')).toBeNull();
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  expect(screen.getByText('Plain words here')).toBeTruthy();
  expect(sent).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(fold).toHaveBeenCalledTimes(1));
  expect(sent).toEqual(['/api/v1/inbox/_admiral/n-heads-up/remove']);
});

test('a notice the server will not remove shows why and stays', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'disk is full' }), { status: 500 })));
  const fold = vi.fn();
  row(notice('n-refused'), fold);
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect((await screen.findByRole('alert')).textContent).toBe('disk is full');
  expect(fold).not.toHaveBeenCalled();
});

test('a folded row shows only its confirmation', () => {
  row(notice('n-folded', { folded: 'Removed from the inbox' }));
  expect(screen.getByRole('status').textContent).toBe('Removed from the inbox');
  expect(screen.queryByRole('button')).toBeNull();
});
