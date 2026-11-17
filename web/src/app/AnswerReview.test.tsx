import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { MooredPR, Ship } from './api';
import { AnswerReviewSheet } from './AnswerReview';

afterEach(() => vi.unstubAllGlobals());

const pr = (n: number, answer: string): MooredPR => ({ url: `https://github.com/acme/api/pull/${n}`, state: 'OPEN', review: 'CHANGES_REQUESTED', checks: '', mergeable: '', conflicts: false, feedback: 1, answer });
const ship = (session: string, prs: MooredPR[]) => ({ session, name: 'Zulu', prs }) as Ship;

function stub(status = 201, body = '{}') {
  const calls: { path: string; method: string; body: string }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push({ path, method: init.method ?? '', body: String(init.body ?? '') });
    return new Response(body, { status });
  }));
  return calls;
}

test('Answer review shows the exact command, Cancel sends nothing, and the confirm posts the PR and closes', async () => {
  const calls = stub();
  const onClose = vi.fn();
  const one = pr(7, 'resume feedback round 1 on https://github.com/acme/api/pull/7');
  render(<AnswerReviewSheet ship={ship('answer-pr', [one])} pr={one} onClose={onClose} onBusy={() => undefined} />);

  expect(screen.getByRole('dialog', { name: 'Answer review' })).toBeTruthy();
  expect(screen.getByText('Starts a feedback round on #7 in the same Commodore run.')).toBeTruthy();
  expect(screen.getByText('resume feedback round 1 on https://github.com/acme/api/pull/7')).toBeTruthy();
  expect(screen.getAllByRole('button').map((b) => b.textContent)).toEqual(['Cancel', 'Answer review']);

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(calls).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Answer review' }));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(2));
  expect(calls.map((c) => `${c.method} ${c.path} ${c.body}`)).toEqual(['POST /api/v1/ships/answer-pr/answer-review {"pr":"https://github.com/acme/api/pull/7"}']);
});

test('Answer all reviews shows the stack command and posts the stack URL', async () => {
  const calls = stub();
  const stack = 'https://github.com/acme/api/stack/4';
  render(<AnswerReviewSheet ship={ship('answer-stack', [pr(7, `/commodore ${stack}`), pr(8, `/commodore ${stack}`)])} stack={stack} onClose={() => undefined} onBusy={() => undefined} />);

  expect(screen.getByRole('dialog', { name: 'Answer all reviews' })).toBeTruthy();
  expect(screen.getByText('One Commodore run answers the feedback on every PR in the stack.')).toBeTruthy();
  expect(screen.getByText(`/commodore ${stack}`)).toBeTruthy();

  fireEvent.click(screen.getByRole('button', { name: 'Answer all reviews' }));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(JSON.parse(calls[0].body)).toEqual({ stack });
});

test('a round the server refuses shows its message, keeps the sheet open and frees the buttons', async () => {
  stub(409, JSON.stringify({ error: 'the ship is no longer moored' }));
  const onClose = vi.fn();
  const onBusy = vi.fn();
  const one = pr(7, 'resume feedback round 1 on https://github.com/acme/api/pull/7');
  render(<AnswerReviewSheet ship={ship('answer-refused', [one])} pr={one} onClose={onClose} onBusy={onBusy} />);

  fireEvent.click(screen.getByRole('button', { name: 'Answer review' }));
  expect((await screen.findByRole('alert')).textContent).toBe('the ship is no longer moored');

  expect(onClose).not.toHaveBeenCalled();
  expect(onBusy.mock.calls).toEqual([[true], [false]]);
  await waitFor(() => expect((screen.getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(false));
});
