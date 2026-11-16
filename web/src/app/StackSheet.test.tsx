import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { expect, test, vi } from 'vitest';
import type { Result, StackShape } from './api';
import { StackSheet } from './StackSheet';

const lines = [
  { url: 'https://github.com/acme/api/pull/7', head: 'branch-7', base: 'main' },
  { url: 'https://github.com/acme/api/pull/8', head: 'branch-8', base: 'branch-7' },
];
const stacked: Result<StackShape> = { ok: true, data: { shape: 'stacked', stack_number: 4, prs: lines.map((l) => l.url) } };

function sheet(session: string, link: () => Promise<Result<StackShape>>, failed = async () => false) {
  const props = { onClose: vi.fn(), onShape: vi.fn() };
  render(<StackSheet lines={lines} pending={['ship', session, 'stack']} link={link} failed={failed} {...props} />);
  return props;
}

test('the sheet lists the PRs bottom first with their branches, and Cancel closes it without linking', () => {
  const link = vi.fn();
  const { onClose } = sheet('stack-list', link);

  expect(screen.getByRole('dialog', { name: 'Stack on GitHub' })).toBeTruthy();
  expect(screen.getByText('Make these 2 PRs one GitHub stack, bottom to top. Nothing is rebased or pushed; GitHub links the PRs so Merge can take them together.')).toBeTruthy();
  expect(screen.getAllByRole('listitem').map((li) => li.textContent?.replace(/\s+/g, ' '))).toEqual(['#7 branch-7 → main', '#8 branch-8 → branch-7']);
  expect(screen.getAllByRole('button').map((b) => b.textContent)).toEqual(['Cancel', 'Stack on GitHub']);

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(onClose).toHaveBeenCalled();
  expect(link).not.toHaveBeenCalled();
});

test('confirming links once however often it is tapped, hands over the shape and closes', async () => {
  const link = vi.fn(async () => stacked);
  const { onClose, onShape } = sheet('stack-link', link);

  const confirm = screen.getByRole('button', { name: 'Stack on GitHub' });
  fireEvent.click(confirm);
  fireEvent.click(confirm);
  await waitFor(() => expect(onClose).toHaveBeenCalled());

  expect(link).toHaveBeenCalledTimes(1);
  expect(onShape).toHaveBeenCalledWith({ shape: 'stacked', stack_number: 4, prs: lines.map((l) => l.url) });
});

test('a link GitHub refuses keeps the sheet open with the message and the button ready to try again', async () => {
  const link = vi.fn(async (): Promise<Result<StackShape>> => ({ ok: false, status: 502, error: 'GraphQL: could not link the pull requests (linkStack)' }));
  const { onClose } = sheet('stack-refused', link);

  fireEvent.click(screen.getByRole('button', { name: 'Stack on GitHub' }));
  expect((await screen.findByRole('alert')).textContent).toContain('could not link the pull requests');

  expect(onClose).not.toHaveBeenCalled();
  expect((screen.getByRole('button', { name: 'Stack on GitHub' }) as HTMLButtonElement).disabled).toBe(false);
});

test('a failed link that GitHub took anyway closes the sheet', async () => {
  const { onClose } = sheet('stack-took', async () => ({ ok: false, status: 502, error: 'timed out' }), async () => true);

  fireEvent.click(screen.getByRole('button', { name: 'Stack on GitHub' }));
  await waitFor(() => expect(onClose).toHaveBeenCalled());
});

test('a link the server answers 403 says Admiral restarted and offers Reload', async () => {
  sheet('stack-stale', async () => ({ ok: false, status: 403, error: 'forbidden' }));

  fireEvent.click(screen.getByRole('button', { name: 'Stack on GitHub' }));
  expect((await screen.findByRole('alert')).textContent).toBe('Admiral restarted, reload to stack');

  expect(screen.getByRole('button', { name: 'Reload' })).toBeTruthy();
});
