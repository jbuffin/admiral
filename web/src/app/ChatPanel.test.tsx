import { fireEvent, render, screen, within } from '@testing-library/preact';
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
const start = { loc: loc.value, fleets: fleets.value, items: inboxItems.value, ships: shipsLoaded.value, inbox: inboxLoaded.value };

afterEach(() => {
  vi.unstubAllGlobals();
  loc.value = start.loc;
  fleets.value = start.fleets;
  inboxItems.value = start.items;
  shipsLoaded.value = start.ships;
  inboxLoaded.value = start.inbox;
});

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

const showing = async (error: string, needsTerminal = false) => {
  vi.stubGlobal('fetch', async (url: string) =>
    url.endsWith('/session') ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', messages: [], questions: [], error, needsTerminal })) : new Response('{}'),
  );
  fleets.value = [fleet('alpha', 'Homeport', { on: true })];
  const { container } = render(<ChatPanel surface={surface} title phone />);
  await screen.findByText(error, { exact: false });
  return container.querySelector('.msg-note') as HTMLElement;
};

test('a chat the server says needs the terminal offers Open terminal beside its message', async () => {
  const note = await showing('Codex is asking you to trust Admiral\'s hooks: open the terminal and trust them', true);
  expect(within(note).getByRole('button', { name: 'Open terminal' })).toBeTruthy();
});

test('a chat error the server does not flag has no Open terminal beside it', async () => {
  const note = await showing("Admiral can't read this chat's transcript");
  expect(within(note).queryByRole('button', { name: 'Open terminal' })).toBeNull();
});

test("a paste with nothing editable focused fills AdmiralBot's message box, and is left alone while another field has focus", async () => {
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  fleets.value = [fleet('alpha', 'Homeport', { on: true })];
  render(<ChatPanel surface={surface} title phone />);
  const box = (await screen.findByRole('textbox', { name: 'Message AdmiralBot' })) as HTMLTextAreaElement;
  const paste = (on: Element, text: string) => fireEvent.paste(on, { clipboardData: { getData: (t: string) => (t === 'text/plain' ? text : ''), files: [], types: ['text/plain'] } });

  const other = document.body.appendChild(document.createElement('input'));
  other.focus();
  expect(paste(other, 'for the other field')).toBe(true);
  expect(box.value).toBe('');
  other.remove();

  expect(paste(document.body, 'ask about the ferry')).toBe(false);
  expect(box.value).toBe('ask about the ferry');
});
