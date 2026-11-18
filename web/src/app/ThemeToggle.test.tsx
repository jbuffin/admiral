import { cleanup, fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const DARK = '(prefers-color-scheme: dark)';
let systemDark = false;
const listeners = new Set<() => void>();

// Answers by query: only the dark-scheme query reports the faked system theme.
function stubSystem(dark: boolean) {
  systemDark = dark;
  vi.stubGlobal('matchMedia', (q: string) => ({
    get matches() {
      return q === DARK ? systemDark : false;
    },
    media: q,
    addEventListener: (_: string, fn: () => void) => q === DARK && listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  }));
}
const changeSystem = (dark: boolean) => {
  systemDark = dark;
  for (const fn of [...listeners]) fn();
};
const html = () => document.documentElement;

beforeEach(() => {
  vi.resetModules();
  localStorage.clear();
  html().removeAttribute('data-theme');
  listeners.clear();
  stubSystem(false);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// A fresh module per render, so a test's choice never leaks into the next.
async function show() {
  const { ThemeToggle: Fresh } = await import('./ThemeToggle');
  return render(<Fresh />);
}
const toDark = () => screen.getByRole('button', { name: 'Switch to dark mode' });
const toLight = () => screen.getByRole('button', { name: 'Switch to light mode' });

test('the toggle is just an icon: an svg, no visible text, and the action as its name', async () => {
  await show();
  const btn = toDark();
  expect(btn.querySelector('svg')).toBeTruthy();
  expect(btn.textContent?.trim()).toBe('');
  expect(btn.getAttribute('title')).toBe('Switch to dark mode');
});

test('a click on a light shell sets dark, remembers it and offers light; a second click goes back', async () => {
  await show();
  const moon = toDark().innerHTML;
  fireEvent.click(toDark());
  expect(html().getAttribute('data-theme')).toBe('dark');
  expect(localStorage.getItem('admiral.theme')).toBe('dark');
  expect(toLight().getAttribute('title')).toBe('Switch to light mode');
  expect(toLight().innerHTML).not.toBe(moon);

  fireEvent.click(toLight());
  expect(html().getAttribute('data-theme')).toBe('light');
  expect(localStorage.getItem('admiral.theme')).toBe('light');
  expect(toDark()).toBeTruthy();
});

test('a dark system with no choice shows the sun, and a click gives light', async () => {
  stubSystem(true);
  await show();
  fireEvent.click(toLight());
  expect(html().getAttribute('data-theme')).toBe('light');
  expect(localStorage.getItem('admiral.theme')).toBe('light');
});

test('a stored choice wins over the system', async () => {
  localStorage.setItem('admiral.theme', 'dark');
  await show();
  expect(toLight()).toBeTruthy();
  fireEvent.click(toLight());
  expect(html().getAttribute('data-theme')).toBe('light');
});

test('a junk stored value is ignored and never reaches the page', async () => {
  localStorage.setItem('admiral.theme', 'purple');
  await show();
  expect(toDark()).toBeTruthy();
  expect(html().getAttribute('data-theme')).toBeNull();
  fireEvent.click(toDark());
  expect(html().getAttribute('data-theme')).toBe('dark');
});

test('storage that throws leaves the toggle working for the session', async () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('blocked');
  });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('blocked');
  });
  await show();
  fireEvent.click(toDark());
  expect(html().getAttribute('data-theme')).toBe('dark');
  expect(toLight()).toBeTruthy();
});

test('with no stored choice a system change flips the icon and name; after a click it no longer does', async () => {
  await show();
  const moon = toDark().innerHTML;
  changeSystem(true);
  expect((await screen.findByRole('button', { name: 'Switch to light mode' })).innerHTML).not.toBe(moon);
  expect(html().getAttribute('data-theme')).toBeNull();

  fireEvent.click(toLight());
  changeSystem(false);
  expect(html().getAttribute('data-theme')).toBe('light');
  expect(toDark()).toBeTruthy();
});
