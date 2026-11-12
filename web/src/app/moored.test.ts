import { describe, expect, it } from 'vitest';
import type { MooredPR, Ship } from './api';
import { NO_RESUME_NOTE, prChips, prLabel, rowButtons, staleNote } from './moored';
import { GROUP_TITLES, groupShips, shipAge, shipRowAge } from './shipList';

const pr = (over: Partial<MooredPR> = {}): MooredPR => ({
  url: 'https://github.com/acme/api/pull/7',
  state: 'OPEN',
  review: '',
  checks: '',
  mergeable: '',
  conflicts: false,
  ...over,
});

const ship = (over: Partial<Ship> = {}): Ship => ({
  session: 's-m',
  name: 'm',
  fleet: 'zulu',
  harness: 'claude',
  status: 'moored',
  order_id: '',
  title: '',
  tail: '',
  fleet_color: '',
  launched: '',
  crew_live: 0,
  dir: '',
  repo: '',
  exit_code: null,
  exit_signal: null,
  crew: [],
  moored_at: '',
  polled_at: '',
  stale: false,
  prs: [],
  ...over,
});

describe('the Moored group', () => {
  it('comes after Underway and before Anchored, titled Moored', () => {
    const groups = groupShips(
      [ship({ name: 'a', status: 'anchored' }), ship({ name: 'm' }), ship({ name: 'u', status: 'underway' }), ship({ name: 'n', status: 'needs_you' })],
      'zulu',
    );
    expect(groups.map((g) => g.status)).toEqual(['needs_you', 'underway', 'moored', 'anchored']);
    expect(GROUP_TITLES.moored).toBe('Moored');
    expect(groups[2].ships.map((s) => s.name)).toEqual(['m']);
  });

  it('holds only the active fleet\'s moored ships', () => {
    const list = [ship({ name: 'mine' }), ship({ name: 'theirs', fleet: 'alpha' })];
    expect(groupShips(list, 'zulu')[0].ships.map((s) => s.name)).toEqual(['mine']);
  });
});

describe('prChips', () => {
  const texts = (p: MooredPR) => prChips(p).map((c) => [c.text, c.tone]);

  it('says each review decision', () => {
    expect(texts(pr({ review: 'APPROVED' }))).toEqual([['Approved', 'ok']]);
    expect(texts(pr({ review: 'CHANGES_REQUESTED' }))).toEqual([['Changes requested', 'err']]);
    expect(texts(pr({ review: 'REVIEW_REQUIRED' }))).toEqual([['Review required', 'muted']]);
  });

  it('says each checks state, ERROR as failing and EXPECTED as pending', () => {
    expect(texts(pr({ checks: 'SUCCESS' }))).toEqual([['Checks passing', 'ok']]);
    expect(texts(pr({ checks: 'FAILURE' }))).toEqual([['Checks failing', 'err']]);
    expect(texts(pr({ checks: 'ERROR' }))).toEqual([['Checks failing', 'err']]);
    expect(texts(pr({ checks: 'PENDING' }))).toEqual([['Checks pending', 'muted']]);
    expect(texts(pr({ checks: 'EXPECTED' }))).toEqual([['Checks pending', 'muted']]);
  });

  it('gives one merge chip: Mergeable, Conflicts, or none when unknown', () => {
    expect(texts(pr({ mergeable: 'MERGEABLE' }))).toEqual([['Mergeable', 'ok']]);
    expect(texts(pr({ mergeable: 'CONFLICTING', conflicts: true }))).toEqual([['Conflicts', 'err']]);
    expect(texts(pr({ mergeable: 'UNKNOWN' }))).toEqual([]);
  });

  it('orders review, checks, merge', () => {
    expect(texts(pr({ review: 'APPROVED', checks: 'FAILURE', mergeable: 'CONFLICTING', conflicts: true }))).toEqual([
      ['Approved', 'ok'],
      ['Checks failing', 'err'],
      ['Conflicts', 'err'],
    ]);
  });

  it('shows no chip for a missing or unknown value, and none before the first poll', () => {
    expect(prChips(pr())).toEqual([]);
    expect(prChips({ url: 'https://github.com/acme/api/pull/7', state: 'OPEN' } as MooredPR)).toEqual([]);
    expect(prChips(pr({ review: 'SOMETHING_NEW', checks: 'WEIRD', mergeable: 'ODD' }))).toEqual([]);
  });

  it('shows only the state of a merged or closed PR', () => {
    const rest = { review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' };
    expect(texts(pr({ state: 'MERGED', ...rest }))).toEqual([['Merged', 'muted']]);
    expect(texts(pr({ state: 'CLOSED', ...rest }))).toEqual([['Closed', 'muted']]);
  });
});

describe('prLabel', () => {
  it('is repo#number', () => {
    expect(prLabel('https://github.com/acme/api/pull/812')).toBe('api#812');
  });
});

describe('how long a moored ship has waited', () => {
  const now = Date.parse('2026-11-11T12:00:00Z');
  it('counts from moored_at, since launched is empty', () => {
    const s = ship({ launched: '', moored_at: '2026-11-11T09:00:00Z' });
    expect(shipRowAge(s, now)).toBe('3h');
  });

  it('counts a ship that is not moored from its launch', () => {
    const s = ship({ status: 'underway', launched: '2026-11-11T11:48:00Z' });
    expect(shipRowAge(s, now)).toBe(shipAge('2026-11-11T11:48:00Z', now));
    expect(shipRowAge(s, now)).toBe('12m');
  });
});

describe('staleNote', () => {
  const now = Date.parse('2026-11-11T12:00:00Z');
  it('says the poll failed and when the last good one ran, only while stale', () => {
    expect(staleNote(ship({ stale: true, polled_at: '2026-11-11T11:20:00Z' }), now)).toBe('Poll failed · last checked 40m ago');
    expect(staleNote(ship({ stale: true, polled_at: '2026-11-11T11:59:40Z' }), now)).toBe('Poll failed · last checked under a minute ago');
    expect(staleNote(ship({ stale: false, polled_at: '2026-11-11T11:20:00Z' }), now)).toBe('');
  });

  it('just says the poll failed when no poll ever answered', () => {
    expect(staleNote(ship({ stale: true, polled_at: '' }), now)).toBe('Poll failed');
  });
});

describe('rowButtons', () => {
  it('leaves every button on for a ship that can be woken', () => {
    expect(rowButtons({}, false)).toEqual({ wake: false, others: false, note: '' });
  });
  it('turns every button off while a call is out, and back on once an error ends it', () => {
    expect(rowButtons({}, true)).toEqual({ wake: true, others: true, note: '' });
    expect(rowButtons({}, false).wake).toBe(false);
  });
  it('turns Wake off with the reason for a ship with no valid resume text, and leaves the others on', () => {
    expect(rowButtons({ no_resume: true }, false)).toEqual({ wake: true, others: false, note: NO_RESUME_NOTE });
  });
  it('shows the waiting line as the note and leaves Wake and the others on', () => {
    expect(rowButtons({ waiting: 'Waiting for room under the cap' }, false)).toEqual({ wake: false, others: false, note: 'Waiting for room under the cap' });
    expect(rowButtons({ waiting: "Can't wake: no dock" }, true)).toEqual({ wake: true, others: true, note: "Can't wake: no dock" });
  });
  it('does not look at a stale poll, since waking needs no GitHub', () => {
    expect(rowButtons({ stale: true } as Parameters<typeof rowButtons>[0], false).wake).toBe(false);
  });
});
