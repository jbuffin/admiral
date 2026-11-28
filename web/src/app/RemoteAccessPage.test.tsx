import { cleanup, render, screen, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { RemoteAccess } from './RemoteAccessPage';

vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  fetchDevices: vi.fn(),
  fetchRemote: vi.fn(),
}));

const device = (over: Partial<api.Device>): api.Device => ({
  id: 'd-1',
  name: 'Phone',
  login: '',
  pairedAt: '2026-11-20T08:00:00Z',
  lastSeen: '2026-11-28T08:30:00Z',
  notifications: false,
  ...over,
});

const rowOf = async (d: api.Device) => {
  vi.mocked(api.fetchDevices).mockResolvedValue({ ok: true, data: [d] });
  render(<RemoteAccess />);
  return within(await screen.findByRole('listitem', { name: d.name }));
};

beforeEach(() => {
  vi.mocked(api.fetchRemote).mockResolvedValue({ ok: true, data: { tailscale: false, allowedLogins: [], url: '', serveOff: '', listenExplicit: false, checks: [] } });
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

test('a device that follows push events shows Notifications on · forwarded and keeps its subline', async () => {
  const row = await rowOf(device({ forwarding: 'on', lastFollowed: null }));
  expect(row.getByText('Notifications on · forwarded')).toBeTruthy();
  expect(row.getByText(/paired .* · last seen /)).toBeTruthy();
  expect(row.queryByText(/Forwarding stopped/)).toBeNull();
});

test('a device whose forwarding stopped shows when it last followed, in the warning tone', async () => {
  const row = await rowOf(device({ forwarding: 'stopped', lastFollowed: '2026-11-28T08:45:00Z' }));
  const line = row.getByText(/^Forwarding stopped · last followed /);
  expect(line.classList.contains('warn')).toBe(true);
  expect(line.textContent).toMatch(/last followed \S/);
  expect(row.queryByText(/Notifications on/)).toBeNull();
});

test('a plain device shows no forwarding line beside one that has it', async () => {
  vi.mocked(api.fetchDevices).mockResolvedValue({
    ok: true,
    data: [device({ id: 'd-1', name: 'Phone', forwarding: 'on' }), device({ id: 'd-2', name: 'Tablet', forwarding: null, lastFollowed: null })],
  });
  render(<RemoteAccess />);
  const phone = within(await screen.findByRole('listitem', { name: 'Phone' }));
  const tablet = within(screen.getByRole('listitem', { name: 'Tablet' }));
  expect(phone.getByText('Notifications on · forwarded')).toBeTruthy();
  expect(tablet.getByText(/paired .* · last seen /)).toBeTruthy();
  expect(tablet.queryByText(/forwarded|Forwarding stopped/)).toBeNull();
});
