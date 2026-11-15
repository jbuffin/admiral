import { describe, expect, it } from 'vitest';
import type { MooredPR, Order, Ship } from './api';
import { mergeHint, moorSheetParts, nextStep, offersMoor, orderRowAction, prChips, prLabel, reReviewPlace, reviews, rowButtons, shipsOnOrder, staleNote } from './moored';
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

const order = (over: Partial<Order> = {}): Order => ({ id: 'acme/api#5', title: 'An order', ...over });

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
    const s = ship({ status: 'anchored', launched: '2026-11-11T11:48:00Z' });
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
  it('leaves Wake on for a ship with no valid resume text, since a hand wake starts it bare', () => {
    expect(rowButtons({ no_resume: true } as Parameters<typeof rowButtons>[0], false)).toEqual({ wake: false, others: false, note: '' });
  });
  it('shows the waiting line as the note and leaves Wake and the others on', () => {
    expect(rowButtons({ waiting: 'Waiting for room under the cap' }, false)).toEqual({ wake: false, others: false, note: 'Waiting for room under the cap' });
    expect(rowButtons({ waiting: "Can't wake: no dock" }, true)).toEqual({ wake: true, others: true, note: "Can't wake: no dock" });
  });
  it('does not look at a stale poll, since waking needs no GitHub', () => {
    expect(rowButtons({ stale: true } as Parameters<typeof rowButtons>[0], false).wake).toBe(false);
  });
});

describe('mergeHint', () => {
  it('names the repo\'s method beside Merge', () => {
    expect(mergeHint('squash')).toBe('squash');
    expect(mergeHint('rebase')).toBe('rebase');
    expect(mergeHint('merge')).toBe('merge commit');
    expect(mergeHint(undefined), 'nothing until the method is known').toBe('');
    expect(mergeHint('squash', true)).toBe('squash · whole stack');
    expect(mergeHint(undefined, true), 'a stack says so before the method is known').toBe('whole stack');
  });
});

describe('shipsOnOrder', () => {
  const url = 'https://github.com/acme/api/pull/7';
  const issue = 'https://github.com/acme/api/issues/5';
  const on = (name: string, over: Partial<Ship> = {}) => ship({ name, session: `s-${name}`, status: 'underway', ...over });
  const names = (ships: Ship[]) => ships.map((s) => s.name);

  it.each([
    ['its url', { url: issue }],
    ['one of its issues', { url: 'https://github.com/acme/api/issues/4', issues: ['https://github.com/acme/api/issues/4', issue] }],
    ['one of its PRs', { prs: [pr({ url: 'https://github.com/acme/api/pull/6' }), pr({ url: issue })] }],
    ['its link', { link: issue }],
  ])('finds a ship by %s', (_how, over) => {
    expect(names(shipsOnOrder([on('a', over)], order({ url: issue })))).toEqual(['a']);
  });

  it.each([
    ['case', 'https://GitHub.com/Acme/API/issues/5'],
    ['a trailing slash', `${issue}/`],
  ])('ignores %s in the order URL', (_how, u) => {
    expect(names(shipsOnOrder([on('a', { url: issue })], order({ url: u })))).toEqual(['a']);
  });

  it('ignores case and a trailing slash in the ship URLs', () => {
    expect(names(shipsOnOrder([on('a', { prs: [pr({ url: `${url.toUpperCase()}/` })] })], order({ url })))).toEqual(['a']);
  });

  it('finds the ship for any layer of its stack', () => {
    const stacker = on('stacker', { prs: [pr({ url: 'https://github.com/acme/api/pull/6' }), pr({ url })] });
    expect(names(shipsOnOrder([stacker], order({ url: 'https://github.com/acme/api/pull/6' })))).toEqual(['stacker']);
    expect(names(shipsOnOrder([stacker], order({ url })))).toEqual(['stacker']);
  });

  it.each(['underway', 'needs_you', 'moored'] as const)('counts a %s ship', (status) => {
    expect(names(shipsOnOrder([on('a', { status, url: issue })], order({ url: issue })))).toEqual(['a']);
  });

  it.each(['anchored', 'sailing_off'] as const)('does not count an %s ship', (status) => {
    expect(shipsOnOrder([on('a', { status, url: issue, prs: [pr({ url: issue })], link: issue })], order({ url: issue }))).toEqual([]);
  });

  it('counts a ship of any fleet', () => {
    expect(names(shipsOnOrder([on('a', { fleet: 'yankee', url: issue })], order({ url: issue })))).toEqual(['a']);
  });

  it('finds nothing for an order no ship is on, or an order with no URL', () => {
    expect(shipsOnOrder([on('other', { url: 'https://github.com/acme/api/issues/6' }), ship()], order({ url: issue }))).toEqual([]);
    expect(shipsOnOrder([], order({ url: issue }))).toEqual([]);
    expect(shipsOnOrder([on('a', { url: issue })], order({ url: undefined }))).toEqual([]);
  });

  describe('which ship comes first', () => {
    const base = { url: issue };
    const mooredAgain = on('again', { ...base, status: 'moored', prs: [pr({ url: issue, reviewing: true, responded: true })] });
    const needs = on('needs', { ...base, status: 'needs_you' });
    const under = on('under', { ...base, status: 'underway' });
    const moored = on('moored', { ...base, status: 'moored' });

    it('puts a moored ship whose PR needs you again, then needs_you, then underway, then moored', () => {
      expect(names(shipsOnOrder([moored, under, needs, mooredAgain], order({ url: issue })))).toEqual(['again', 'needs', 'under', 'moored']);
    });

    it('treats an own-PR ship with new feedback as needing you again', () => {
      const wake = on('wake', { ...base, status: 'moored', prs: [pr({ url: issue, answer: '/commodore answer', feedback: 2 })] });
      expect(names(shipsOnOrder([needs, wake], order({ url: issue })))).toEqual(['wake', 'needs']);
    });

    it('treats a moored reviewing ship as needing you again when the order is review-requested', () => {
      const reviewing = on('reviewing', { ...base, status: 'moored', prs: [pr({ url: issue, reviewing: true })] });
      const o = order({ url: issue, review_requested_at: '2026-11-14T10:00:00Z', reviewed_since_push: false });
      expect(names(shipsOnOrder([needs, reviewing], o))).toEqual(['reviewing', 'needs']);
      expect(names(shipsOnOrder([needs, reviewing], order({ url: issue })))).toEqual(['needs', 'reviewing']);
    });

    it('puts the newest launch first within a status', () => {
      const older = on('older', { ...base, launched: '2026-11-14T08:00:00Z' });
      const newer = on('newer', { ...base, launched: '2026-11-14T09:00:00Z' });
      expect(names(shipsOnOrder([older, newer], order({ url: issue })))).toEqual(['newer', 'older']);
      expect(names(shipsOnOrder([newer, older], order({ url: issue })))).toEqual(['newer', 'older']);
    });
  });
});

describe('orderRowAction', () => {
  const url = 'https://github.com/acme/api/pull/42';
  const issue = 'https://github.com/acme/api/issues/5';
  const stack = 'https://github.com/acme/api/stack/3';
  const requested = { review_requested_at: '2026-11-14T10:00:00Z', reviewed_since_push: false };
  const on = (over: Partial<Ship> = {}) => ship({ name: 'sloop', session: 's-sloop', ...over });
  const act = (ships: Ship[], o: Order) => orderRowAction(ships, o);
  const shipAction = (a: ReturnType<typeof orderRowAction>) => {
    if (a.kind !== 'ship') throw new Error(`expected a ship action, got ${a.kind}`);
    return a;
  };

  it('is the launch arrow with no ship on the order, or only an anchored one', () => {
    expect(act([], order({ url: issue }))).toEqual({ kind: 'launch' });
    expect(act([on({ status: 'anchored', url: issue })], order({ url: issue }))).toEqual({ kind: 'launch' });
    expect(act([on({ status: 'sailing_off', url: issue })], order({ url: issue }))).toEqual({ kind: 'launch' });
  });

  it('is a pill with the ship and its status word for an underway ship, with no further action', () => {
    const s = on({ status: 'underway', url: issue });
    const a = shipAction(act([s], order({ url: issue })));
    expect(a).toMatchObject({ ship: s, word: 'Underway', needsYou: false, again: null });
  });

  it('colours the pill of a needs_you ship', () => {
    const a = shipAction(act([on({ status: 'needs_you', url: issue })], order({ url: issue })));
    expect(a).toMatchObject({ word: 'Needs you', needsYou: true, again: null });
  });

  it('is a pill alone for a moored ship that has nothing new', () => {
    const own = on({ url: issue, prs: [pr({ url: issue, answer: '/commodore answer' })] });
    expect(shipAction(act([own], order({ url: issue })))).toMatchObject({ word: 'Moored', needsYou: false, again: null });
    const reviewing = on({ prs: [pr({ url, reviewing: true })] });
    expect(shipAction(act([reviewing], order({ url, ...requested, reviewed_since_push: true }))).again).toBeNull();
  });

  describe('Re-review', () => {
    it('shows for a reviewing ship whose PR is responded, with that PR url as served', () => {
      const served = 'https://github.com/Acme/API/pull/42/';
      const s = on({ prs: [pr({ url: served, reviewing: true, responded: true })] });
      expect(shipAction(act([s], order({ url, reviewed_since_push: true }))).again).toEqual({ kind: 'rereview', url: served });
    });

    it('sends the stack url for a stack-url ship', () => {
      const s = on({ url: stack, prs: [pr({ url, reviewing: true, responded: true }), pr({ url: 'https://github.com/acme/api/pull/43', reviewing: true })] });
      expect(shipAction(act([s], order({ url, reviewed_since_push: true }))).again).toEqual({ kind: 'rereview', url: stack });
    });

    it('shows when the order is review-requested, with the matched PR url exactly as served, never the order url', () => {
      const served = 'https://github.com/Acme/API/pull/42/';
      const s = on({ prs: [pr({ url: 'https://github.com/acme/api/pull/41', reviewing: true }), pr({ url: served, reviewing: true })] });
      const a = shipAction(act([s], order({ url, ...requested })));
      expect(a.again).toEqual({ kind: 'rereview', url: served });
      expect(a.again).not.toMatchObject({ url });
    });

    it('sends the ship url when the review-requested order is on a stack-url ship', () => {
      const s = on({ url: stack, prs: [pr({ url, reviewing: true }), pr({ url: 'https://github.com/acme/api/pull/43', reviewing: true })] });
      expect(shipAction(act([s], order({ url: stack, ...requested }))).again).toEqual({ kind: 'rereview', url: stack });
    });

    it('prefers the responded PR url over the matched one', () => {
      const s = on({ prs: [pr({ url, reviewing: true }), pr({ url: 'https://github.com/acme/api/pull/43', reviewing: true, responded: true })] });
      expect(shipAction(act([s], order({ url, ...requested }))).again).toEqual({ kind: 'rereview', url: 'https://github.com/acme/api/pull/43' });
    });

    it.each([
      ['you reviewed since the push', { review_requested_at: '2026-11-14T10:00:00Z', reviewed_since_push: true }],
      ['no review is requested', { reviewed_since_push: false }],
    ])('does not show when %s and no PR is responded', (_why, over) => {
      const s = on({ prs: [pr({ url, reviewing: true })] });
      expect(shipAction(act([s], order({ url, ...over }))).again).toBeNull();
    });

    it('treats a missing reviewed_since_push as not reviewed', () => {
      const s = on({ prs: [pr({ url, reviewing: true })] });
      expect(shipAction(act([s], order({ url, review_requested_at: '2026-11-14T10:00:00Z' }))).again).toEqual({ kind: 'rereview', url });
    });

    it('does not show for a ship that opened its own PR', () => {
      const s = on({ prs: [pr({ url })] });
      expect(shipAction(act([s], order({ url, ...requested }))).again).toBeNull();
    });

    it('does not show for a live ship', () => {
      const s = on({ status: 'underway', prs: [pr({ url, reviewing: true, responded: true })] });
      expect(shipAction(act([s], order({ url, ...requested }))).again).toBeNull();
    });
  });

  describe('Wake', () => {
    const feedback = { answer: '/commodore answer', feedback: 1 };

    it('shows for a moored ship with new feedback on its own open PR, sending that PR url as served', () => {
      const served = 'https://github.com/Acme/API/pull/42/';
      const s = on({ prs: [pr({ url: served, ...feedback })] });
      expect(shipAction(act([s], order({ url }))).again).toEqual({ kind: 'wake', pr: served });
    });

    it('shows for an issue order whose ship owns the PR', () => {
      const s = on({ url: issue, prs: [pr({ url, ...feedback })] });
      expect(shipAction(act([s], order({ url: issue }))).again).toEqual({ kind: 'wake', pr: url });
    });

    it("prefers the order's PR when several have feedback", () => {
      const s = on({ prs: [pr({ url: 'https://github.com/acme/api/pull/41', ...feedback }), pr({ url, ...feedback })] });
      expect(shipAction(act([s], order({ url }))).again).toEqual({ kind: 'wake', pr: url });
    });

    it('falls back to the first own PR with feedback when the order names none of them', () => {
      const s = on({ url: issue, prs: [pr({ url: 'https://github.com/acme/api/pull/41' }), pr({ url: 'https://github.com/acme/api/pull/43', ...feedback })] });
      expect(shipAction(act([s], order({ url: issue }))).again).toEqual({ kind: 'wake', pr: 'https://github.com/acme/api/pull/43' });
    });

    it.each([
      ['a merged PR', { state: 'MERGED' }],
      ['a reviewed PR', { reviewing: true }],
      ['no new feedback', { feedback: 0 }],
      ['no answer command', { answer: '' }],
    ])('does not show for %s', (_why, over) => {
      const s = on({ prs: [pr({ url, ...feedback, ...over })] });
      expect(shipAction(act([s], order({ url }))).again).toBeNull();
    });

    it('does not show for a live ship', () => {
      const s = on({ status: 'underway', prs: [pr({ url, ...feedback })] });
      expect(shipAction(act([s], order({ url }))).again).toBeNull();
    });
  });

  it('acts on the ship that comes first', () => {
    const under = on({ name: 'under', session: 's-under', status: 'underway', url: issue });
    const needs = on({ name: 'needs', session: 's-needs', status: 'needs_you', url: issue });
    expect(shipAction(act([under, needs], order({ url: issue }))).ship.name).toBe('needs');
  });
});

describe('nextStep', () => {
  it('is ready to merge when the whole stack is, whatever else the ship says', () => {
    expect(nextStep(ship({ stack_ready: true, waiting: 'Waiting for room under the cap', prs: [pr({ checks: 'FAILURE' })] })).ready).toBe(true);
  });

  it('gives the server\'s waiting text over what the chips imply, and a missing resume text changes nothing', () => {
    const failing = [pr({ checks: 'FAILURE' })];
    expect(nextStep(ship({ waiting: 'Waiting for room under the cap', prs: failing }))).toEqual({ ready: false, text: 'Waiting for room under the cap' });
    expect(nextStep(ship({ no_resume: true, prs: failing }))).toEqual(nextStep(ship({ prs: failing })));
  });

  it('says what the PR itself waits on when the ship says nothing', () => {
    expect(nextStep(ship({ prs: [pr({ review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE' })] }))).toEqual({ ready: false, text: 'Waiting on review' });
    expect(nextStep(ship({ prs: [pr({ review: 'APPROVED', checks: 'FAILURE', mergeable: 'MERGEABLE' })] }))).toEqual({ ready: false, text: 'Checks failing' });
    expect(nextStep(ship({ prs: [pr({ review: 'APPROVED', checks: 'SUCCESS', mergeable: 'CONFLICTING', conflicts: true })] }))).toEqual({ ready: false, text: 'Has conflicts' });
  });

  it('names the first thing that applies, in a fixed order', () => {
    const bad = pr({ mergeable: 'CONFLICTING', conflicts: true, checks: 'FAILURE', review: 'CHANGES_REQUESTED' });
    const text = (over: Partial<MooredPR>) => nextStep(ship({ prs: [{ ...bad, ...over }] })).text;
    expect(text({})).toBe('Has conflicts');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false })).toBe('Checks failing');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false, checks: 'ERROR' })).toBe('Checks failing');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false, checks: 'PENDING' })).toBe('Changes requested');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false, checks: 'PENDING', review: 'APPROVED' })).toBe('Checks pending');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false, checks: 'EXPECTED', review: 'REVIEW_REQUIRED' })).toBe('Checks pending');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false, checks: 'SUCCESS', review: '' })).toBe('Waiting on review');
    expect(text({ mergeable: 'MERGEABLE', conflicts: false, checks: 'SUCCESS', review: 'APPROVED' }), 'nothing to say').toBe('');
  });

  it('reads the first layer of a stack that is not ready', () => {
    const ready = pr({ url: 'https://github.com/acme/api/pull/6', ready: true });
    const failing = pr({ url: 'https://github.com/acme/api/pull/7', checks: 'FAILURE' });
    const conflicting = pr({ url: 'https://github.com/acme/api/pull/8', conflicts: true, mergeable: 'CONFLICTING' });
    expect(nextStep(ship({ prs: [ready, failing, conflicting] })).text).toBe('Checks failing');
    expect(nextStep(ship({ prs: [ready, conflicting, failing] })).text).toBe('Has conflicts');
  });
});

describe('reviews', () => {
  it('is true when any PR the ship carries is one it reviews', () => {
    expect(reviews(ship({ prs: [pr({ reviewing: true })] }))).toBe(true);
    expect(reviews(ship({ prs: [pr(), pr({ url: 'https://github.com/acme/api/pull/8', reviewing: true })] }))).toBe(true);
  });

  it('is false for the ship\'s own PRs, no PRs and a ship with no list', () => {
    expect(reviews(ship({ prs: [pr(), pr({ reviewing: false })] }))).toBe(false);
    expect(reviews(ship({ prs: [] }))).toBe(false);
    expect(reviews(ship({ prs: undefined }))).toBe(false);
  });
});

describe('nextStep for a reviewing ship', () => {
  const rev = (over: Partial<MooredPR> = {}) => pr({ reviewing: true, author: 'dana', ...over });

  it('is never ready to merge, though the server serves stack_ready', () => {
    expect(nextStep(ship({ stack_ready: true, prs: [rev({ ready: true })] })).ready).toBe(false);
  });
});

describe('reReviewPlace', () => {
  const stackUrl = 'https://github.com/acme/api/stack/9';
  const layer = (n: number, over: Partial<MooredPR> = {}) => pr({ url: `https://github.com/acme/api/pull/${n}`, reviewing: true, author: 'dana', ...over });

  it('puts Re-review on the responded PR with the PR\'s own URL for a PR-URL ship', () => {
    const prUrl = 'https://github.com/acme/api/pull/7';
    expect(reReviewPlace(ship({ url: prUrl, prs: [layer(7, { responded: true })] }))).toEqual({ place: 'pr', url: prUrl });
  });

  it('puts Re-review stack on the stack bar with the stack URL for a reviewed stack of three layers', () => {
    const prs = [layer(6), layer(7, { responded: true }), layer(8)];
    expect(reReviewPlace(ship({ url: stackUrl, prs }))).toEqual({ place: 'stack', url: stackUrl });
  });

  it('puts Re-review stack on the bar of a mixed stack with one reviewed layer', () => {
    const prs = [pr({ url: 'https://github.com/acme/api/pull/5' }), layer(6, { responded: true })];
    expect(reReviewPlace(ship({ url: stackUrl, prs }))).toEqual({ place: 'stack', url: stackUrl });
  });

  it('puts Re-review on the PR, with the stack URL, for a stack that has one layer left', () => {
    expect(reReviewPlace(ship({ url: stackUrl, prs: [layer(6, { responded: true })] }))).toEqual({ place: 'pr', url: stackUrl });
  });

  it('is null while the author has not responded, and for a ship that is not moored', () => {
    expect(reReviewPlace(ship({ url: stackUrl, prs: [layer(6), layer(7)] }))).toBeNull();
    expect(reReviewPlace(ship({ url: stackUrl, status: 'underway', prs: [layer(6, { responded: true }), layer(7)] }))).toBeNull();
  });
});

describe('offersMoor', () => {
  const live = (over: Partial<Ship> = {}) => ship({ status: 'needs_you', ...over });

  it('is true for a live ship carrying an open PR it did not review', () => {
    expect(offersMoor(live({ prs: [pr()] }))).toBe(true);
    expect(offersMoor(live({ status: 'underway', prs: [pr()] }))).toBe(true);
  });

  it('is false for a moored ship, which gets Merge instead', () => {
    expect(offersMoor(ship({ prs: [pr()] }))).toBe(false);
  });

  it('is false for a ship that reviews the PR', () => {
    expect(offersMoor(live({ prs: [pr({ reviewing: true })] }))).toBe(false);
  });

  it('is false when every PR is merged or closed, and true while one is still open', () => {
    expect(offersMoor(live({ prs: [pr({ state: 'MERGED' }), pr({ state: 'CLOSED' })] }))).toBe(false);
    expect(offersMoor(live({ prs: [pr({ state: 'MERGED' }), pr({ url: 'https://github.com/acme/api/pull/8' })] }))).toBe(true);
  });

  it('is false for a ship returning to port', () => {
    expect(offersMoor(live({ prs: [pr()], returning: true }))).toBe(false);
  });

  it('is false for a ship carrying no PR', () => {
    expect(offersMoor(live({ prs: [] }))).toBe(false);
    expect(offersMoor(live({ prs: undefined }))).toBe(false);
  });
});

describe('moorSheetParts', () => {
  const two = [pr(), pr({ url: 'https://github.com/acme/api/pull/8' }), pr({ url: 'https://github.com/acme/api/pull/9', state: 'MERGED' })];

  it('lists the open PRs it carries, not the merged ones', () => {
    const parts = moorSheetParts(ship({ status: 'needs_you', name: 'kelp', prs: two }));
    expect(parts.urls).toEqual(['https://github.com/acme/api/pull/7', 'https://github.com/acme/api/pull/8']);
  });

  it('says "the PR" for one PR and "the PRs" for more', () => {
    expect(moorSheetParts(ship({ status: 'needs_you', prs: [pr()] })).explain).toContain('keeps the PR, so you can merge it here');
    expect(moorSheetParts(ship({ status: 'needs_you', prs: two })).explain).toContain('keeps the PRs, so you can merge them here');
  });

  it('says the turn is cut off only while the ship is underway', () => {
    expect(moorSheetParts(ship({ status: 'underway', name: 'kelp', prs: two })).explain).toContain('kelp is mid-turn: the turn is cut off.');
    expect(moorSheetParts(ship({ status: 'needs_you', prs: two })).explain).not.toMatch(/cut off/i);
  });
});
