import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { PairScreen } from './PairScreen';

const reply = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } }));

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test('a wrong code is refused in an alert and stays typed, and a device that paired before says it was signed out', async () => {
  const fetchMock = reply(400, { error: 'That code is wrong, used or expired.' });
  vi.stubGlobal('fetch', fetchMock);
  render(<PairScreen />);
  expect(screen.getByRole('heading', { name: 'Pair this app' })).toBeTruthy();
  const field = screen.getByRole('textbox', { name: /code/i }) as HTMLInputElement;
  expect(field.getAttribute('autocapitalize')).toBe('characters');

  fireEvent.input(field, { target: { value: 'ZZZZ-ZZZZ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Pair' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('wrong, used or expired'));
  expect(field.value).toBe('ZZZZ-ZZZZ');
  const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
  expect(url).toBe('/api/v1/pair');
  expect(JSON.parse(init.body as string)).toEqual({ code: 'ZZZZ-ZZZZ', cookie: true });

  cleanup();
  localStorage.setItem('admiral.paired', '1');
  render(<PairScreen />);
  expect(screen.getByRole('heading', { name: 'This device was signed out' })).toBeTruthy();
});
