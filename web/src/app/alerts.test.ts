import { describe, expect, it, vi } from 'vitest';
import type { Ship } from './api';
import type { InboxItem } from './inbox';
import { alertDestination, alertSearch, createAlertHandler } from './alerts';
import { createToasts } from './toasts';

// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

const ship = (extra: Partial<Ship>) => ({ session: 's1', name: 'Dory', fleet: 'a', status: 'needs_you', reason: 'permission:Bash', crew: [], ...extra }) as unknown as Ship;
const item = (id: string, session = 's1') => ({ version: 1, id, session, kind: 'question', created: '2026-10-22T10:00:00Z', title: 'Which port?' }) as InboxItem;
const none = { ships: [] as Ship[], items: [] as InboxItem[] };

describe('where a tapped toast lands', () => {
  it('opens Peek on the waiting ship', () => {
    expect(alertDestination({ target: 'peek', session: 's1' }, { ...none, ships: [ship({})] })).toBe('/app/inbox/s1');
  });

  it('opens Peek on an Inbox item that is still there', () => {
    expect(alertDestination({ target: 'peek', session: 's1', item: 'q-1' }, { ...none, items: [item('q-1')] })).toBe('/app/inbox/s1/item/q-1');
  });

  it('opens Peek on a crew boat that is still waiting', () => {
    const crew = [{ name: 'scout', status: 'needs_you', reason: 'question' }] as Ship['crew'];
    expect(alertDestination({ target: 'peek', session: 's1', crew: 'scout' }, { ...none, ships: [ship({ crew })] })).toBe('/app/inbox/s1/crew/scout');
  });

  it('opens a ship that is ready to merge on its merge entry', () => {
    const moored = ship({ status: 'moored', stack_ready: true });
    expect(alertDestination({ target: 'peek', session: 's1', merge: true }, { ...none, ships: [moored] })).toBe('/app/inbox/s1/merge');
  });

  it('opens the Inbox alone when the item, the stop or the ship is gone', () => {
    expect(alertDestination({ target: 'peek', session: 's1', item: 'q-1' }, none)).toBe('/app/inbox');
    expect(alertDestination({ target: 'peek', session: 's1', item: 'q-1' }, { ...none, items: [item('q-2')] })).toBe('/app/inbox');
    expect(alertDestination({ target: 'peek', session: 's1' }, { ...none, ships: [ship({ status: 'underway' })] })).toBe('/app/inbox');
    expect(alertDestination({ target: 'peek', session: 's1' }, none)).toBe('/app/inbox');
    expect(alertDestination({ target: 'peek', session: 's1', merge: true }, { ...none, ships: [ship({ status: 'moored', stack_ready: false })] })).toBe('/app/inbox');
  });

  it('opens an anchored ship’s own view, which the Inbox never lists', () => {
    expect(alertDestination({ target: 'ship', session: 's1' }, { ...none, ships: [ship({ status: 'anchored' })] })).toBe('/app/ships/s1');
    expect(alertDestination({ target: 'ship', session: 's1' }, none)).toBe('/app/inbox');
  });
});

describe('the fleet a tap carries', () => {
  it('is the alert’s own fleet, not the one on show', () => {
    expect(alertSearch({ fleet: 'zulu' })).toBe('?fleet=zulu');
    expect(alertSearch({})).toBe('');
  });
});

describe('the alert message', () => {
  function wired(muted = false) {
    const chimes: number[] = [];
    const toasts = createToasts({ now: () => 1 });
    const handler = createAlertHandler({ toasts, chime: () => chimes.push(1), muted: () => muted });
    return { chimes, toasts, handler };
  }
  const msg = { type: 'alert', name: 'Dory', line: 'needs your approval', tag: 's1', target: 'peek', session: 's1' };

  it('chimes once and shows a toast', () => {
    const { chimes, toasts, handler } = wired();
    handler({ ...msg });
    expect(chimes).toHaveLength(1);
    expect(toasts.list()).toHaveLength(1);
  });

  it('shows the toast and stays quiet when muted', () => {
    const { chimes, toasts, handler } = wired(true);
    handler({ ...msg });
    expect(chimes).toHaveLength(0);
    expect(toasts.list()).toHaveLength(1);
  });

  it('does nothing for the other messages on the socket', () => {
    const { chimes, toasts, handler } = wired();
    handler({ type: 'inbox', items: [{}] });
    handler({ type: 'ships', ships: [] });
    expect(chimes).toHaveLength(0);
    expect(toasts.list()).toHaveLength(0);
  });
});
