import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import type { ChatEntry, ChatSnapshot } from './chat';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc } from './router';
import { chats } from './shipsLink';
import type { Surface } from './surfaces';

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const fleetOf = (key: string) => ({ key, name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } }) as unknown as Fleet;
const proposal = { version: 1, id: 'campaign-c1-ab12-1', session: '_chat-x', kind: 'question' as const, created: '2026-01-02T03:04:05Z', title: 'Start the campaign?', question: { questions: [{ id: '1', text: 'Start it?', options: [{ label: 'Yes' }, { label: 'No' }] }] } };
const asked = { ...proposal, id: 'ask-1', title: 'Which panel?' };

// Each test gets its own fleet: pending keys are module-global and busy outlasts the request.
let n = 0;
let posts: string[] = [];
let interrupt: () => Response | Promise<Response>;
let messages: () => Promise<Response>;
let served: Partial<ChatSnapshot> | null;

// A null state leaves the session read unanswered, so the chat stays loading.
const open = (state: ChatSnapshot['state'] | null, extra: Partial<ChatSnapshot> = {}) => {
  const key = `intr${++n}`;
  served = state === null ? null : { fleet: key, running: true, state, messages: [], questions: [], ...extra };
  fleets.value = [fleetOf(key)];
  loc.value = { path: '/app/chat', search: `?fleet=${key}` };
  render(<ChatPanel surface={surface} title phone />);
  return key;
};
const snapshotReady = () => waitFor(() => expect(screen.getByRole('textbox', { name: 'Message AdmiralBot' })).toBeTruthy());
const button = () => screen.queryByRole('button', { name: 'Interrupt' });
const tap = () => fireEvent.click(button()!);
const busy = () => button()?.getAttribute('aria-busy');
const settledBusy = async () => {
  await waitFor(() => expect(busy()).toBe('true'));
  await waitFor(() => expect(busy()).not.toBe('true'));
};
const interrupts = () => posts.filter((p) => p.endsWith('/interrupt'));

const start = { loc: loc.value, fleets: fleets.value, chats: chats.value };
beforeEach(() => {
  posts = [];
  interrupt = () => new Response(JSON.stringify({ interrupted: true }));
  messages = async () => new Response('{}');
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    if (init?.method === 'POST') posts.push(url);
    if (url.endsWith('/interrupt')) return interrupt();
    if (url.endsWith('/messages')) return messages();
    if (url.endsWith('/session')) return served === null ? new Promise<Response>(() => {}) : new Response(JSON.stringify(served));
    return new Response('{}');
  });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  loc.value = start.loc;
  fleets.value = start.fleets;
  chats.value = start.chats;
});

test.each(['working', 'waiting'] as const)('the composer offers Interrupt while the chat is %s', async (state) => {
  open(state);
  expect(await screen.findByRole('button', { name: 'Interrupt' })).toBeTruthy();
});

test.each(['idle', ''] as const)("the composer has no Interrupt while the chat's state is %j", async (state) => {
  open(state);
  await snapshotReady();
  expect(button()).toBeNull();
});

test('the composer has no Interrupt before the chat has loaded', () => {
  open(null);
  expect(button()).toBeNull();
});

test('Interrupt posts to the fleet\'s interrupt route', async () => {
  const key = open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  tap();
  await waitFor(() => expect(interrupts()).toEqual([`/api/v1/chat/${key}/interrupt`]));
});

test('two quick taps send one request', async () => {
  let release!: () => void;
  interrupt = () => new Promise((r) => (release = () => r(new Response(JSON.stringify({ interrupted: true })))));
  open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  tap();
  await waitFor(() => expect(interrupts()).toHaveLength(1));
  tap();
  expect(interrupts()).toHaveLength(1);
  await act(async () => release());
});

test('Interrupt is disabled while a message is sending', async () => {
  messages = () => new Promise(() => {});
  open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  fireEvent.input(screen.getByRole('textbox', { name: 'Message AdmiralBot' }), { target: { value: 'hello' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect((button() as HTMLButtonElement).disabled).toBe(true));
});

test("a refused Interrupt shows the server's error in the composer's alert", async () => {
  interrupt = () => new Response(JSON.stringify({ error: 'The chat is not running.' }), { status: 409 });
  open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  tap();
  expect((await screen.findByRole('alert')).textContent).toBe('The chat is not running.');
});

test("an Interrupt that never reached Admiral says so, without a send's “Your message is kept.”", async () => {
  interrupt = () => {
    throw new TypeError('Failed to fetch');
  };
  open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  tap();
  expect((await screen.findByRole('alert')).textContent).toBe("Admiral isn't reachable.");
});

test('a press clears an earlier error first', async () => {
  interrupt = () => new Response(JSON.stringify({ error: 'The chat is not running.' }), { status: 409 });
  open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  tap();
  await screen.findByRole('alert');
  await settledBusy();
  interrupt = () => new Promise(() => {});
  tap();
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
});

test('an Interrupt answered with interrupted false shows no error', async () => {
  interrupt = () => new Response(JSON.stringify({ interrupted: false }));
  open('working');
  await screen.findByRole('button', { name: 'Interrupt' });
  tap();
  await settledBusy();
  expect(interrupts()).toHaveLength(1);
  expect(screen.queryByRole('alert')).toBeNull();
});

test('the chat thread shows an interrupted entry as a divider with its time', async () => {
  const entry: ChatEntry = { id: 'int-1', kind: 'interrupted', text: 'Interrupted', at: '2026-01-02T14:02:00Z' };
  open('idle', { messages: [entry] });
  expect((await screen.findByRole('separator')).textContent).toMatch(/^Interrupted · \S+/);
});

test("an interrupted turn's question reads Withdrawn with no answer controls while a campaign proposal stays open", async () => {
  const at = '2026-01-02T03:04:05Z';
  open('idle', {
    messages: [
      { id: 'q-ask-1', kind: 'question', text: 'Which panel?', at, card: asked },
      { id: 'q-campaign-c1-ab12-1', kind: 'question', text: 'Start the campaign?', at: '2026-01-02T03:04:06Z', card: proposal },
      { id: 'int-1', kind: 'interrupted', text: 'Interrupted', at: '2026-01-02T03:04:07Z' },
    ],
    questions: [{ ...proposal, answer: null }] as unknown as ChatSnapshot['questions'],
  });
  const withdrawn = (await screen.findByText('Withdrawn')).closest('.chat-card') as HTMLElement;
  expect(within(withdrawn).queryByRole('button')).toBeNull();
  expect(screen.getAllByRole('button', { name: 'Send answers' })).toHaveLength(1);
});
