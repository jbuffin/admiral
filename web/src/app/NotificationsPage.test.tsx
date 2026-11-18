import { act, fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchDevices: vi.fn(async () => ({ ok: true, data: [] })) }));
vi.mock('./pushDevice', async (orig) => ({
  ...(await orig<typeof import('./pushDevice')>()),
  readPushDevice: vi.fn(async () => ({
    key: 'k',
    problem: null,
    inputs: { secure: true, hasPushManager: true, hasServiceWorker: true, ios: false, standalone: false, permission: 'default', key: 'ready', subscribed: false, held: false },
  })),
}));

import { muted } from '../chime';
import { NotificationsPage } from './NotificationsPage';

const sound = () => screen.getByRole('switch', { name: 'Attention sounds' });
const fromAnotherPage = (key: string | null, value: string | null) =>
  act(() => {
    if (key === null) localStorage.clear();
    else if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
    window.dispatchEvent(new StorageEvent('storage', { key, newValue: value }));
  });

beforeEach(() => {
  localStorage.clear();
  muted.value = false;
});

afterEach(() => {
  localStorage.clear();
  muted.value = false;
});

test('the Attention sounds switch is on when nothing is stored, and is not the push switch', async () => {
  render(<NotificationsPage />);
  expect(sound().getAttribute('aria-checked')).toBe('true');
  expect(await screen.findByRole('switch', { name: 'Notifications on this device' })).not.toBe(sound());
});

test('the switch shows on for a muted value of false and off for true', () => {
  localStorage.setItem('admiral.muted', '0');
  muted.value = false;
  const first = render(<NotificationsPage />);
  expect(sound().getAttribute('aria-checked')).toBe('true');
  first.unmount();

  localStorage.setItem('admiral.muted', '1');
  muted.value = true;
  render(<NotificationsPage />);
  expect(sound().getAttribute('aria-checked')).toBe('false');
});

test('a click turns the sound off and stores it, and the next click turns it on and stores that', () => {
  render(<NotificationsPage />);
  fireEvent.click(sound());
  expect(sound().getAttribute('aria-checked')).toBe('false');
  expect(localStorage.getItem('admiral.muted')).toBe('1');
  fireEvent.click(sound());
  expect(sound().getAttribute('aria-checked')).toBe('true');
  expect(localStorage.getItem('admiral.muted')).toBe('0');
});

test('a change to the preference while the page is open shows in the switch', () => {
  render(<NotificationsPage />);
  act(() => {
    muted.value = true;
  });
  expect(sound().getAttribute('aria-checked')).toBe('false');
  act(() => {
    muted.value = false;
  });
  expect(sound().getAttribute('aria-checked')).toBe('true');
});

test('a page loaded with 1 stored starts with the switch off', async () => {
  localStorage.setItem('admiral.muted', '1');
  vi.resetModules();
  const fresh = await import('../chime');
  expect(fresh.muted.value).toBe(true);
});
