import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import { FindingsSheet } from './FindingsSheet';

const FIRST = '2026-01-02T03:04:05Z';
const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });
const findingsOf = (markdown: string) => ({ ship: 'calm-otter', first_launched: FIRST, fleet: 'alpha', findings: markdown });

const open = (fetchImpl: (url: string) => Promise<Response>, onClose = vi.fn()) => {
  const calls: string[] = [];
  vi.stubGlobal('fetch', (url: string) => {
    calls.push(String(url));
    return fetchImpl(String(url));
  });
  render(<FindingsSheet ship="calm-otter" firstLaunched={FIRST} onClose={onClose} />);
  return { calls, onClose };
};

afterEach(() => vi.unstubAllGlobals());

test('the sheet asks for its own voyage of the ship and says it is loading until the findings come', async () => {
  let release: (r: Response) => void = () => {};
  const { calls } = open(() => new Promise((r) => (release = r)));
  const dialog = await screen.findByRole('dialog');
  expect(dialog.textContent).toMatch(/loading/i);
  const url = new URL(calls[0], 'http://admiral.test');
  expect(url.pathname).toBe('/api/v1/ships/calm-otter/findings');
  expect(url.searchParams.get('first_launched')).toBe(FIRST);
  release(reply(200, findingsOf('# Done')));
  await waitFor(() => expect(dialog.textContent).not.toMatch(/loading/i));
});

test('the findings show as rendered markdown: a heading, a list and a code block', async () => {
  open(async () => reply(200, findingsOf('# The race\n\n- the cache\n- the lock\n\n```\nmutex.Lock()\n```\n')));
  expect((await screen.findByRole('heading', { name: 'The race' })).tagName).toMatch(/^H\d$/);
  expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['the cache', 'the lock']);
  expect(document.querySelector('pre')?.textContent).toContain('mutex.Lock()');
  expect(document.body.textContent).not.toContain('# The race');
});

test('findings that hold HTML show it as inert text, never as elements or handlers', async () => {
  open(async () => reply(200, findingsOf('<img src=x onerror="window.__xss=1"><script>window.__xss=1</script>\n\n[go](javascript:window.__xss=1)\n')));
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(dialog.textContent).toContain('<img'));
  expect(dialog.querySelector('img, script')).toBeNull();
  expect(dialog.querySelector('a[href^="javascript"]')).toBeNull();
  expect((window as unknown as { __xss?: number }).__xss).toBeUndefined();
});

test('a 404 says in plain words that the findings could not be opened, with no markdown shown', async () => {
  open(async () => reply(404, { error: 'no findings kept' }));
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(dialog.textContent).toMatch(/couldn.t|could not|no findings|not found|unavailable/i));
  expect(dialog.textContent).not.toMatch(/loading/i);
});

test('a failed fetch says so in plain words and leaves the sheet closable', async () => {
  const { onClose } = open(async () => {
    throw new TypeError('network down');
  });
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(dialog.textContent).toMatch(/couldn.t|could not|failed|try again|unavailable/i));
  expect(dialog.textContent).not.toMatch(/loading/i);
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  expect(onClose).toHaveBeenCalledTimes(1);
});

test('the Close button and the Escape key close the sheet', async () => {
  const { onClose } = open(async () => reply(200, findingsOf('# Done')));
  await screen.findByRole('heading', { name: 'Done' });
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  expect(onClose).toHaveBeenCalledTimes(1);
  fireEvent.keyDown(document, { key: 'Escape' });
  expect(onClose).toHaveBeenCalledTimes(2);
});

test('focus lands on Close, not on a link from the findings, while loading and once they have shown', async () => {
  let release: (r: Response) => void = () => {};
  open(() => new Promise((r) => (release = r)));
  const close = await screen.findByRole('button', { name: 'Close' });
  await waitFor(() => expect(document.activeElement).toBe(close));
  release(reply(200, findingsOf('See [the PR](https://github.com/acme/api/pull/7) first.\n')));
  await screen.findByRole('link', { name: 'the PR' });
  expect(document.activeElement).toBe(close);
});

test('Close comes before the findings in the sheet, so a tap lands on it at the top', async () => {
  open(async () => reply(200, findingsOf('# Done')));
  const heading = await screen.findByRole('heading', { name: 'Done' });
  const close = screen.getByRole('button', { name: 'Close' });
  expect(close.compareDocumentPosition(heading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});
