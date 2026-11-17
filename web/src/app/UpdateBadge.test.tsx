import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { UpdateBadge } from './UpdateBadge';

const available = {
  current_version: '0.9.1',
  latest_version: '0.9.2',
  update_available: true,
  checked_at: '2026-11-17T09:00:00Z',
  enabled: true,
  install_method: 'homebrew',
  upgrade_commands: [
    { label: 'Upgrade with Homebrew', command: 'brew upgrade admiral' },
    { label: 'Restart the service', command: 'launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral' },
  ],
  restart_note: 'If Admiral did not start as a service, restart it the way you started it.',
  executable_path: '/opt/homebrew/Cellar/admiral/0.9.1/bin/admiral',
  release_url: 'https://github.com/jbuffin/admiral/releases/tag/v0.9.2',
};

let answer: () => Response;
const fetchMock = vi.fn(async (_path: string) => answer());
const asked = () => fetchMock.mock.calls.filter(([p]) => String(p).endsWith('/api/v1/update'));
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

beforeEach(() => {
  fetchMock.mockClear();
  answer = () => json(available);
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// A badge that is absent must have looked first, or a component that never asks would pass.
async function settled() {
  await waitFor(() => expect(asked().length).toBeGreaterThan(0));
  await new Promise((r) => setTimeout(r, 20));
}

test('shows an Update available button when the server says an update is available', async () => {
  render(<UpdateBadge />);
  expect(await screen.findByRole('button', { name: /update available/i })).toBeTruthy();
});

test.each([
  ['no newer release', { ...available, update_available: false }],
  ['the check is off', { ...available, update_available: false, enabled: false, latest_version: null, checked_at: null }],
  ['no check has run yet', { ...available, update_available: false, latest_version: null, checked_at: null }],
])('shows nothing when %s', async (_name, body) => {
  answer = () => json(body);
  render(<UpdateBadge />);
  await settled();
  expect(screen.queryByRole('button', { name: /update/i })).toBeNull();
});

test('shows nothing when the server cannot answer', async () => {
  answer = () => json({ error: 'no' }, 500);
  render(<UpdateBadge />);
  await settled();
  expect(screen.queryByRole('button', { name: /update/i })).toBeNull();
});

test('tapping the badge opens a dialog with the versions, the upgrade commands, where Admiral runs and the release link', async () => {
  render(<UpdateBadge />);
  fireEvent.click(await screen.findByRole('button', { name: /update available/i }));
  const dialog = await screen.findByRole('dialog');
  expect(dialog.textContent).toContain('0.9.1');
  expect(dialog.textContent).toContain('0.9.2');
  expect(dialog.textContent).toContain('brew upgrade admiral');
  expect(dialog.textContent).toContain('launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral');
  expect(dialog.textContent).toContain('/opt/homebrew/Cellar/admiral/0.9.1/bin/admiral');
  expect(dialog.textContent).toContain('restart it the way you started it');
  const link = screen.getByRole('link', { name: /release/i }) as HTMLAnchorElement;
  expect(link.getAttribute('href')).toBe('https://github.com/jbuffin/admiral/releases/tag/v0.9.2');
});

test('the commands show in order, each as text', async () => {
  render(<UpdateBadge />);
  fireEvent.click(await screen.findByRole('button', { name: /update available/i }));
  const dialog = await screen.findByRole('dialog');
  const text = dialog.textContent ?? '';
  expect(text.indexOf('brew upgrade admiral')).toBeGreaterThan(-1);
  expect(text.indexOf('brew upgrade admiral')).toBeLessThan(text.indexOf('launchctl kickstart'));
});

test('a command is shown as plain text, never as markup', async () => {
  answer = () => json({ ...available, upgrade_commands: [{ label: 'Upgrade', command: '<img src=x onerror=alert(1)> && echo hi' }] });
  render(<UpdateBadge />);
  fireEvent.click(await screen.findByRole('button', { name: /update available/i }));
  const dialog = await screen.findByRole('dialog');
  expect(dialog.textContent).toContain('<img src=x onerror=alert(1)> && echo hi');
  expect(dialog.querySelector('img')).toBeNull();
});

test('Escape closes the dialog and leaves the badge', async () => {
  render(<UpdateBadge />);
  fireEvent.click(await screen.findByRole('button', { name: /update available/i }));
  fireEvent.keyDown(await screen.findByRole('dialog'), { key: 'Escape' });
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.getByRole('button', { name: /update available/i })).toBeTruthy();
});

const unchecked = { ...available, update_available: false, latest_version: null, checked_at: null };

async function withClock(body: () => Promise<void>) {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
  try {
    await body();
  } finally {
    vi.useRealTimers();
  }
}

test('asks again soon while no check has landed, and shows the badge once one has', () =>
  withClock(async () => {
    answer = () => json(unchecked);
    render(<UpdateBadge />);
    await vi.advanceTimersByTimeAsync(0);
    expect(asked().length).toBe(1);
    expect(screen.queryByRole('button', { name: /update available/i })).toBeNull();
    answer = () => json(available);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(screen.getByRole('button', { name: /update available/i })).toBeTruthy();
  }));

test('does not ask the server again right after the first answer', () =>
  withClock(async () => {
    answer = () => json(unchecked);
    render(<UpdateBadge />);
    await vi.advanceTimersByTimeAsync(4_000);
    expect(asked().length).toBe(1);
  }));

test.each([
  ['no check ever lands', () => json(unchecked)],
  ['the server cannot answer', () => json({ error: 'no' }, 500)],
])('stops asking every 5 seconds after about 2 minutes when %s, then asks every 5 minutes', (_name, reply) =>
  withClock(async () => {
    answer = reply;
    render(<UpdateBadge />);
    await vi.advanceTimersByTimeAsync(130_000);
    const n = asked().length;
    expect(n).toBeGreaterThan(10);
    expect(n).toBeLessThan(40);
    await vi.advanceTimersByTimeAsync(4 * 60_000);
    expect(asked().length).toBe(n);
    await vi.advanceTimersByTimeAsync(60_000 + 5_000);
    expect(asked().length).toBe(n + 1);
  }));

test('asks every 5 minutes, not every 5 seconds, once a check has landed', () =>
  withClock(async () => {
    render(<UpdateBadge />);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(4 * 60_000);
    expect(asked().length).toBe(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(asked().length).toBe(2);
  }));

test('asks every 5 minutes when the check is off', () =>
  withClock(async () => {
    answer = () => json({ ...unchecked, enabled: false });
    render(<UpdateBadge />);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(4 * 60_000);
    expect(asked().length).toBe(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(asked().length).toBe(2);
  }));
