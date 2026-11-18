// The waiting count on a module's nav entry.
import { render } from '@testing-library/preact';
import { afterEach, expect, test } from 'vitest';
import { fleets } from './fleets';
import { syncSharedQueue } from './attention';
import { attentionCount, sharedInboxQueue } from './inbox';
import { sources } from './shipsLink';
import { WaitingBadge } from './WaitingBadge';
import type { Fleet, Source } from './api';

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

test('is not part of the attention count behind the title and badge', () => {
  fleets.value = [{ key: 'zulu', name: 'Zulu', docks: [] } as unknown as Fleet];
  sources.value = [{ key: 'deploys', name: 'Deploys', fleet: 'zulu', views: [], capabilities: ['page'], page: { state: 'ready', waiting: 5, version: 1 } } as unknown as Source];
  syncSharedQueue([], []);
  expect(attentionCount(sharedInboxQueue())).toBe(0);
});
