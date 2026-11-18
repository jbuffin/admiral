import { cleanup, fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

let systemDark = false;
const html = () => document.documentElement;
const status = () => [...document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')].map((m) => m.content);

beforeEach(() => {
  vi.resetModules();
  localStorage.clear();
  html().removeAttribute('data-theme');
  document.head.innerHTML =
    '<meta name="theme-color" media="(prefers-color-scheme: light)" content="#F4F5F7">' +
    '<meta name="theme-color" media="(prefers-color-scheme: dark)" content="#0B1118">';
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: q === '(prefers-color-scheme: dark)' ? systemDark : false,
    media: q,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  systemDark = false;
});

test('a stored choice is on the page before anything renders', async () => {
  localStorage.setItem('admiral.theme', 'dark');
  const { applyStoredTheme } = await import('./theme');
  applyStoredTheme();
  expect(html().getAttribute('data-theme')).toBe('dark');
});

test('a junk stored choice never reaches the page', async () => {
  localStorage.setItem('admiral.theme', 'purple');
  const { applyStoredTheme } = await import('./theme');
  applyStoredTheme();
  expect(html().getAttribute('data-theme')).toBeNull();
});

test('the status-bar colors follow an explicit choice and are untouched without one', async () => {
  const { ThemeToggle } = await import('./ThemeToggle');
  render(<ThemeToggle />);
  expect(status()).toEqual(['#F4F5F7', '#0B1118']);

  fireEvent.click(screen.getByRole('button', { name: 'Switch to dark mode' }));
  expect(status()).toEqual(['#0B1118', '#0B1118']);

  fireEvent.click(screen.getByRole('button', { name: 'Switch to light mode' }));
  expect(status()).toEqual(['#F4F5F7', '#F4F5F7']);
});
