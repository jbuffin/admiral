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

const pointer = (coarse: boolean) => vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('coarse') ? coarse : !q.includes('coarse'), addEventListener() {}, removeEventListener() {} }));
const dropped = (data: Record<string, string>, files: unknown[] = []) => ({ getData: (type: string) => data[type] ?? '', files, types: Object.keys(data) });
const pasteAnywhere = (data: Record<string, string>, files: unknown[] = []) => fireEvent.paste(document.body, { clipboardData: dropped(data, files) });

function Field({ focus = false, primary = false, disabled = false, busy = false, locked = false }: { focus?: boolean; primary?: boolean; disabled?: boolean; busy?: boolean; locked?: boolean }) {
  const [text, setText] = useState('');
  return <MessageField label="Message" placeholder="Message the ship" text={text} setText={setText} send={() => {}} disabled={disabled} canSend busy={busy} locked={locked} focus={focus} primary={primary} />;
}

test('a fine pointer focuses the message box once it is enabled', () => {
  pointer(false);
  render(<Field focus />);
  expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'Message' }));
});

test('a coarse pointer leaves the message box unfocused so the keyboard stays down', () => {
  pointer(true);
  render(<Field focus />);
  expect(document.activeElement).not.toBe(screen.getByRole('textbox', { name: 'Message' }));
});

test('a box that enables after the engineer moved on does not take focus back', () => {
  pointer(false);
  const other = document.body.appendChild(document.createElement('input'));
  const { rerender } = render(<Field focus disabled />);
  other.focus();
  rerender(<Field focus disabled={false} />);
  expect(document.activeElement).toBe(other);
  other.remove();
});

test('a paste with nothing editable focused fills the primary box and is consumed', () => {
  pointer(false);
  render(<Field primary />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;
  expect(pasteAnywhere({ 'text/plain': 'from the clipboard' })).toBe(false);
  expect(box.value).toBe('from the clipboard');
  expect(document.activeElement).toBe(box);
});

test('a paste fills the primary box without raising the keyboard on a coarse pointer', () => {
  pointer(true);
  render(<Field primary />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;
  pasteAnywhere({ 'text/plain': 'from the clipboard' });
  expect(box.value).toBe('from the clipboard');
  expect(document.activeElement).not.toBe(box);
});

test('pasted markup stays literal text in the box', () => {
  pointer(false);
  render(<Field primary />);
  pasteAnywhere({ 'text/plain': '<img src=x onerror=alert(1)> **bold**' });
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('<img src=x onerror=alert(1)> **bold**');
  expect(document.querySelector('img')).toBeNull();
});

test('a paste is left alone while another field has focus', () => {
  pointer(false);
  const other = document.body.appendChild(document.createElement('input'));
  render(<Field primary />);
  other.focus();
  expect(fireEvent.paste(other, { clipboardData: dropped({ 'text/plain': 'for the other field' }) })).toBe(true);
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('');
  other.remove();
});

test('a paste is left alone while a dialog is open', () => {
  pointer(false);
  const dialog = document.body.appendChild(document.createElement('div'));
  dialog.setAttribute('aria-modal', 'true');
  render(<Field primary />);
  expect(pasteAnywhere({ 'text/plain': 'under the dialog' })).toBe(true);
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('');
  dialog.remove();
});

test.each([
  ['disabled', { disabled: true }],
  ['busy', { busy: true }],
  ['locked', { locked: true }],
])('a paste is left alone while the box is %s', (_, state) => {
  pointer(false);
  render(<Field primary {...state} />);
  expect(pasteAnywhere({ 'text/plain': 'not now' })).toBe(true);
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('');
});

test('a paste of only a file is left alone', () => {
  pointer(false);
  render(<Field primary />);
  expect(pasteAnywhere({}, [{ name: 'shot.png', type: 'image/png' }])).toBe(true);
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('');
});

test('a box that is not primary takes no paste from elsewhere', () => {
  pointer(false);
  render(<Field />);
  expect(pasteAnywhere({ 'text/plain': 'nobody asked' })).toBe(true);
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('');
});

test('the Composer passes primary to its box', () => {
  pointer(false);
  function Primary() {
    const box = useComposer(ship, conv);
    return <Composer target={ship} box={box} primary />;
  }
  render(<Primary />);
  pasteAnywhere({ 'text/plain': 'into the composer' });
  expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('into the composer');
});

test('with two boxes mounted, an unfocused paste goes only to the primary one, on a coarse pointer too', () => {
  pointer(true);
  render(
    <>
      <Field primary />
      <Field />
    </>,
  );
  const [primary, other] = screen.getAllByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement[];
  pasteAnywhere({ 'text/plain': 'one box only' });
  expect(primary.value).toBe('one box only');
  expect(other.value).toBe('');
});

test('a paste a primary box took is not inserted again by another primary box', () => {
  pointer(true);
  render(
    <>
      <Field primary />
      <Field primary />
    </>,
  );
  const [first, second] = screen.getAllByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement[];
  pasteAnywhere({ 'text/plain': 'taken once' });
  expect(first.value).toBe('taken once');
  expect(second.value).toBe('');
});
