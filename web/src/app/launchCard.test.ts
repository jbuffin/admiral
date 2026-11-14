import { afterEach, describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads; the card's ship link comes from it.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', { replaceState: () => {} });
});

import type { Ship } from './api';
import { threadOf, type ChatSnapshot, type LaunchCard } from './chat';
import { fetchLaunchState, launchCardView, promoteLaunch, stopShip, undoLaunch } from './launchCard';
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
  it('says a wall-clock stop in words where it would say anchored', () => {
    const v = launchCardView(item(), { ships: [ship({ status: 'anchored', stopped_by: 'wall_clock:60' })], undone: false });
    expect(v.status).toBe('Stopped at the 60-minute wall clock');
    expect(launchCardView(item(), { ships: [ship({ status: 'anchored', stopped_by: 'other' })], undone: false }).status).toBe('anchored');
  });

  it('offers Stop, Undo and Mute rule for a live ship, with its status and a link to it', () => {
    const v = launchCardView(item(), { ships: [ship({ status: 'needs_you' })], undone: false });
    expect(v.actions).toEqual({ stop: true, undo: true, mute: true, promote: true });
    expect(v.ship).toBe('calm-otter');
    expect(v.shipHref).toBe(shipPath('calm-otter'));
    expect(v.status).toBe('needs_you');
    expect(v.text).toBe('Alice asks for a review.');
  });

  it('offers no Stop for a ship the host no longer lists, and keeps Undo and Mute rule', () => {
    const v = launchCardView(item(), { ships: [ship({ session: 'brisk-heron' })], undone: false });
    expect(v.actions).toEqual({ stop: false, undo: true, mute: true, promote: true });
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

describe('Promote on the launch card', () => {
  const view = (card: Partial<LaunchCard>, o: { undone?: boolean; promotedShip?: string } = {}) =>
    launchCardView(item(card), { ships: [ship()], undone: false, ...o });

  it('is offered on a restricted Draft fix card and on no other', () => {
    expect(view({ restricted: 'draft_fix' }).actions.promote).toBe(true);
    expect(view({ restricted: 'investigate' }).actions.promote).toBe(false);
    for (const level of ['A4', 'A5'] as const) expect(view({ level, restricted: undefined }).actions.promote).toBe(false);
  });

  it('asks first, saying the restricted ship stops and the new one can push and open PRs', () => {
    const c = view({}).promoteConfirm;
    expect(c).toBe('Stops the restricted ship if it’s running and starts a normal ship from its work. That ship can push and open PRs.');
  });

  it('after Promote shows where the work went, with a link to that ship, and no Promote or Undo', () => {
    const v = view({}, { promotedShip: 'swift-heron' });
    expect(v.notice).toBe('Promoted to swift-heron');
    expect(v.promotedShip).toBe('swift-heron');
    expect(v.promotedHref).toBe(shipPath('swift-heron'));
    expect(v.actions.promote).toBe(false);
    expect(v.actions.undo).toBe(false);
  });

  it('is not offered on an undone launch', () => {
    expect(view({}).actions.promote).toBe(true);
    expect(view({}, { undone: true }).actions.promote).toBe(false);
  });

  it('posts to the record’s promote route and passes the new ship or a refusal on', async () => {
    let calls = record(200, { id: 'trig-1', promoted: true, promoted_ship: 'swift-heron' });
    const ok = await promoteLaunch('work', 'r1', 'trig-1');
    expect(calls).toEqual([{ url: '/api/v1/fleets/work/routines/r1/runs/trig-1/promote', method: 'POST' }]);
    expect(ok).toMatchObject({ ok: true, data: { promoted_ship: 'swift-heron' } });
    calls = record(409, { error: 'the global cap of 4 ships is reached' });
    expect(await promoteLaunch('work', 'r1', 'trig-1')).toMatchObject({ ok: false, error: 'the global cap of 4 ships is reached' });
  });

  it('a reload reads the promoted ship, and the undone state, from the one record', async () => {
    const calls = record(200, { id: 'trig-1', promoted: true, promoted_ship: 'swift-heron' });
    expect(await fetchLaunchState('work', 'r1', 'trig-1')).toEqual({ undone: false, promotedShip: 'swift-heron' });
    expect(calls).toEqual([{ url: '/api/v1/fleets/work/routines/r1/runs/trig-1', method: 'GET' }]);
    record(200, { id: 'trig-1', undone: true });
    expect(await fetchLaunchState('work', 'r1', 'trig-1')).toEqual({ undone: true, promotedShip: '' });
    record(404, { error: 'no such record' });
    expect(await fetchLaunchState('work', 'r1', 'trig-1')).toBeUndefined();
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
    expect(await fetchLaunchState('work', 'r1', 'trig-1')).toMatchObject({ undone: true });
    expect(calls).toEqual([{ url: '/api/v1/fleets/work/routines/r1/runs/trig-1', method: 'GET' }]);
    record(200, { id: 'trig-1' });
    expect(await fetchLaunchState('work', 'r1', 'trig-1')).toMatchObject({ undone: false });
    record(404, { error: 'no such record' });
    expect(await fetchLaunchState('work', 'r1', 'trig-1')).toBeUndefined();
  });
});
