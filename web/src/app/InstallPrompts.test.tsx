import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { installedHere, installOffer, listenForInstall, type InstallPromptEvent } from './install';
import { InstallPage, InstallSheet } from './InstallPrompts';
import { Unreachable } from './Unreachable';

const ipad = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';

function device({ ios, standalone = false }: { ios: boolean; standalone?: boolean }) {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(ios ? ipad : 'Mozilla/5.0 (X11; Linux x86_64) Chrome/130');
  vi.spyOn(navigator, 'maxTouchPoints', 'get').mockReturnValue(ios ? 5 : 0);
  Object.defineProperty(navigator, 'standalone', { value: standalone, configurable: true });
}

beforeEach(() => {
  localStorage.clear();
  installOffer.value = undefined;
  installedHere.value = false;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test('iPad Safari gets the Add to Home Screen sheet once, Escape closes it, and its steps stay in Install app', () => {
  device({ ios: true });
  const first = render(<InstallSheet />);
  expect(screen.getByRole('dialog', { name: /home screen/i })).toBeTruthy();
  fireEvent.keyDown(screen.getByRole('dialog', { name: /home screen/i }), { key: 'Escape' });
  expect(screen.queryByRole('dialog', { name: /home screen/i })).toBeNull();
  first.unmount();

  render(<InstallSheet />);
  expect(screen.queryByRole('dialog', { name: /home screen/i })).toBeNull();

  cleanup();
  render(<InstallPage />);
  expect(screen.getByText('Tap Share, then Add to Home Screen.')).toBeTruthy();
});

test('the installed app gets no sheet and says it is installed', () => {
  device({ ios: true, standalone: true });
  render(<InstallSheet />);
  expect(screen.queryByRole('dialog')).toBeNull();
  cleanup();
  render(<InstallPage />);
  expect(screen.getByText('Admiral is installed on this device.')).toBeTruthy();
});

test('a browser that offers the install keeps the offer from load, Install asks the browser, and then it says installed', async () => {
  device({ ios: false });
  render(<InstallPage />);
  expect(screen.getByText(/isn't offering to install Admiral/)).toBeTruthy();
  cleanup();

  listenForInstall();
  const prompt = vi.fn(async () => undefined);
  const offer = Object.assign(new Event('beforeinstallprompt', { cancelable: true }), { prompt, userChoice: Promise.resolve({ outcome: 'accepted' }) });
  window.dispatchEvent(offer);
  expect(offer.defaultPrevented).toBe(true);
  expect(installOffer.value).toBe(offer as unknown as InstallPromptEvent);

  render(<InstallPage />);
  fireEvent.click(screen.getByRole('button', { name: 'Install' }));
  await waitFor(() => expect(screen.getByText('Admiral is installed on this device.')).toBeTruthy());
  expect(prompt).toHaveBeenCalledOnce();
});

test("an unreachable server shows Can't reach your Mac with a Retry", () => {
  render(<Unreachable />);
  expect(screen.getByRole('alert').textContent).toContain("Can't reach your Mac");
  expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
});
