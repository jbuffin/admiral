import { cleanup, fireEvent, render, screen } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { expect, test, vi } from 'vitest';
import { type AnswerBody, type Draft, blankDraft } from '../questions';
import type { InboxQuestion } from './inbox';
import { PeekQuestions } from './PeekQuestions';

const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';

const questions: InboxQuestion[] = [
  { id: 'p', text: `Which port? ${HOSTILE}`, options: [{ label: '80' }, { label: '8080', description: 'unprivileged' }], recommended: '8080' },
  { id: 'f', text: 'Which flags?', multi: true, options: [{ label: '--tls' }, { label: '--gzip' }] },
];

function Form({ onSend, start, locked }: { onSend: (b: AnswerBody) => void; start?: Partial<Draft>; locked?: boolean }) {
  const [draft, setDraft] = useState<Draft>({ ...blankDraft(), ...start });
  return <PeekQuestions questions={questions} draft={draft} edit={(fn) => setDraft(fn)} onSend={onSend} locked={locked} />;
}

test('shows the questions as text with the recommended option, and Send waits until every question is answered', () => {
  const onSend = vi.fn();
  const { container } = render(<Form onSend={onSend} />);
  expect(screen.getByText(/Which port\?/).textContent).toContain(HOSTILE);
  expect(container.querySelector('img, script')).toBeNull();
  expect(screen.getByRole('radio', { name: /8080/ }).textContent).toContain('Recommended');
  expect(screen.getByRole('radio', { name: '80' }).textContent).not.toContain('Recommended');
  expect(screen.getByText('0 of 2 answered')).toBeTruthy();
  const send = screen.getByRole('button', { name: 'Send answers' }) as HTMLButtonElement;
  expect(send.disabled).toBe(true);

  fireEvent.click(screen.getByRole('radio', { name: /8080/ }));
  expect(screen.getByText('1 of 2 answered')).toBeTruthy();
  expect(send.disabled).toBe(true);
  fireEvent.click(screen.getByRole('checkbox', { name: '--tls' }));
  expect(screen.getByText('2 of 2 answered')).toBeTruthy();
  expect(send.disabled).toBe(false);
  expect(onSend).not.toHaveBeenCalled();
});

test('Send hands over the choices, the multi-choice list and the trimmed note', () => {
  const onSend = vi.fn();
  render(<Form onSend={onSend} />);
  fireEvent.click(screen.getByRole('radio', { name: /8080/ }));
  fireEvent.click(screen.getByRole('checkbox', { name: '--tls' }));
  fireEvent.click(screen.getByRole('checkbox', { name: '--gzip' }));
  fireEvent.input(screen.getByRole('textbox', { name: 'Note (optional)' }), { target: { value: ' thanks ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  expect(onSend).toHaveBeenCalledTimes(1);
  expect(onSend).toHaveBeenCalledWith({ answers: { p: { choice: '8080' }, f: { choice: ['--tls', '--gzip'] } }, note: 'thanks' });
});

test('Other takes the typed answer instead of a choice', () => {
  const onSend = vi.fn();
  render(<Form onSend={onSend} />);
  fireEvent.click(screen.getByRole('radio', { name: /Other/ }));
  fireEvent.input(screen.getByRole('textbox', { name: 'Your answer to question 1' }), { target: { value: ' 9090 ' } });
  fireEvent.click(screen.getByRole('checkbox', { name: '--gzip' }));
  fireEvent.click(screen.getByRole('button', { name: 'Send answers' }));
  expect(onSend).toHaveBeenCalledWith({ answers: { p: { other: '9090' }, f: { choice: ['--gzip'] } } });
});

test('a refused answer shows its error and keeps Send usable, and a locked form cannot send again', () => {
  const answered = { picks: { p: { sel: ['80'], other: false, text: '' }, f: { sel: ['--tls'], other: false, text: '' } } };
  const { unmount } = render(<Form onSend={vi.fn()} start={{ ...answered, error: 'that answer is not valid' }} />);
  expect(screen.getByRole('alert').textContent).toBe('that answer is not valid');
  expect((screen.getByRole('button', { name: 'Send answers' }) as HTMLButtonElement).disabled).toBe(false);
  unmount();

  render(<Form onSend={vi.fn()} start={{ ...answered, error: 'inbox item is already answered' }} locked />);
  expect(screen.getByRole('alert').textContent).toBe('inbox item is already answered');
  expect((screen.getByRole('button', { name: 'Send answers' }) as HTMLButtonElement).disabled).toBe(true);
});

test('Send stays off while an answer is in flight', () => {
  const answered = { picks: { p: { sel: ['80'], other: false, text: '' }, f: { sel: ['--tls'], other: false, text: '' } } };
  render(<Form onSend={vi.fn()} start={{ ...answered, sending: true }} />);
  expect((screen.getByRole('button', { name: 'Send answers' }) as HTMLButtonElement).disabled).toBe(true);
});

test('the question text renders as markdown with headings, lists, links and code, and a script in it stays text', () => {
  const md = '## Heading\n\n- first point\n\n[the docs](https://example.com/docs) and `inline_code`\n\n```\nfenced line\n```';
  const qs: InboxQuestion[] = [{ id: 'm', text: md, options: [{ label: 'A' }] }];
  const { container } = render(<PeekQuestions questions={qs} draft={blankDraft()} edit={() => {}} onSend={() => {}} />);
  expect(container.querySelector('h1, h2, h3, h4')?.textContent).toBe('Heading');
  expect(container.querySelector('li')?.textContent).toBe('first point');
  expect(container.querySelector('a[href="https://example.com/docs"]')?.textContent).toBe('the docs');
  expect([...container.querySelectorAll('code')].some((c) => c.textContent === 'inline_code')).toBe(true);
  expect(container.querySelector('pre > code')?.textContent).toContain('fenced line');
  expect(container.querySelector('button h1, button h2, button h3, button p, button pre')).toBeNull();
});

test('a script in the question text comes out as text', () => {
  const qs: InboxQuestion[] = [{ id: 'x', text: 'Hello <script>window.__xss=1</script>', options: [{ label: 'A' }] }];
  const { container } = render(<PeekQuestions questions={qs} draft={blankDraft()} edit={() => {}} onSend={() => {}} />);
  expect(container.querySelector('script')).toBeNull();
  expect(container.textContent).toContain('<script>window.__xss=1</script>');
});

test("an option's description renders as inline markdown inside its button", () => {
  render(<Form onSend={vi.fn()} />);
  const option = screen.getByRole('radio', { name: /8080/ });
  expect(option.querySelector('code, strong')).toBeNull();
  cleanup();
  render(<PeekQuestions questions={[{ id: 'q', text: 'Which tool?', options: [{ label: 'pnpm', description: 'Use `pnpm` for **speed**' }] }]} draft={blankDraft()} edit={() => {}} onSend={vi.fn()} />);
  const radio = screen.getByRole('radio', { name: /pnpm/ });
  expect(radio.querySelector('code')!.textContent).toBe('pnpm');
  expect(radio.querySelector('strong')!.textContent).toBe('speed');
  expect(radio.textContent).not.toContain('`');
});
