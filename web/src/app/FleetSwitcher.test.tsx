import { fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import { fleets, loaded } from './fleets';
import { FleetSwitcher } from './FleetSwitcher';

const zulu = { key: 'zulu', name: 'Westwind', color: '#336699', ink: '#fff', docks: [] } as unknown as Fleet;
const fetchSpy = vi.fn(async () => new Response(JSON.stringify({ running: true }), { status: 200 }));
const assign = vi.fn();

beforeEach(() => {
  fleets.value = [zulu];
  loaded.value = true;
  vi.stubGlobal('fetch', fetchSpy);
  vi.stubGlobal('location', { ...window.location, assign });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  vi.clearAllMocks();
});

const openNewFleet = () => {
  fireEvent.click(screen.getByRole('button', { name: /Westwind/ }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'New fleet…' }));
};

test('New fleet… tells the person to run admiral setup in a terminal', () => {
  render(<FleetSwitcher sheet={false} />);
  openNewFleet();
  expect(screen.getByText(/admiral setup/)).toBeTruthy();
  expect(screen.getByText(/terminal/)).toBeTruthy();
});

test('New fleet… starts no setup and leaves the page', async () => {
  vi.useFakeTimers();
  render(<FleetSwitcher sheet={false} />);
  openNewFleet();
  await vi.advanceTimersByTimeAsync(5000);
  expect(fetchSpy).not.toHaveBeenCalled();
  expect(assign).not.toHaveBeenCalled();
});

test('the note does not claim setup is running or promise the old dashboard', () => {
  render(<FleetSwitcher sheet={false} />);
  openNewFleet();
  expect(screen.queryByText(/Setup is running|Opening the dashboard/)).toBeNull();
});
