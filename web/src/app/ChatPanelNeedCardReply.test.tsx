import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, Ship } from './api';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc } from './router';
import { ships } from './shipsLink';
import type { Surface } from './surfaces';

// Replying in words under an open need card: which cards offer it, what it sends and how the engineer's turn reads back.

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const alpha = { key: 'alpha', name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } } as unknown as Fleet;
const otter = { session: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', status: 'working' } as unknown as Ship;

const open = (id: string, c: Record<string, unknown>) => ({ id, kind: 'need_card', text: `Card ${id}`, at: '2026-01-02T03:04:05Z', card: { ship: 'calm-otter', item: `item-${id}`, state: 'open', ...c } });
const question = (id = 'need-q') => open(id, { item_kind: 'question', questions: [{ id: 'port', text: 'Which port?', options: [{ label: 'Harbor' }, { label: 'Bay' }] }] });
const stopCard = (id = 'need-s') => open(id, { item_kind: 'stop', answerable: true });
const permission = (id = 'need-p', answerable = true) => open(id, { item_kind: 'permission', tool: 'Bash', answerable });
const notStarted = (id = 'need-n') => open(id, { item_kind: 'not_started', answerable: false });
const routine = (id = 'need-r') => open(id, { item_kind: 'routine_proposal', ship: undefined, action: 'Run the sweep', reason: 'Fresh commits', routine_name: 'sweep' });
const review = (id = 'need-v') => open(id, { item_kind: 'review', events: ['COMMENT'], discard: true, pr_url: 'https://github.com/acme/api/pull/7' });

let sent: { url: string; body: Record<string, unknown> }[];
let served: unknown[];
let reply: () => Response;

beforeEach(() => {
  sent = [];
  served = [];
  reply = () => new Response(JSON.stringify({ sent: true }));
  fleets.value = [alpha];
  ships.value = [otter];
  loc.value = { path: '/app/chat', search: '?fleet=alpha' };
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    if (init?.method === 'POST' && url.endsWith('/messages')) {
      sent.push({ url, body: JSON.parse(String(init.body)) });
      return reply();
    }
    return url.endsWith('/session') ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: served })) : new Response('{}');
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  ships.value = [];
  fleets.value = [];
});

const shown = async (...entries: unknown[]) => {
  served = entries;
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelector('.chat-card')).not.toBeNull());
};
const cardOf = (text: string) => screen.getByText(text, { exact: false }).closest('.chat-card') as HTMLElement;
const replyBox = (el: HTMLElement) => within(el).queryByRole('textbox', { name: 'Reply to AdmiralBot' });

test.each([
  ['a question', question],
  ['a permission prompt', permission],
  ['a stop', stopCard],
  ['a routine proposal', routine],
  ['a review', review],
])('an open card for %s has a reply box under it', async (_k, make) => {
  const c = make();
  await shown(c);
  const box = replyBox(cardOf(c.text));
  expect(box).not.toBeNull();
  expect(within(cardOf(c.text)).getByRole('button', { name: 'Send reply' })).not.toBeNull();
});

test.each([
  ['a not-started card', notStarted()],
  ['a permission card answered in the terminal', permission('need-t', false)],
  ['an answered card', { ...question('need-a'), card: { ...question('need-a').card, state: 'answered', answered_in: 'chat' } }],
  ['a moved-on card', { ...routine('need-m'), card: { ...routine('need-m').card, state: 'moved_on' } }],
])('%s offers no reply, beside an open question card that does', async (_k, c) => {
  const beside = question('need-beside');
  await shown(c, beside);
  expect(replyBox(cardOf(beside.text))).not.toBeNull();
  const el = cardOf(c.text);
  expect(replyBox(el)).toBeNull();
  expect(within(el).queryByRole('button', { name: 'Send reply' })).toBeNull();
});

test('Send reply posts the typed text with the card id to the chat send route and clears the box', async () => {
  const c = question();
  await shown(c);
  const el = cardOf(c.text);
  const box = replyBox(el) as HTMLTextAreaElement;
  fireEvent.input(box, { target: { value: 'go with Harbor, it is calmer' } });
  expect(sent).toHaveLength(0);
  fireEvent.click(within(el).getByRole('button', { name: 'Send reply' }));
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0].url).toBe('/api/v1/chat/alpha/messages');
  expect(sent[0].body).toEqual({ text: 'go with Harbor, it is calmer', need_card: 'need-q' });
  await waitFor(() => expect((replyBox(el) as HTMLTextAreaElement).value).toBe(''));
});

test('Send reply is held while the box is empty', async () => {
  const c = question();
  await shown(c);
  const send = within(cardOf(c.text)).getByRole('button', { name: 'Send reply' }) as HTMLButtonElement;
  expect(send.disabled).toBe(true);
  fireEvent.input(replyBox(cardOf(c.text)) as HTMLTextAreaElement, { target: { value: '   ' } });
  expect(send.disabled).toBe(true);
});

test("a stop card keeps its Next prompt box and the reply is its own control that does not use the box's text", async () => {
  const c = stopCard();
  await shown(c);
  const el = cardOf(c.text);
  fireEvent.input(within(el).getByRole('textbox', { name: 'Next prompt' }), { target: { value: 'run the tests' } });
  fireEvent.input(replyBox(el) as HTMLTextAreaElement, { target: { value: 'why did it stop?' } });
  fireEvent.click(within(el).getByRole('button', { name: 'Send reply' }));
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0].body).toEqual({ text: 'why did it stop?', need_card: 'need-s' });
  expect((within(el).getByRole('textbox', { name: 'Next prompt' }) as HTMLTextAreaElement).value).toBe('run the tests');
});

test('each card keeps its own reply text and sends under its own id', async () => {
  const one = question('need-1');
  const two = routine('need-2');
  await shown(one, two);
  fireEvent.input(replyBox(cardOf(one.text)) as HTMLTextAreaElement, { target: { value: 'first words' } });
  fireEvent.input(replyBox(cardOf(two.text)) as HTMLTextAreaElement, { target: { value: 'second words' } });
  fireEvent.click(within(cardOf(two.text)).getByRole('button', { name: 'Send reply' }));
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0].body).toEqual({ text: 'second words', need_card: 'need-2' });
  expect((replyBox(cardOf(one.text)) as HTMLTextAreaElement).value).toBe('first words');
});

test('a refused reply shows the server message under its card and keeps the typed text', async () => {
  reply = () => new Response(JSON.stringify({ error: 'that card is already answered' }), { status: 409 });
  const c = question();
  await shown(c);
  const el = cardOf(c.text);
  fireEvent.input(replyBox(el) as HTMLTextAreaElement, { target: { value: 'too late?' } });
  fireEvent.click(within(el).getByRole('button', { name: 'Send reply' }));
  await waitFor(() => expect(within(cardOf(c.text)).getByRole('alert').textContent).toContain('that card is already answered'));
  expect((replyBox(cardOf(c.text)) as HTMLTextAreaElement).value).toBe('too late?');
});

const HEADER = '[Need card: need-q · ship calm-otter · item item-need-q · question]';
const turn = (id: string, text: string) => ({ id, kind: 'message', role: 'user', text, at: '2026-01-02T03:06:00Z' });

test("the engineer's card reply shows in the thread as their own words, without the server header", async () => {
  await shown(question(), turn('e1', `${HEADER}\n\ngo with Harbor`));
  await waitFor(() => expect(document.querySelector('[data-message-id="e1"]')).not.toBeNull());
  const msg = document.querySelector('[data-message-id="e1"]') as HTMLElement;
  expect(msg.classList.contains('user')).toBe(true);
  expect(msg.textContent?.trim()).toBe('go with Harbor');
  expect(document.body.textContent).not.toContain('Need card:');
});

const settled = (c: ReturnType<typeof question>) => ({ ...c, card: { ...c.card, state: 'answered', answered_in: 'inbox' } });
const refuseThenSettle = (c: ReturnType<typeof question>) => {
  reply = () => {
    served = [settled(c)];
    return new Response(JSON.stringify({ error: 'item item-need-q is already answered' }), { status: 409 });
  };
};

test('a reply refused because the card was answered elsewhere moves its words to the composer with a note', async () => {
  const c = question();
  await shown(c);
  refuseThenSettle(c);
  fireEvent.input(replyBox(cardOf(c.text)) as HTMLTextAreaElement, { target: { value: 'go with Harbor' } });
  fireEvent.click(within(cardOf(c.text)).getByRole('button', { name: 'Send reply' }));
  await waitFor(() => expect((screen.getByRole('textbox', { name: 'Message AdmiralBot' }) as HTMLTextAreaElement).value).toBe('go with Harbor'));
  expect(screen.getByRole('alert').textContent).toContain('already answered');
  expect(screen.queryByRole('textbox', { name: 'Reply to AdmiralBot' })).toBeNull();
});

test('words moved to a composer that already has text go after it on a new line', async () => {
  const c = question();
  await shown(c);
  refuseThenSettle(c);
  fireEvent.input(screen.getByRole('textbox', { name: 'Message AdmiralBot' }), { target: { value: 'half a thought' } });
  fireEvent.input(replyBox(cardOf(c.text)) as HTMLTextAreaElement, { target: { value: 'go with Harbor' } });
  fireEvent.click(within(cardOf(c.text)).getByRole('button', { name: 'Send reply' }));
  await waitFor(() => expect((screen.getByRole('textbox', { name: 'Message AdmiralBot' }) as HTMLTextAreaElement).value).toBe('half a thought\ngo with Harbor'));
});
