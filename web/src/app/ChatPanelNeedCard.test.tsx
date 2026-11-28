import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, Ship } from './api';
import type { NeedCard } from './chat';
import { ChatPanel } from './ChatPanel';
import { fleets } from './fleets';
import { loc } from './router';
import { ships } from './shipsLink';
import type { Surface } from './surfaces';

const surface = { id: 'chat', title: 'Chat', scoped: true, line: '' } as unknown as Surface;
const alpha = { key: 'alpha', name: 'Homeport', color: '#2a9d8f', docks: [], chat: { on: true, off_reason: '', harnesses: [], harness_options: [], models: [], efforts: [] } } as unknown as Fleet;

const port = { id: 'port', text: 'Which port?', options: [{ label: 'Harbor', description: 'the **calm** one' }, { label: 'Bay' }] };
const mode = { id: 'mode', text: 'Which modes?', multi: true, options: [{ label: 'Fast' }, { label: 'Safe' }] };
const card: NeedCard = { ship: 'calm-otter', item: 'q-port', item_kind: 'question', questions: [port], state: 'open' };
const live = (over: Partial<Ship> = {}) => ({ session: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', status: 'working', ...over }) as Ship;

const entry = (id: string, text: string, c: Partial<NeedCard> = {}) => ({ id, kind: 'need_card', text, at: '2026-01-02T03:04:05Z', card: { ...card, ...c } });

// A session read serves the current entries; an answer POST is recorded and answered with `answer`.
function serve(entries: () => unknown[], answer: () => Response = () => new Response(JSON.stringify({ answered: 'q-port' }))) {
  const posts: { url: string; body: unknown }[] = [];
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    if (init?.method === 'POST' && url.includes('/need-cards/')) {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return answer();
    }
    return url.endsWith('/session') ? new Response(JSON.stringify({ fleet: 'alpha', running: true, state: 'idle', questions: [], messages: entries() })) : new Response('{}');
  });
  return posts;
}

const start = { loc: loc.value, fleets: fleets.value, ships: ships.value };
beforeEach(() => {
  fleets.value = [alpha];
  ships.value = [live()];
  loc.value = { path: '/app/chat', search: '?fleet=alpha' };
});
afterEach(() => {
  vi.unstubAllGlobals();
  loc.value = start.loc;
  fleets.value = start.fleets;
  ships.value = start.ships;
});

const shown = async () => {
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelector('.chat-card')).not.toBeNull());
  return document.querySelector('.chat-card') as HTMLElement;
};

test('an open need card shows the ship, the title and its choices', async () => {
  serve(() => [entry('need-1', 'Pick a port')]);
  const el = await shown();
  expect(el.textContent).toContain('Calm Otter');
  expect(el.textContent).toContain('Pick a port');
  expect(screen.getByRole('radio', { name: /Harbor/ })).not.toBeNull();
  expect(screen.getByRole('radio', { name: /Bay/ })).not.toBeNull();
  expect(screen.getByRole('button', { name: 'Send answers' })).not.toBeNull();
});

test('a ship not in the list shows by its session', async () => {
  ships.value = [];
  serve(() => [entry('need-1', 'Pick a port')]);
  const el = await shown();
  expect(el.textContent).toContain('calm-otter');
});

test('choosing then Send answers posts to the card route and nothing before', async () => {
  const posts = serve(() => [entry('need-1', 'Pick a port')]);
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Harbor/ }));
  expect(posts).toHaveLength(0);
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].url).toBe('/api/v1/chat/alpha/need-cards/need-1/answer');
  expect(posts[0].body).toEqual({ answers: { port: { choice: 'Harbor' } } });
});

test('a card with several questions sends once, with every answer, and only when all are answered', async () => {
  const posts = serve(() => [entry('need-1', 'Pick', { questions: [port, mode] })]);
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Bay/ }));
  expect((screen.getByRole('button', { name: 'Send answers' }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('checkbox', { name: /Fast/ }));
  fireEvent.click(screen.getByRole('checkbox', { name: /Safe/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ answers: { port: { choice: 'Bay' }, mode: { choice: ['Fast', 'Safe'] } } });
});

test('typed Other text and a note go in the same send', async () => {
  const posts = serve(() => [entry('need-1', 'Pick a port')]);
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Other/ }));
  fireEvent.input(screen.getByLabelText('Your answer to question 1'), { target: { value: 'Dock 9' } });
  fireEvent.input(screen.getByLabelText('Note (optional)'), { target: { value: 'soon' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ answers: { port: { other: 'Dock 9' } }, note: 'soon' });
});

test('a 409 shows no error, and the card shows answered once the next snapshot says so', async () => {
  let served: ReturnType<typeof entry> = entry('need-1', 'Pick a port');
  serve(
    () => [served],
    () => {
      served = entry('need-1', 'Pick a port', { state: 'answered', answered_in: 'inbox' });
      return new Response(JSON.stringify({ error: 'item q-port is already answered' }), { status: 409 });
    },
  );
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Harbor/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect(document.querySelector('.chat-card.quiet')?.textContent).toContain('Answered in the Inbox'));
  expect(screen.queryByRole('alert')).toBeNull();
  expect(screen.queryByText(/already answered/)).toBeNull();
});

test('a 409 with the card still open for now shows no error either', async () => {
  serve(() => [entry('need-1', 'Pick a port')], () => new Response(JSON.stringify({ error: 'item q-port is already answered' }), { status: 409 }));
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Harbor/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect((screen.getByRole('button', { name: 'Send answers' }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByRole('alert')).toBeNull();
});

test('any other failure shows its error on the card and keeps the choice', async () => {
  serve(() => [entry('need-1', 'Pick a port')], () => new Response(JSON.stringify({ error: 'disk full' }), { status: 500 }));
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Harbor/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('disk full'));
  expect(screen.getByRole('radio', { name: /Harbor/ }).getAttribute('aria-checked')).toBe('true');
});

test('two cards for one item id on different ships keep separate drafts', async () => {
  ships.value = [live(), live({ session: 'brisk-heron', name: 'Brisk Heron' })];
  serve(() => [entry('need-1', 'First'), entry('need-2', 'Second', { ship: 'brisk-heron' })]);
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelectorAll('.chat-card')).toHaveLength(2));
  fireEvent.click(screen.getAllByRole('radio', { name: /Harbor/ })[0]);
  expect(screen.getAllByRole('radio', { name: /Harbor/ })[0].getAttribute('aria-checked')).toBe('true');
  expect(screen.getAllByRole('radio', { name: /Harbor/ })[1].getAttribute('aria-checked')).toBe('false');
});

test.each([
  [{ state: 'answered', answered_in: 'chat' }, 'Answered in the chat'],
  [{ state: 'answered', answered_in: 'inbox' }, 'Answered in the Inbox'],
  [{ state: 'removed' }, 'Removed'],
] as [Partial<NeedCard>, string][])('a card served %j collapses to one line saying %s, with the title', async (over, words) => {
  serve(() => [entry('need-1', 'Pick a port', over)]);
  const el = await shown();
  expect(el.classList.contains('quiet')).toBe(true);
  expect(el.textContent).toContain('Pick a port');
  expect(el.textContent).toContain(words);
  expect(el.textContent).not.toContain(words === 'Removed' ? 'Answered' : 'Removed');
  expect(screen.queryByRole('radio')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Send answers' })).toBeNull();
});

test('an answered in the chat card does not say Inbox, and the other way round', async () => {
  serve(() => [entry('need-1', 'A', { state: 'answered', answered_in: 'chat' }), entry('need-2', 'B', { state: 'answered', answered_in: 'inbox' })]);
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelectorAll('.chat-card.quiet')).toHaveLength(2));
  const [a, b] = Array.from(document.querySelectorAll('.chat-card.quiet')).map((e) => e.textContent ?? '');
  expect(a).not.toContain('Inbox');
  expect(b).not.toContain('in the chat');
});

test('the choices are buttons, which iPad Safari taps', async () => {
  serve(() => [entry('need-1', 'Pick a port')]);
  await shown();
  for (const name of [/Harbor/, /Bay/, /Other/]) expect(screen.getByRole('radio', { name }).tagName).toBe('BUTTON');
});

test('hostile ship-written text shows inert', async () => {
  const hostile = '<img src=x onerror="window.__xss=1"><script>window.__xss=1</script>';
  serve(() => [entry('need-1', hostile, { questions: [{ id: 'port', text: hostile, options: [{ label: hostile, description: hostile }] }] })]);
  const el = await shown();
  expect(el.textContent).toContain(hostile);
  expect(el.querySelector('img, script')).toBeNull();
  expect((window as unknown as { __xss?: number }).__xss).toBeUndefined();
});

test('an option description renders through the shared markdown renderer', async () => {
  serve(() => [entry('need-1', 'Pick a port')]);
  const el = await shown();
  expect(el.querySelector('.inbox-option-desc strong')?.textContent).toBe('calm');
});

test('a title that equals the only question is shown once, and one that differs is still shown', async () => {
  serve(() => [entry('need-1', ' Which port? ')]);
  const el = await shown();
  expect(el.textContent?.split('Which port?')).toHaveLength(2);
});

test('a title that differs from the question is shown beside it', async () => {
  serve(() => [entry('need-1', 'Pick a port')]);
  const el = await shown();
  expect(el.textContent).toContain('Pick a port');
  expect(el.textContent).toContain('Which port?');
});

test('a hidden character in the title shows by its code point, open and collapsed', async () => {
  serve(() => [entry('need-1', 'Pick\u202Ea port'), entry('need-2', 'Done\u202Eit', { state: 'removed' })]);
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() => expect(document.querySelectorAll('.chat-card')).toHaveLength(2));
  const [open, quiet] = Array.from(document.querySelectorAll('.chat-card')).map((e) => e.textContent ?? '');
  expect(open).toContain('Pick⟨U+202E⟩a port');
  expect(quiet).toContain('Done⟨U+202E⟩it');
});

test('a 404 from the card route shows its error', async () => {
  serve(() => [entry('need-1', 'Pick a port')], () => new Response(JSON.stringify({ error: 'no such card' }), { status: 404 }));
  await shown();
  fireEvent.click(screen.getByRole('radio', { name: /Harbor/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('no such card'));
});
