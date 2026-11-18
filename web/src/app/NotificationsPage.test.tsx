import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { muted } from '../chime';
import * as api from './api';
import { NotificationsPage } from './NotificationsPage';

vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  fetchDevices: vi.fn(),
  fetchPushKey: vi.fn(),
  checkPushSubscription: vi.fn(),
  savePushSubscription: vi.fn(),
  dropPushSubscription: vi.fn(),
}));

const RAW = 'Registration failed - push service error';

let subscribe: ReturnType<typeof vi.fn>;

const rejectWith = (name: string, message: string) => () => Promise.reject(new DOMException(message, name));

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
  vi.mocked(api.fetchDevices).mockResolvedValue({ ok: true, data: [] });
  vi.mocked(api.fetchPushKey).mockResolvedValue({ ok: true, data: { key: 'dGVzdC1rZXk' } });
  vi.mocked(api.checkPushSubscription).mockResolvedValue({ ok: true, data: null });
  vi.mocked(api.savePushSubscription).mockResolvedValue({ ok: true, data: null });
  vi.mocked(api.dropPushSubscription).mockResolvedValue({ ok: true, data: null });
  subscribe = vi.fn();
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('PushManager', class {});
  vi.stubGlobal('Notification', { permission: 'default', requestPermission: vi.fn(async () => 'granted') });
  const registration = { pushManager: { subscribe, getSubscription: vi.fn(async () => null) } };
  Object.defineProperty(navigator, 'serviceWorker', { value: { ready: Promise.resolve(registration) }, configurable: true });
});
afterEach(() => {
  cleanup();
  localStorage.clear();
  muted.value = false;
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  Reflect.deleteProperty(navigator, 'serviceWorker');
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

async function tapSwitch() {
  const toggle = await screen.findByRole('switch', { name: 'Notifications on this device' });
  await waitFor(() => expect((toggle as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(toggle);
  return toggle as HTMLButtonElement;
}

test('a push service that refuses both tries shows a plain hint at the setting, not the browser text', async () => {
  subscribe.mockImplementation(rejectWith('AbortError', RAW));
  render(<NotificationsPage />);
  const toggle = await tapSwitch();
  const alert = await screen.findByRole('alert');
  const text = alert.textContent ?? '';
  expect(text).not.toContain(RAW);
  expect(text).toMatch(/push/i);
  expect(text).toMatch(/setting/i);
  expect(text).not.toMatch(/brave|chrome|google/i);
  await waitFor(() => expect(toggle.disabled).toBe(false));
  expect(toggle.getAttribute('aria-checked')).toBe('false');
  expect(api.savePushSubscription).not.toHaveBeenCalled();
});

test('any other subscribe failure still shows its own message', async () => {
  subscribe.mockImplementation(rejectWith('NotAllowedError', 'Permission denied by the browser'));
  render(<NotificationsPage />);
  const toggle = await tapSwitch();
  expect((await screen.findByRole('alert')).textContent).toBe('Permission denied by the browser');
  await waitFor(() => expect(toggle.disabled).toBe(false));
  expect(toggle.getAttribute('aria-checked')).toBe('false');
  expect(api.savePushSubscription).not.toHaveBeenCalled();
});

test('a failed save still shows the server error', async () => {
  const sub = { endpoint: 'https://push.example/abc', toJSON: () => ({ keys: { p256dh: 'p', auth: 'a' } }), unsubscribe: vi.fn(async () => true) };
  subscribe.mockResolvedValue(sub);
  vi.mocked(api.savePushSubscription).mockResolvedValue({ ok: false, error: 'The Mac refused this device.' } as never);
  render(<NotificationsPage />);
  await tapSwitch();
  expect((await screen.findByRole('alert')).textContent).toBe('The Mac refused this device.');
  expect(api.savePushSubscription).toHaveBeenCalledTimes(1);
});

test('a first subscribe that fails and a second that succeeds still turns push on', async () => {
  const sub = { endpoint: 'https://push.example/abc', toJSON: () => ({ keys: { p256dh: 'p', auth: 'a' } }), unsubscribe: vi.fn(async () => true) };
  subscribe.mockImplementationOnce(rejectWith('AbortError', RAW)).mockResolvedValue(sub);
  render(<NotificationsPage />);
  await tapSwitch();
  await waitFor(() => expect(api.savePushSubscription).toHaveBeenCalledTimes(1));
  expect(api.savePushSubscription).toHaveBeenCalledWith({ endpoint: 'https://push.example/abc', keys: { p256dh: 'p', auth: 'a' } });
  expect(screen.queryByRole('alert')).toBeNull();
});
