import { render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test } from 'vitest';
import type { InboxItem } from './inbox';
import { InboxWaiting } from './InboxWaiting';
import { inboxItems, inboxLoaded, shipsLoaded } from './shipsLink';

const start = { items: inboxItems.value, ships: shipsLoaded.value, inbox: inboxLoaded.value };

afterEach(() => {
  inboxItems.value = start.items;
  shipsLoaded.value = start.ships;
  inboxLoaded.value = start.inbox;
});

const item = (session: string, id: string, title: string, created: string, kind = 'question') => ({ id, session, name: 'Visitor', kind, title, version: 1, created }) as unknown as InboxItem;

const seed = (...items: InboxItem[]) => {
  inboxItems.value = items;
  shipsLoaded.value = true;
  inboxLoaded.value = true;
};

test('a ship with no open Inbox item shows no waiting row', () => {
  seed();
  const { container } = render(<InboxWaiting session="s-none" />);
  expect(container.textContent).toBe('');
  expect(screen.queryByRole('link')).toBeNull();
});

test("one open item shows that it is waiting on the Inbox with the item's title, and Go to Inbox links to that item", () => {
  seed(item('s-one', 'w-one', 'Which port should the visitor use?', '2026-01-01T10:00:00Z'));
  render(<InboxWaiting session="s-one" />);
  expect(screen.getByText(/Waiting on the Inbox/)).toBeTruthy();
  expect(screen.getByText(/Which port should the visitor use\?/)).toBeTruthy();
  expect(screen.getByRole('link', { name: /Go to Inbox/ }).getAttribute('href')).toMatch(/^\/app\/inbox\/s-one\/item\/w-one(\?|$)/);
});

test('an item of another ship is no reason to show the waiting row', () => {
  seed(item('s-other', 'w-other', 'Not this ship', '2026-01-01T10:00:00Z'));
  const { container } = render(<InboxWaiting session="s-mine" />);
  expect(container.textContent).toBe('');
});

test('several open items show their count and Go to Inbox opens the oldest', () => {
  seed(item('s-many', 'w-new', 'Newer', '2026-01-01T12:00:00Z', 'review'), item('s-many', 'w-old', 'Older', '2026-01-01T09:00:00Z'));
  render(<InboxWaiting session="s-many" />);
  expect(screen.getByText(/Waiting on 2 Inbox items/)).toBeTruthy();
  expect(screen.getByRole('link', { name: /Go to Inbox/ }).getAttribute('href')).toMatch(/\/item\/w-old(\?|$)/);
});

test('the row goes away at once when the item is removed', async () => {
  seed(item('s-gone', 'w-gone', 'Soon answered', '2026-01-01T10:00:00Z'));
  const { container } = render(<InboxWaiting session="s-gone" />);
  expect(screen.getByRole('link', { name: /Go to Inbox/ })).toBeTruthy();
  inboxItems.value = [];
  await waitFor(() => expect(container.textContent).toBe(''));
});
