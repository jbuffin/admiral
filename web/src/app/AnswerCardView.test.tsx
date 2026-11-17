import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { AnswerCard } from './answerCard';
import { AnswerCardView } from './ConversationView';

afterEach(() => vi.unstubAllGlobals());

const permission = (reason: string): AnswerCard => ({ kind: 'permission', tool: 'Bash', reason });

function stub(status: number) {
  const calls: { path: string; body: string }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push({ path, body: String(init.body ?? '') });
    return new Response(status < 300 ? '{}' : JSON.stringify({ error: 'refused' }), { status });
  }));
  return calls;
}

test('one tap on Allow once posts one answer for the card and keeps the card, busy, until the ship moves on', async () => {
  const calls = stub(200);
  render(<AnswerCardView card={permission('allow-one')} session="card-ok" openTerminal={() => undefined} />);

  const allow = screen.getByRole('button', { name: 'Allow once' });
  fireEvent.click(allow);
  fireEvent.click(allow);
  await waitFor(() => expect(allow.getAttribute('aria-busy')).toBe('true'));

  expect(calls).toHaveLength(1);
  expect(calls[0].path).toBe('/api/v1/ships/card-ok/answer');
  expect(JSON.parse(calls[0].body)).toEqual({ card: 'permission', reason: 'allow-one', choice: 'allow' });
  expect(screen.getByText('Bash needs your permission')).toBeTruthy();
});

test('an answer the server refuses clears the card, and the next prompt shows its own', async () => {
  stub(409);
  const { rerender } = render(<AnswerCardView card={permission('refused-1')} session="card-refused" openTerminal={() => undefined} />);

  fireEvent.click(screen.getByRole('button', { name: 'Deny' }));
  await waitFor(() => expect(screen.queryByText('Bash needs your permission')).toBeNull());

  rerender(<AnswerCardView card={permission('refused-2')} session="card-refused" openTerminal={() => undefined} />);
  expect(screen.getByText('Bash needs your permission')).toBeTruthy();
});

test('a prompt Admiral cannot answer shows the terminal card, and its button opens the terminal', () => {
  const openTerminal = vi.fn();
  render(<AnswerCardView card={{ kind: 'terminal' }} session="card-terminal" openTerminal={openTerminal} />);

  expect(screen.getByText('Answer this in the terminal')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Open terminal' }));
  expect(openTerminal).toHaveBeenCalledTimes(1);
});
