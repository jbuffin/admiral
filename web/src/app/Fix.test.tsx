import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { FixOffer, Ship } from './api';
import { FixSheet } from './Fix';

afterEach(() => vi.unstubAllGlobals());

const pr = (n: number) => `https://github.com/acme/api/pull/${n}`;
const ship = (session: string, fix: FixOffer) => ({ session, name: 'Zulu', fix }) as Ship;

function stub(status = 201, body = '{}') {
  const calls: { path: string; method: string; body: string | undefined }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push({ path, method: init.method ?? '', body: init.body as string | undefined });
    return new Response(body, { status });
  }));
  return calls;
}

test('Fix PR says what the PR has, shows the command word for word, and posts no fields on confirm', async () => {
  const calls = stub();
  const onClose = vi.fn();
  const command = `resume feedback round 1 on ${pr(7)}`;
  render(<FixSheet ship={ship('fix-pr', { pr: pr(7), command, problems: [{ pr: pr(7), conflicts: true }] })} onClose={onClose} onBusy={() => undefined} />);

  expect(screen.getByRole('dialog', { name: 'Fix PR' })).toBeTruthy();
  expect(screen.getByText('#7 has merge conflicts')).toBeTruthy();
  expect(screen.getByText(command)).toBeTruthy();
  expect(screen.getAllByRole('button').map((b) => b.textContent)).toEqual(['Cancel', 'Fix PR']);

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(calls).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Fix PR' }));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(2));
  expect(calls).toEqual([{ path: '/api/v1/ships/fix-pr/fix', method: 'POST', body: undefined }]);
});

test('Fix stack lists each PR\'s problems, behind and failing checks by name', () => {
  const fix: FixOffer = { pr: pr(7), stack: true, command: `resume feedback round 1 on ${pr(7)}`, problems: [{ pr: pr(7), behind: true }, { pr: pr(8), failing_checks: ['build', 'lint'] }] };
  render(<FixSheet ship={ship('fix-stack', fix)} onClose={() => undefined} onBusy={() => undefined} />);

  expect(screen.getByRole('dialog', { name: 'Fix stack' })).toBeTruthy();
  expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['#7 is behind its base', '#8 has failing checks: build, lint']);
});

test('a fix the server refuses shows its message and keeps the sheet open', async () => {
  stub(409, JSON.stringify({ error: 'the PR has nothing to fix' }));
  const onClose = vi.fn();
  render(<FixSheet ship={ship('fix-refused', { pr: pr(7), command: 'c', problems: [{ pr: pr(7), conflicts: true }] })} onClose={onClose} onBusy={() => undefined} />);

  fireEvent.click(screen.getByRole('button', { name: 'Fix PR' }));
  expect((await screen.findByRole('alert')).textContent).toBe('the PR has nothing to fix');

  expect(onClose).not.toHaveBeenCalled();
});
