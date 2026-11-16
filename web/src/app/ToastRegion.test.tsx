import { act, fireEvent, render, screen } from '@testing-library/preact';
import { beforeEach, expect, test, vi } from 'vitest';
import type { Ship } from './api';

const h = vi.hoisted(() => ({ listener: null as null | ((m: { type: string; [k: string]: unknown }) => void), go: vi.fn(), chime: vi.fn() }));
vi.mock('./router', async (orig) => ({ ...(await orig<typeof import('./router')>()), go: h.go }));
vi.mock('../chime', async (orig) => ({ ...(await orig<typeof import('../chime')>()), chime: h.chime }));
vi.mock('./shipsLink', async (orig) => ({
  ...(await orig<typeof import('./shipsLink')>()),
  onShipsMessage: (fn: typeof h.listener) => {
    h.listener = fn;
    return () => (h.listener = null);
  },
}));

import { muted } from '../chime';
import { ToastRegion } from './ToastRegion';
import { ships } from './shipsLink';

const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';
const alert = (over: Record<string, unknown> = {}) => ({ type: 'alert', name: 'Mizzen', line: 'wants to run Bash', tag: 'needs-you:s1', session: 's1', fleet: 'zulu', ...over });
const send = (m: Record<string, unknown>) => act(() => h.listener!(m as { type: string }));
let n = 0;

beforeEach(() => {
  h.go.mockClear();
  h.chime.mockClear();
  muted.value = false;
  ships.value = [{ session: 's1', status: 'needs_you' } as Ship];
  // The queue is module-level, so every test uses its own tag and dismisses what it opened.
  n++;
});

const arrive = async (over: Record<string, unknown> = {}) => {
  render(<ToastRegion />);
  send(alert({ tag: `t${n}`, ...over }));
  return screen.findByRole('button', { name: new RegExp(String(over.name ?? 'Mizzen')) });
};

test('a ship that starts waiting brings a toast naming it with its line, and a tap opens its Peek in its fleet and clears the toast', async () => {
  const toast = await arrive();
  expect(toast.textContent).toContain('wants to run Bash');
  expect(h.chime).toHaveBeenCalledTimes(1);
  fireEvent.click(toast);
  expect(h.go).toHaveBeenCalledWith('/app/inbox/s1', '?fleet=zulu');
  expect(screen.queryByRole('button', { name: /Mizzen/ })).toBeNull();
});

test('a toast for an entry that is gone lands on the Inbox, not a Peek that is empty', async () => {
  ships.value = [];
  fireEvent.click(await arrive());
  expect(h.go).toHaveBeenCalledWith('/app/inbox', '?fleet=zulu');
});

test('markup in a toast shows as text and nothing in it runs, and Dismiss removes it without going anywhere', async () => {
  const toast = await arrive({ name: 'Hostile', line: HOSTILE });
  expect(toast.textContent).toContain(HOSTILE);
  expect(document.querySelector('img, script')).toBeNull();
  expect((window as unknown as { __xss?: number }).__xss).toBeUndefined();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(screen.getByRole('status').textContent).not.toContain(HOSTILE);
  expect(h.go).not.toHaveBeenCalled();
});

test('a muted engineer still gets the toast, with no chime, and a message that is not an alert brings nothing', async () => {
  muted.value = true;
  await arrive({ name: 'Quiet' });
  expect(h.chime).not.toHaveBeenCalled();
  send({ type: 'ships', ships: [] });
  expect(screen.getAllByRole('button', { name: /Quiet|Dismiss/ })).toHaveLength(2);
});
