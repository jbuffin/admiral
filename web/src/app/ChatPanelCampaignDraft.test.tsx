import { cleanup, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc } from './router';
import type { Surface } from './surfaces';

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const alpha = { key: 'alpha', name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } } as unknown as Fleet;
const HOSTILE = '<img src=x onerror="window.__xss=1"> [fake](https://evil.example/x)';

const chatWith = (card: Record<string, unknown>, text = 'ignored') =>
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session')
      ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: [{ id: 'cd-1', kind: 'campaign_draft_card', text, at: '2026-01-02T03:04:05Z', card }] }))
      : new Response('{}'),
  );

const start = { loc: loc.value, fleets: fleets.value };
beforeEach(() => {
  fleets.value = [alpha];
  loc.value = { path: '/app/chat', search: '?fleet=alpha' };
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  loc.value = start.loc;
  fleets.value = start.fleets;
});

const card = async (needle: string) => {
  render(<ChatPanel surface={surface} title phone />);
  const main = screen.getByRole('main');
  await waitFor(() => expect(main.textContent).toContain(needle));
  return main;
};

test('a created campaign shows as a card with its title as plain text and one link to the campaign issue', async () => {
  chatWith({ title: 'Speed up the fleet map', url: 'https://github.com/acme/widgets/issues/99', created: true });
  const main = await card('Speed up the fleet map');
  const links = Array.from(main.querySelectorAll<HTMLAnchorElement>('.chat-card a'));
  expect(links.map((a) => a.getAttribute('href'))).toEqual(['https://github.com/acme/widgets/issues/99']);
  expect(main.querySelector('.chat-card')).not.toBeNull();
});

test('a hostile model-written title is text: no element, no link of its own', async () => {
  chatWith({ title: HOSTILE, url: 'https://github.com/acme/widgets/issues/99', created: true });
  const main = await card('<img src=x');
  expect(main.querySelector('img')).toBeNull();
  expect(Array.from(main.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toEqual(['https://github.com/acme/widgets/issues/99']);
});

test('a dismissed draft shows its title as thrown away, with no link', async () => {
  chatWith({ title: 'Speed up the fleet map' });
  const main = await card('Speed up the fleet map');
  expect(main.querySelector('.chat-card')!.textContent).toMatch(/thrown away/i);
  expect(main.querySelectorAll('.chat-card a')).toHaveLength(0);
});

test('a create that failed after the parent exists shows what failed beside the campaign link', async () => {
  chatWith({ title: 'Speed up the fleet map', url: 'https://github.com/acme/widgets/issues/99', created: true, failed: 'follow failed: gh is signed out' });
  const main = await card('follow failed: gh is signed out');
  expect(main.querySelector('.chat-card a')!.getAttribute('href')).toBe('https://github.com/acme/widgets/issues/99');
});
