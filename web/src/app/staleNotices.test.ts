import { describe, expect, it, vi } from 'vitest';
import type { Ship } from './api';
import type { InboxItem } from './inbox';
import { staleNotices } from './staleNotices';

// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

const ship = (extra: Partial<Ship>) => ({ session: 's1', name: 'Dory', fleet: 'a', status: 'needs_you', reason: 'permission:Bash', crew: [], ...extra }) as unknown as Ship;
const item = (id: string) => ({ version: 1, id, session: 's1', kind: 'question', created: '2026-10-22T10:00:00Z', title: 'Which port?' }) as InboxItem;
const note = (tag: string, data: Record<string, unknown>) => ({ tag, data });

describe('which shown notifications to close when the app opens', () => {
  it('closes only those whose entry is gone, matching on the payload and never the tag', () => {
    // The item's and the crew boat's tags have the same shape; the item is gone and the crew boat still needs you.
    const crew = [{ name: 'abc', status: 'needs_you', reason: 'question' }] as Ship['crew'];
    const now = { ships: [ship({ crew })], items: [] as InboxItem[] };
    const gone = note('s1/abc', { target: 'peek', session: 's1', item: 'abc' });
    const stays = note('s1/abc', { target: 'peek', session: 's1', crew: 'abc' });
    expect(staleNotices([gone, stays], now)).toEqual([gone]);
  });

  it('keeps one whose item is still in the Inbox, and the ship stop that still waits', () => {
    const now = { ships: [ship({})], items: [item('q-1')] };
    const a = note('s1/q-1', { target: 'peek', session: 's1', item: 'q-1' });
    const b = note('s1', { target: 'peek', session: 's1' });
    expect(staleNotices([a, b], now)).toEqual([]);
  });

  it('closes a ship stop once the ship no longer needs you', () => {
    const answered = note('s1', { target: 'peek', session: 's1' });
    expect(staleNotices([answered], { ships: [ship({ status: 'underway' })], items: [] })).toEqual([answered]);
  });

  it('closes none while nothing has been shown', () => {
    expect(staleNotices([], { ships: [], items: [] })).toEqual([]);
  });
});
