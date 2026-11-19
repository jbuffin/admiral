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

const chatWith = (card: Record<string, unknown>) =>
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session')
      ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: [{ id: 'following-1', kind: 'following_card', text: 'Following the campaign', at: '2026-01-02T03:04:05Z', card }] }))
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

const shown = async (needle: string) => {
  render(<ChatPanel surface={surface} title phone />);
  const main = screen.getByRole('main');
  await waitFor(() => expect(main.textContent).toContain(needle));
  return main;
};

test('a Following card names the campaign and links its page', async () => {
  chatWith({ ship: 'saltpaw', campaign_id: 'c-7', title: 'Make the fleet map load faster' });
  const main = await shown('Make the fleet map load faster');
  expect(main.textContent).toContain('Following');
  const links = Array.from(main.querySelectorAll<HTMLAnchorElement>('.chat-card a'));
  expect(links.map((a) => a.getAttribute('href'))).toEqual(['/app/campaigns/c-7?fleet=alpha']);
});

test('a hostile campaign title in a Following card is text: no element and no link of its own', async () => {
  chatWith({ ship: 'saltpaw', campaign_id: 'c-7', title: HOSTILE });
  const main = await shown('<img src=x');
  expect(main.querySelector('img')).toBeNull();
  expect(Array.from(main.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toEqual(['/app/campaigns/c-7?fleet=alpha']);
});
