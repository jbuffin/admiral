import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { PRActor, PRDetail } from './api';
import { Reviewers } from './PRDetailSections';

afterEach(() => vi.unstubAllGlobals());

const PR = 'https://github.com/acme/api/pull/7';
const who = (login: string, bot = false): PRActor => ({ login, bot });
const detail = (over: Partial<PRDetail> = {}) =>
  ({
    url: PR,
    author: who('octocat'),
    reviews: [
      { author: who('hubot'), state: 'APPROVED' },
      { author: who('copilot-pull-request-reviewer', true), state: 'COMMENTED' },
      { author: who('defunkt'), state: 'CHANGES_REQUESTED' },
      { author: who('octocat'), state: 'COMMENTED' },
    ],
    requested: [who('defunkt'), who('monalisa'), who('acme/core')],
    ...over,
  }) as PRDetail;

const row = (login: string) => screen.getByText(login, { exact: false }).closest('li') as HTMLElement;
const button = (login: string) => within(row(login)).queryByRole('button', { name: /re-request review/i });

function stub(status = 200, body = '{}') {
  const calls: { path: string; method: string; body: string | undefined }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
    calls.push({ path, method: init.method ?? '', body: init.body as string | undefined });
    return new Response(body, { status });
  }));
  return calls;
}

test('Re-request review shows on a reviewer whose review stands, a bot included, and nowhere else', () => {
  render(<Reviewers detail={detail()} session="s-1" reread={() => undefined} />);

  expect(button('hubot')).toBeTruthy();
  expect(button('copilot-pull-request-reviewer')).toBeTruthy();
  expect(button('octocat')).toBeNull();
  expect(button('defunkt')).toBeNull();
  expect(button('monalisa')).toBeNull();
  expect(button('acme/core')).toBeNull();
  expect(screen.getAllByRole('button', { name: /re-request review/i })).toHaveLength(2);
});

test('a reviewer requested again reads Waiting, with no button', () => {
  render(<Reviewers detail={detail()} session="s-1" reread={() => undefined} />);

  expect(within(row('defunkt')).getByText('Waiting')).toBeTruthy();
  expect(within(row('hubot')).getByText('Approved')).toBeTruthy();
});

test('tapping Re-request review posts the PR and the login, then reads the PR again', async () => {
  const calls = stub();
  const reread = vi.fn();
  render(<Reviewers detail={detail()} session="s-1" reread={reread} />);

  expect(button('hubot')).toBeTruthy();
  fireEvent.click(button('hubot') as HTMLElement);

  await waitFor(() => expect(reread).toHaveBeenCalledTimes(1));
  expect(calls).toEqual([{ path: '/api/v1/ships/s-1/pr/rerequest', method: 'POST', body: JSON.stringify({ pr: PR, login: 'hubot' }) }]);
});

test("GitHub's refusal shows beside the row, keeps the button and reads nothing again", async () => {
  stub(502, JSON.stringify({ error: 'Could not resolve to a User with the login of hubot.' }));
  const reread = vi.fn();
  render(<Reviewers detail={detail()} session="s-1" reread={reread} />);

  expect(button('hubot')).toBeTruthy();
  fireEvent.click(button('hubot') as HTMLElement);

  expect((await screen.findByRole('alert')).textContent).toContain('Could not resolve to a User with the login of hubot.');
  expect(reread).not.toHaveBeenCalled();
  expect(button('hubot')).toBeTruthy();
});
