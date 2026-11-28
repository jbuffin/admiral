import { render, screen } from '@testing-library/preact';
import { expect, test } from 'vitest';
import type { Item } from './api';
import { ConversationView } from './ConversationView';

const items: Item[] = [
  { id: 'u1', kind: 'user', text: 'Do the thing' },
  { id: 'x1', kind: 'interrupted', text: 'Interrupted', at: '2026-01-02T14:02:00Z' },
  { id: 'a1', kind: 'assistant', text: 'Back again' },
];

test('a ship conversation shows an interrupted item as a divider with its time', () => {
  render(<ConversationView conv={{ state: 'ok', items, more: false }} harness="claude" working={false} card={{ kind: 'none' }} session="calm-otter" loadOlder={() => {}} />);
  const divider = screen.getByRole('separator');
  expect(divider.textContent).toMatch(/^Interrupted · \S+/);
  expect(document.body.textContent).toContain('Back again');
});
