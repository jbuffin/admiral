import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, expect, test, vi } from 'vitest';
import type { Conversation } from './conversation';
import { Composer, MessageField, useComposer, type Target } from './Composer';

afterEach(() => vi.unstubAllGlobals());

const skills = {
  mark: '/',
  skills: [
    { name: 'tdd', description: 'Test-driven development.' },
    { name: 'triage', description: 'Sort the new issues.' },
    { name: 'review', description: 'Review the diff.' },
  ],
};

// Each test names its own skills route: the list is cached per route for a minute.
function Box({ skills = '/api/v1/ships/s1/skills', sent = [], start = '' }: { skills?: string; sent?: string[]; start?: string }) {
  const [text, setText] = useState(start);
  return <MessageField label="Message" placeholder="Message the ship" text={text} setText={setText} send={() => sent.push(text)} disabled={false} canSend skills={skills} />;
}

test('typing / offers the ship\'s skills, and arrows with Tab or a tap complete one', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(skills))));
  render(<Box />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;

  fireEvent.input(box, { target: { value: '/' } });
  const menu = await screen.findByRole('listbox', { name: 'Skills' });
  const offered = (name: RegExp) => screen.queryByRole('option', { name }) !== null;
  expect(screen.getAllByRole('option')).toHaveLength(3);
  expect(screen.getByRole('option', { name: /^\/review.*Review the diff\./ })).toBeTruthy();

  fireEvent.input(box, { target: { value: '/t' } });
  expect(screen.getAllByRole('option')).toHaveLength(2);
  expect(offered(/^\/tdd/) && offered(/^\/triage/) && !offered(/^\/review/)).toBe(true);
  fireEvent.keyDown(box, { key: 'ArrowDown' });
  expect(screen.getByRole('option', { selected: true, name: /^\/triage/ })).toBeTruthy();
  fireEvent.keyDown(box, { key: 'Tab' });
  expect(box.value).toBe('/triage ');
  expect(screen.queryByRole('listbox', { name: 'Skills' })).toBeNull();
  expect(menu.isConnected).toBe(false);

  fireEvent.input(box, { target: { value: '/rev' } });
  fireEvent.click(await screen.findByRole('option', { name: /review/ }));
  expect(box.value).toBe('/review ');

  fireEvent.input(box, { target: { value: '/x' } });
  expect(screen.queryByRole('listbox', { name: 'Skills' })).toBeNull();
});

test('Enter on a skill completed with Tab sends the whole line, and Enter while picking does not', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(skills))));
  const sent: string[] = [];
  render(<Box skills="/api/v1/ships/enter/skills" sent={sent} />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;

  fireEvent.input(box, { target: { value: '/t' } });
  await screen.findByRole('listbox', { name: 'Skills' });
  fireEvent.keyDown(box, { key: 'ArrowDown' });
  fireEvent.keyDown(box, { key: 'Tab' });
  expect(box.value).toBe('/triage ');
  fireEvent.input(box, { target: { value: '/triage the inbox' } });
  fireEvent.keyDown(box, { key: 'Enter' });
  expect(sent).toEqual(['/triage the inbox']);
});

test('a tapped skill leaves the caret in the box, ready for the rest of the line', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(skills))));
  render(<Box skills="/api/v1/ships/focus/skills" />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;

  fireEvent.input(box, { target: { value: '/rev' } });
  fireEvent.click(await screen.findByRole('option', { name: /review/ }));
  expect(box.value).toBe('/review ');
  expect(document.activeElement).toBe(box);
  expect(box.selectionStart).toBe('/review '.length);
});

test('Shift+Enter is left to the browser to break the line, and sends nothing', () => {
  const sent: string[] = [];
  render(<Box sent={sent} start="first line" />);
  const box = screen.getByRole('textbox', { name: 'Message' });

  expect(fireEvent.keyDown(box, { key: 'Enter', shiftKey: true })).toBe(true);
  expect(sent).toEqual([]);
  expect(fireEvent.keyDown(box, { key: 'Enter' })).toBe(false);
  expect(sent).toEqual(['first line']);
});

test('a paste lands where the cursor is, even when the browser inserts nothing', () => {
  const sent: string[] = [];
  render(<Box sent={sent} />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;
  const paste = (text: string) => fireEvent.paste(box, { clipboardData: { getData: (type: string) => (type === 'text/plain' ? text : '') } });

  fireEvent.input(box, { target: { value: 'Please ' } });
  box.setSelectionRange(7, 7);
  expect(paste('grill the gaps.')).toBe(false);
  expect(box.value).toBe('Please grill the gaps.');

  box.setSelectionRange(7, 7);
  paste('now, ');
  expect(box.value).toBe('Please now, grill the gaps.');

  fireEvent.keyDown(box, { key: 'Enter' });
  expect(sent).toEqual(['Please now, grill the gaps.']);
});

const ship: Target = { session: 's1', state: { enabled: true }, interruptible: false, hasConversation: true, subject: 'This ship' };
const conv: Conversation = { state: 'ok', items: [], more: false };

function Sender() {
  const box = useComposer(ship, conv);
  return <Composer target={ship} box={box} />;
}

test('a refused or lost send shows an error beside the box and keeps the text', async () => {
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  let lose = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      await gate;
      if (lose) throw new TypeError('Load failed');
      return new Response('{"error":"ship is anchored"}', { status: 409 });
    }),
  );
  render(<Sender />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;
  const send = () => screen.getByRole('button', { name: 'Send' });

  fireEvent.input(box, { target: { value: 'Keep this text' } });
  fireEvent.keyDown(box, { key: 'Enter' });
  await waitFor(() => expect(send().getAttribute('aria-busy')).toBe('true'));
  expect(box.value).toBe('Keep this text');
  expect(box.readOnly).toBe(true);

  release();
  expect((await screen.findByRole('alert')).textContent).toBe('This ship is not taking messages.');
  expect(box.value).toBe('Keep this text');
  expect(box.readOnly).toBe(false);
  expect(send().getAttribute('aria-busy')).not.toBe('true');
  expect(screen.queryByText('Keep this text', { selector: '.msg' })).toBeNull();

  lose = true;
  fireEvent.click(send());
  await waitFor(() => expect(screen.getByRole('alert').textContent).not.toBe('This ship is not taking messages.'));
  expect(screen.getByRole('alert').textContent).not.toBe('');
  expect(box.value).toBe('Keep this text');
  expect(screen.queryByText('Keep this text', { selector: '.msg' })).toBeNull();
});
