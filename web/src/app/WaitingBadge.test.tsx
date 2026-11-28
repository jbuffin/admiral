// The waiting count on a module's nav entry.
import { render } from '@testing-library/preact';
import { afterEach, expect, test } from 'vitest';
import { fleets } from './fleets';
import { sources } from './shipsLink';
import { WaitingBadge } from './WaitingBadge';

afterEach(() => {
  sources.value = [];
  fleets.value = [];
});

test('shows the count in the Orders badge style', () => {
  const { container } = render(<WaitingBadge count={4} />);
  const badge = container.querySelector('.nav-count.needs');
  expect(badge?.textContent).toBe('4');
});

test('shows nothing at zero', () => {
  const { container } = render(<WaitingBadge count={0} />);
  expect(container.querySelector('.nav-count')).toBeNull();
});
