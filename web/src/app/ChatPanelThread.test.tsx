import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import type { ChatEntry, ShipCard } from './chat';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { chats } from './shipsLink';
import { chatMessagePath, loc } from './router';
import type { Surface } from './surfaces';

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const alpha = { key: 'alpha', name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } } as unknown as Fleet;
const CARD = 'ship-calm-otter-2026-01-02T03:04:05Z-moored';
const card: ShipCard = { ship: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', fleet: 'alpha', skill: 'tdd', skill_kind: 'skill', result: 'moored', prs: [] };
const TURN = '[Report: Calm Otter] A ship you launched has reported back.\ncard: ' + CARD + '\ntitle: <b>raw</b> **not bold**';
const REPLY = 'Calm Otter is **done**: [PR 7](https://github.com/acme/api/pull/7)';

const entry = (id: string, at: string, over: Partial<ChatEntry>): ChatEntry => ({ id, kind: 'message', text: '', at, ...over });
const shipCard = entry(CARD, '2026-01-02T03:04:05Z', { kind: 'ship_card', text: 'Calm Otter', card });
const turn = (thread = CARD) => entry('t1', '2026-01-02T03:05:00Z', { role: 'user', text: TURN, thread });
const reply = (id = 'r1', text = REPLY, thread = CARD) => entry(id, '2026-01-02T03:05:30Z', { role: 'assistant', text, thread });

// What the server answers a session read with; a test that changes it also pushes the same snapshot, as the socket does.
let served: ChatEntry[] = [];
let sessionReads = 0;
const chatWith = (messages: ChatEntry[]) => {
  served = messages;
  sessionReads = 0;
  vi.stubGlobal('fetch', async (url: string) => {
    if (!url.endsWith('/session')) return new Response('{}');
    sessionReads++;
    return new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: served }));
  });
};
// The chat reads its session before and after starting it; a later push must not race the second read.
const settled = () => waitFor(() => expect(sessionReads).toBe(2));

const start = { loc: loc.value, fleets: fleets.value, chats: chats.value };
const scroll = Element.prototype.scrollIntoView;
beforeEach(() => {
  fleets.value = [alpha];
  loc.value = { path: '/app/chat', search: '?fleet=alpha' };
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  Element.prototype.scrollIntoView = scroll;
  loc.value = start.loc;
  fleets.value = start.fleets;
  chats.value = start.chats;
});

const shown = async (ready: () => unknown) => {
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(ready()).toBeTruthy());
};
const ready = () => document.querySelector('.msg.assistant');
const byId = (id: string) => document.querySelector(`[data-message-id="${id}"]`) as HTMLElement | null;

test("AdmiralBot's reply shows under its ship card as a markdown assistant message", async () => {
  chatWith([shipCard, turn(), reply()]);
  await shown(ready);
  const thread = document.querySelector(`[data-thread="${CARD}"]`) as HTMLElement;
  expect(thread).not.toBeNull();
  expect(thread.querySelector('.chat-card')?.textContent).toContain('Calm Otter');
  const msg = thread.querySelector('[data-message-id="r1"]') as HTMLElement;
  expect(msg.classList.contains('assistant')).toBe(true);
  expect(msg.querySelector('strong')?.textContent).toBe('done');
  expect(msg.querySelector('a')?.getAttribute('href')).toBe('https://github.com/acme/api/pull/7');
});

test('the turn text is not an engineer message and sits behind a collapsed Report sent to AdmiralBot disclosure', async () => {
  chatWith([shipCard, turn(), reply()]);
  await shown(ready);
  expect(document.querySelector('.msg.user')).toBeNull();
  const toggle = screen.getByRole('button', { name: 'Report sent to AdmiralBot' });
  expect(toggle.getAttribute('aria-expanded')).toBe('false');
  expect(screen.queryByText(/A ship you launched has reported back/, { exact: false })).toBeNull();
  fireEvent.click(toggle);
  expect(toggle.getAttribute('aria-expanded')).toBe('true');
  const text = screen.getByText(/A ship you launched has reported back/, { exact: false });
  expect(text.textContent).toContain('**not bold**');
  expect(text.closest('[data-thread]')?.getAttribute('data-thread')).toBe(CARD);
  expect(text.parentElement?.querySelector('b, strong')).toBeNull();
  fireEvent.click(toggle);
  expect(toggle.getAttribute('aria-expanded')).toBe('false');
});

test('a reply whose ship card is out of the window shows as an ordinary message, with the turn text collapsed the same way', async () => {
  chatWith([turn(), reply()]);
  await shown(ready);
  expect(document.querySelector('[data-thread]')).toBeNull();
  expect(byId('r1')?.classList.contains('assistant')).toBe(true);
  expect(document.querySelector('.msg.user')).toBeNull();
  const toggle = screen.getByRole('button', { name: 'Report sent to AdmiralBot' });
  expect(toggle.getAttribute('aria-expanded')).toBe('false');
  fireEvent.click(toggle);
  expect(screen.getByText(/A ship you launched has reported back/, { exact: false })).not.toBeNull();
});

test('an engineer message with no thread still shows as the engineer', async () => {
  chatWith([shipCard, entry('e1', '2026-01-02T03:06:00Z', { role: 'user', text: '[Report: Calm Otter] please write one' }), reply()]);
  await shown(ready);
  expect(byId('e1')?.classList.contains('user')).toBe(true);
  expect(screen.queryByRole('button', { name: 'Report sent to AdmiralBot' })).toBeNull();
});

test('opening the chat at a message scrolls to it and highlights it for a moment', async () => {
  const scrolled: Element[] = [];
  Element.prototype.scrollIntoView = function (this: Element) {
    scrolled.push(this);
  };
  chatWith([shipCard, turn(), reply('r1'), reply('r2', 'A second reply')]);
  loc.value = { path: chatMessagePath('alpha', 'r2'), search: '?fleet=alpha' };
  await shown(ready);
  await waitFor(() => expect(scrolled).toContain(byId('r2')));
  expect(scrolled).not.toContain(byId('r1'));
  expect(byId('r2')?.classList.contains('flash')).toBe(true);
  expect(byId('r1')?.classList.contains('flash')).toBe(false);
});

const pushSnapshot = (messages: ChatEntry[]) =>
  act(() => {
    served = messages;
    chats.value = { alpha: { fleet: 'alpha', running: true, state: 'idle', questions: [], messages } } as unknown as typeof chats.value;
  });
const convo = () => document.querySelector('.convo') as HTMLElement;

test('a message outside the window opens the chat pinned to the bottom, and later messages change nothing', async () => {
  const scrolled: Element[] = [];
  Element.prototype.scrollIntoView = function (this: Element) {
    scrolled.push(this);
  };
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(900);
  chatWith([shipCard, turn(), reply()]);
  loc.value = { path: chatMessagePath('alpha', 'gone'), search: '?fleet=alpha' };
  await shown(ready);
  await settled();
  expect(screen.getByRole('button', { name: 'Report sent to AdmiralBot' })).not.toBeNull();
  // A later render runs the focus effect again, so the check holds for the whole of its window.
  await pushSnapshot([shipCard, turn(), reply(), reply('r2', 'More')]);
  expect(byId('r2')).not.toBeNull();
  expect(scrolled).toHaveLength(0);
  expect(document.querySelector('.flash')).toBeNull();
  expect(convo().scrollTop).toBe(900);
});

test('a tap before the reply arrives scrolls to it and lights it once it renders', async () => {
  const scrolled: Element[] = [];
  Element.prototype.scrollIntoView = function (this: Element) {
    scrolled.push(this);
  };
  chatWith([shipCard, turn()]);
  loc.value = { path: chatMessagePath('alpha', 'r1'), search: '?fleet=alpha' };
  await shown(() => document.querySelector('.report-sent'));
  await settled();
  expect(scrolled).toHaveLength(0);
  await pushSnapshot([shipCard, turn(), reply()]);
  await waitFor(() => expect(scrolled).toContain(byId('r1')));
  expect(byId('r1')?.classList.contains('flash')).toBe(true);
});

test('an engineer who scrolls off the bottom before the reply arrives is not pulled to it', async () => {
  const scrolled: Element[] = [];
  Element.prototype.scrollIntoView = function (this: Element) {
    scrolled.push(this);
  };
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(900);
  chatWith([shipCard, turn()]);
  loc.value = { path: chatMessagePath('alpha', 'r1'), search: '?fleet=alpha' };
  await shown(() => document.querySelector('.report-sent'));
  await settled();
  fireEvent.scroll(convo(), { target: { scrollTop: 100 } });
  await pushSnapshot([shipCard, turn(), reply()]);
  expect(byId('r1')).not.toBeNull();
  expect(scrolled).toHaveLength(0);
  expect(document.querySelector('.flash')).toBeNull();
});
