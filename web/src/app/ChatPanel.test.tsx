import { render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import type { InboxItem } from './inbox';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc } from './router';
import { inboxItems, inboxLoaded, shipsLoaded } from './shipsLink';
import type { Surface } from './surfaces';

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const fleet = (key: string, name: string, chat: Partial<Fleet['chat']>) => ({ key, name, color: '#2a9d8f', docks: [], chat: { on: false, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [], ...chat } }) as unknown as Fleet;

beforeEach(() => {
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session') ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', messages: [], questions: [] })) : new Response('{}'),
  );
  loc.value = { path: '/app/chat', search: '?fleet=alpha' };
});
afterEach(() => vi.unstubAllGlobals());

test("a fleet whose chat is off shows the server's reason and a way to its settings, and has no Open terminal", () => {
  fleets.value = [fleet('alpha', 'Homeport', { on: false, off_reason: 'This fleet has no Claude Code dock, so AdmiralBot has nowhere to run.' })];
  render(<ChatPanel surface={surface} title phone />);
  const main = screen.getByRole('main');
  expect(main.textContent).toContain('no Claude Code dock');
  expect(screen.getByRole('heading', { name: 'AdmiralBot · Homeport fleet' })).toBeTruthy();
  expect(screen.getByRole('link', { name: 'AdmiralBot settings' }).getAttribute('href')).toBe('/app/preferences/alpha/admiralbot?fleet=alpha');
  expect(screen.queryByRole('button', { name: 'Open terminal' })).toBeNull();
});

test('a fleet whose chat is on offers the conversation box and Open terminal', async () => {
  fleets.value = [fleet('alpha', 'Homeport', { on: true })];
  render(<ChatPanel surface={surface} title phone />);
  expect((await screen.findAllByRole('button', { name: 'Open terminal' })).length).toBeGreaterThan(0);
  expect(screen.getByRole('textbox', { name: 'Message AdmiralBot' })).toBeTruthy();
});

test("AdmiralBot's own waiting question sits in the rail beside the chat and links to its item in the chat's Peek", async () => {
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  fleets.value = [fleet('alpha', 'Homeport', { on: true })];
  inboxItems.value = [{ id: 'q1', session: '_chat-alpha', name: 'AdmiralBot', kind: 'question', title: 'Which panel?', version: 1, created: new Date().toISOString(), question: { questions: [{ id: '1', text: 'Which panel?', options: [{ label: 'Freeform' }, { label: 'Work queue' }] }] } } as unknown as InboxItem];
  shipsLoaded.value = true;
  render(<ChatPanel surface={surface} title phone={false} />);
  const rail = (await screen.findAllByRole('listitem')).find((li) => li.textContent?.includes('Which panel?'))!;
  expect(rail.textContent).toContain('AdmiralBot');
  expect(rail.querySelector('a')?.getAttribute('href')).toMatch(/^\/app\/chat\/alpha\/item\/_chat-alpha\/q1(\?|$)/);
});

test("opening that question's path shows it in Peek beside the chat, with its choices, while the chat stays", async () => {
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  fleets.value = [fleet('alpha', 'Homeport', { on: true })];
  inboxItems.value = [{ id: 'q1', session: '_chat-alpha', name: 'AdmiralBot', kind: 'question', title: 'Which panel?', version: 1, created: new Date().toISOString(), question: { questions: [{ id: '1', text: 'Which panel?', options: [{ label: 'Freeform' }, { label: 'Work queue' }] }] } } as unknown as InboxItem];
  shipsLoaded.value = true;
  inboxLoaded.value = true;
  loc.value = { path: '/app/chat/alpha/item/_chat-alpha/q1', search: '?fleet=alpha' };
  render(<ChatPanel surface={surface} title phone={false} />);
  expect(await screen.findByRole('radio', { name: 'Work queue' })).toBeTruthy();
  expect(screen.getByRole('heading', { name: 'AdmiralBot · Homeport fleet' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Send answers' })).toBeTruthy();
});
