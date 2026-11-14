import { afterEach, describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads; the card's ship link comes from it.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', { replaceState: () => {} });
});

import type { Ship } from './api';
import { threadOf, type ChatSnapshot, type LaunchCard } from './chat';
import { fetchLaunchUndone, launchCardView, stopShip, undoLaunch } from './launchCard';
import { shipPath } from './router';

const base: LaunchCard = {
  routine: 'r1', routine_name: 'Review watch', tier: 'T1', sender: 'alice', dedupe_key: 'gh:acme/api#7', mute_by: ['sender', 'dedupe_key'],
  link: 'https://github.com/acme/api/pull/7', ship: 'calm-otter', level: 'A3', restricted: 'draft_fix',
};
const item = (card: Partial<LaunchCard> = {}) => ({ text: 'Alice asks for a review.', card: { ...base, ...card } });
const ship = (over: Partial<Ship> = {}) => ({ session: 'calm-otter', status: 'underway', ...over }) as Ship;

type Call = { url: string; method?: string };
const record = (status = 200, body: unknown = {}) => {
  const calls: Call[] = [];
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    calls.push({ url: String(url), method: init?.method });
    return new Response(JSON.stringify(body), { status });
  });
  return calls;
};
afterEach(() => vi.unstubAllGlobals());

describe('the launch card in the thread', () => {
  it('comes from a launch_card entry as a launch item that keeps its fields', () => {
    const s: ChatSnapshot = { fleet: 'work', running: true, state: 'idle', questions: [], messages: [{ id: 'trig-1', kind: 'launch_card', text: 'Alice asks for a review.', at: '2026-01-02T03:04:05Z', card: base }] };
    const t = threadOf(s);
    expect(t).toHaveLength(1);
    expect(t[0]).toMatchObject({ kind: 'launch', id: 'trig-1', text: 'Alice asks for a review.', card: { ship: 'calm-otter', level: 'A3' } });
  });
});

describe('the launch card view', () => {
  it('offers Stop, Undo and Mute rule for a live ship, with its status and a link to it', () => {
    const v = launchCardView(item(), { ships: [ship({ status: 'needs_you' })], undone: false });
    expect(v.actions).toEqual({ stop: true, undo: true, mute: true });
    expect(v.ship).toBe('calm-otter');
    expect(v.shipHref).toBe(shipPath('calm-otter'));
    expect(v.status).toBe('needs_you');
    expect(v.text).toBe('Alice asks for a review.');
  });

  it('offers no Stop for a ship the host no longer lists, and keeps Undo and Mute rule', () => {
    const v = launchCardView(item(), { ships: [ship({ session: 'brisk-heron' })], undone: false });
    expect(v.actions).toEqual({ stop: false, undo: true, mute: true });
    expect(v.status).toBe('');
  });

  it('names the routine, the level and the restricted profile of an A3 card, marked untrusted', () => {
    expect(launchCardView(item({ restricted: 'draft_fix' }), { ships: [], undone: false }).eyebrow).toBe('Review watch · A3 · Restricted · Draft fix · Untrusted');
    expect(launchCardView(item({ restricted: 'investigate' }), { ships: [], undone: false }).eyebrow).toBe('Review watch · A3 · Restricted · Investigate · Untrusted');
  });

  it('names the level of an A4 or A5 card and no restricted profile', () => {
    for (const level of ['A4', 'A5'] as const) {
      const eyebrow = launchCardView(item({ level, restricted: undefined }), { ships: [], undone: false }).eyebrow;
      expect(eyebrow).toBe(`Review watch · ${level} · Untrusted`);
    }
  });

  it('shows an undone launch as undone, with no Stop or Undo, even while the host still lists the ship', () => {
    const v = launchCardView(item(), { ships: [ship()], undone: true });
    expect(v.undone).toBe(true);
    expect(v.actions.stop).toBe(false);
    expect(v.actions.undo).toBe(false);
    expect(v.notice.toLowerCase()).toContain('undone');
  });

  it('asks before an Undo that deletes the ship’s local work, and on an A4 or A5 card says pushed branches and PRs stay', () => {
    const a3 = launchCardView(item(), { ships: [ship()], undone: false }).undoConfirm;
    expect(a3.toLowerCase()).toContain('delete');
    for (const level of ['A4', 'A5'] as const) {
      const c = launchCardView(item({ level, restricted: undefined }), { ships: [ship()], undone: false }).undoConfirm;
      expect(c.toLowerCase()).toContain('delete');
      expect(c.toLowerCase()).toContain('pushed');
      expect(c).toContain('PR');
    }
  });
});

describe('the launch card’s calls', () => {
  it('Stop posts to the ship’s stop route', async () => {
    const calls = record();
    expect((await stopShip('calm-otter')).ok).toBe(true);
    expect(calls).toEqual([{ url: '/api/v1/ships/calm-otter/stop', method: 'POST' }]);
  });

  it('Undo posts to the trigger record’s undo route and passes a refusal on', async () => {
    const calls = record();
    expect((await undoLaunch('work', 'r1', 'trig-1')).ok).toBe(true);
    expect(calls).toEqual([{ url: '/api/v1/fleets/work/routines/r1/runs/trig-1/undo', method: 'POST' }]);
    record(500, { error: 'could not remove the clone' });
    expect(await undoLaunch('work', 'r1', 'trig-1')).toMatchObject({ ok: false, error: 'could not remove the clone' });
  });

  it('a reload reads the undone state from the one record', async () => {
    const calls = record(200, { id: 'trig-1', undone: true });
    expect(await fetchLaunchUndone('work', 'r1', 'trig-1')).toBe(true);
    expect(calls).toEqual([{ url: '/api/v1/fleets/work/routines/r1/runs/trig-1', method: 'GET' }]);
    record(200, { id: 'trig-1' });
    expect(await fetchLaunchUndone('work', 'r1', 'trig-1')).toBe(false);
    record(404, { error: 'no such record' });
    expect(await fetchLaunchUndone('work', 'r1', 'trig-1')).toBeUndefined();
  });
});
