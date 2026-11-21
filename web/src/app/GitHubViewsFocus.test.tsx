import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ViewsSection } from './GitHubPage';

const BUILTIN = [{ id: 'my-prs', builtin: true, hidden: false }];

function page(onAdd: (title: string, query: string) => Promise<boolean>) {
  render(<ViewsSection sourceKey="github-1" views={BUILTIN} inert={false} counts={{}} errors={{}} edit={() => {}} reorder={() => {}} onAdd={onAdd} />);
  const opener = screen.getByRole('button', { name: '+ New view' });
  fireEvent.click(opener);
  return opener;
}

// The card's live count asks the server; no answer is needed here.
beforeEach(() => void vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {}))));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('returns focus to the button that opened the card when the card is cancelled', async () => {
  const opener = page(async () => true);
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(document.activeElement).toBe(opener));
});

it('leaves focus alone when the view is added, even once every timer has run', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'requestAnimationFrame'] });
  const opener = page(async () => true);
  fireEvent.click(screen.getByRole('button', { name: 'Add view' }));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Add view' })).toBeNull());
  await act(async () => {
    await vi.runAllTimersAsync();
  });
  expect(document.activeElement).not.toBe(opener);
});
