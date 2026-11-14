import { describe, expect, it } from 'vitest';
import type { MooredPR, Ship } from './api';
import { actingWhile, chooseStackDetail, showAnswerAll, defaultPR, feedbackBarText, headerChips, openCount, orderPRs, prDot, prSummary, stackBarText, stackedPRs, reviewMark, reviewStackBar } from './prTab';

const URL = (n: number) => `https://github.com/acme/api/pull/${n}`;
const pr = (n: number, over: Partial<MooredPR> = {}): MooredPR => ({
  url: URL(n),
  state: 'OPEN',
  review: '',
  checks: '',
  mergeable: '',
  conflicts: false,
  ...over,
});
const ready = (n: number, over: Partial<MooredPR> = {}) => pr(n, { review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE', ready: true, ...over });
const entries = (...numbers: number[]) => ({ entries: numbers.map((n, i) => ({ position: i + 1, number: n, url: URL(n), state: 'OPEN', head: `layer-${n}` })) });

describe('openCount', () => {
  it('counts the open PRs, not the merged or closed ones', () => {
    expect(openCount([pr(7, { state: 'MERGED' }), pr(8), pr(9, { state: 'CLOSED' }), pr(10)])).toBe(2);
  });

  it('is 0 for no PRs, a ship with no list, and PRs that have all ended', () => {
    expect(openCount([])).toBe(0);
    expect(openCount(undefined)).toBe(0);
    expect(openCount([pr(7, { state: 'MERGED' })])).toBe(0);
  });
});

describe('prSummary', () => {
  it('says Ready to merge when every open PR is ready, for one PR and for many', () => {
    expect(prSummary([ready(7)])).toBe('1 PR · Ready to merge');
    expect(prSummary([ready(7), ready(8)])).toBe('2 PRs · Ready to merge');
  });

  it("goes by the server's ready flag, not by the chips", () => {
    expect(prSummary([pr(7, { ready: true })])).toBe('1 PR · Ready to merge');
  });

  it('says what the first open PR that is not ready waits on', () => {
    expect(prSummary([pr(7, { review: 'CHANGES_REQUESTED' })])).toBe('1 PR · #7 changes requested');
    expect(prSummary([pr(7, { review: 'REVIEW_REQUIRED', checks: 'FAILURE' })])).toBe('1 PR · #7 checks failing');
    expect(prSummary([pr(7, { review: 'REVIEW_REQUIRED', checks: 'SUCCESS' })])).toBe('1 PR · #7 waiting on review');
    expect(prSummary([pr(7)])).toBe('1 PR · #7 waiting on review');
  });

  it('names the first PR in the ship\'s list that is not ready, with every open PR counted', () => {
    expect(prSummary([ready(7), pr(8, { review: 'CHANGES_REQUESTED' }), pr(9)])).toBe('3 PRs · #8 changes requested');
  });

  it('counts and names only open PRs: a merged or closed layer waits on nothing', () => {
    expect(prSummary([pr(7, { state: 'MERGED', review: 'CHANGES_REQUESTED' }), pr(8, { state: 'CLOSED' }), ready(9)])).toBe('1 PR · Ready to merge');
    expect(prSummary([pr(7, { state: 'MERGED' }), pr(8, { checks: 'FAILURE' })])).toBe('1 PR · #8 checks failing');
  });

  it('says nothing when no PR is open', () => {
    expect(prSummary([])).toBe('');
    expect(prSummary(undefined)).toBe('');
    expect(prSummary([pr(7, { state: 'MERGED' }), pr(8, { state: 'CLOSED' })])).toBe('');
  });

  it('leaves out feedback, which the ship API does not carry', () => {
    expect(prSummary([pr(7, { review: 'CHANGES_REQUESTED' }), pr(8)])).not.toMatch(/feedback/i);
  });
});

describe('prDot', () => {
  it('is rev for a merged PR whatever its last chips said', () => {
    expect(prDot(pr(7, { state: 'MERGED', review: 'CHANGES_REQUESTED', checks: 'FAILURE' }))).toBe('rev');
  });

  it('is its own kind for a closed PR, which is not a merged one', () => {
    expect(prDot(pr(7, { state: 'CLOSED' }))).toBe('closed');
  });

  it('is err for requested changes or failing checks, even when the other half is good', () => {
    expect(prDot(pr(7, { review: 'CHANGES_REQUESTED', checks: 'SUCCESS' }))).toBe('err');
    expect(prDot(pr(7, { review: 'APPROVED', checks: 'FAILURE' }))).toBe('err');
    expect(prDot(pr(7, { review: 'APPROVED', checks: 'ERROR' }))).toBe('err');
  });

  it('is ok for an approved PR with passing checks', () => {
    expect(prDot(pr(7, { review: 'APPROVED', checks: 'SUCCESS' }))).toBe('ok');
  });

  it('is wait for anything else open', () => {
    expect(prDot(pr(7))).toBe('wait');
    expect(prDot(pr(7, { review: 'APPROVED', checks: 'PENDING' }))).toBe('wait');
    expect(prDot(pr(7, { review: 'REVIEW_REQUIRED', checks: 'SUCCESS' }))).toBe('wait');
  });
});

describe('headerChips', () => {
  const chips = (p: MooredPR) => headerChips(p).map((c) => [c.text, c.tone]);

  it('shows an approved, passing, mergeable PR in ok', () => {
    expect(chips(ready(7))).toEqual([
      ['Approved', 'ok'],
      ['Checks passing', 'ok'],
      ['Mergeable', 'ok'],
    ]);
  });

  it('shows a PR with requested changes, failing checks and conflicts in err', () => {
    expect(chips(pr(7, { review: 'CHANGES_REQUESTED', checks: 'FAILURE', mergeable: 'CONFLICTING', conflicts: true }))).toEqual([
      ['Changes requested', 'err'],
      ['Checks failing', 'err'],
      ['Conflicts', 'err'],
    ]);
  });

  it('shows what is waiting in muted', () => {
    expect(chips(pr(7, { review: 'REVIEW_REQUIRED', checks: 'PENDING' }))).toEqual([
      ['Review required', 'muted'],
      ['Checks pending', 'muted'],
    ]);
  });

  it('shows no chip for a value it does not know', () => {
    expect(chips(pr(7))).toEqual([]);
  });

  it('shows a merged PR as one Merged chip in the merged tone', () => {
    expect(chips(pr(7, { state: 'MERGED', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' }))).toEqual([['Merged', 'rev']]);
  });

  it('shows a closed PR as one Closed chip, never Merged', () => {
    expect(chips(pr(7, { state: 'CLOSED', review: 'APPROVED' }))).toEqual([['Closed', 'muted']]);
  });
});

describe('orderPRs', () => {
  const numbers = (list: MooredPR[]) => list.map((p) => Number(p.url.split('/').pop()));

  it('puts the stack\'s bottom layer first, whatever order the ship listed them in', () => {
    expect(numbers(orderPRs([pr(9), pr(8), pr(7)], entries(7, 8, 9)))).toEqual([7, 8, 9]);
  });

  it('goes by position, not by the order of the stack\'s entries', () => {
    const stack = { entries: [{ position: 2, number: 8, url: URL(8), state: 'OPEN', head: 'b' }, { position: 1, number: 7, url: URL(7), state: 'OPEN', head: 'a' }] };
    expect(numbers(orderPRs([pr(8), pr(7)], stack))).toEqual([7, 8]);
  });

  it('keeps a merged layer in its place', () => {
    const list = orderPRs([pr(8), pr(7, { state: 'MERGED' }), pr(9, { state: 'CLOSED' })], entries(7, 8, 9));
    expect(list.map((p) => [Number(p.url.split('/').pop()), p.state])).toEqual([[7, 'MERGED'], [8, 'OPEN'], [9, 'CLOSED']]);
  });

  it('puts carried PRs outside the stack after it, in the ship\'s order', () => {
    expect(numbers(orderPRs([pr(20), pr(8), pr(10), pr(7)], entries(7, 8)))).toEqual([7, 8, 20, 10]);
  });

  it('shows no stack entry the ship does not carry', () => {
    expect(numbers(orderPRs([pr(8)], entries(6, 7, 8, 9)))).toEqual([8]);
  });

  it('keeps the ship\'s order with no stack', () => {
    expect(numbers(orderPRs([pr(9), pr(7), pr(8)], null))).toEqual([9, 7, 8]);
    expect(numbers(orderPRs([pr(9), pr(7), pr(8)], undefined))).toEqual([9, 7, 8]);
  });

  it('matches an entry to a carried PR by URL, so another repo\'s PR with the same number follows the stack', () => {
    const other = pr(7, { url: 'https://github.com/acme/web/pull/7' });
    const list = orderPRs([other, pr(8), pr(7)], entries(7, 8));
    expect(list.map((p) => p.url)).toEqual([URL(7), URL(8), other.url]);
  });

  it('does not change the list it is given', () => {
    const given = [pr(8), pr(7)];
    orderPRs(given, entries(7, 8));
    expect(numbers(given)).toEqual([8, 7]);
  });
});

describe('stackedPRs', () => {
  it('is the carried PRs in the stack, bottom first, and none without a stack', () => {
    expect(stackedPRs([pr(20), pr(8), pr(7)], entries(7, 8)).map((p) => p.url)).toEqual([URL(7), URL(8)]);
    expect(stackedPRs([pr(8)], null)).toEqual([]);
  });
});

describe('defaultPR', () => {
  it('is the first open PR in stack order', () => {
    expect(defaultPR([pr(7, { state: 'MERGED' }), pr(8), pr(9)])).toEqual(pr(8));
  });

  it('is the bottom PR when none is open, and nothing for no PRs', () => {
    expect(defaultPR([pr(7, { state: 'MERGED' }), pr(8, { state: 'CLOSED' })])).toEqual(pr(7, { state: 'MERGED' }));
    expect(defaultPR([])).toBeUndefined();
  });
});

describe('stackBarText', () => {
  it('says how many PRs are stacked, bottom to top', () => {
    expect(stackBarText(2, true)).toBe('Stacked on GitHub · 2 PRs, bottom to top.');
    expect(stackBarText(3, true)).toBe('Stacked on GitHub · 3 PRs, bottom to top.');
  });

  it('is empty with no stack, and with fewer than two PRs to show', () => {
    expect(stackBarText(2, false)).toBe('');
    expect(stackBarText(1, true)).toBe('');
    expect(stackBarText(0, true)).toBe('');
  });
});

describe('feedbackBarText', () => {
  const answer = (n: number, over: Partial<MooredPR> = {}) => pr(7, { feedback: n, answer: `/commodore resume run-7 feedback round 1 on ${URL(7)}`, ...over });

  it('counts the new items, 1 singular and 2 plural', () => {
    expect(feedbackBarText(answer(1))).toBe("New feedback since the ship's last round: 1 new item.");
    expect(feedbackBarText(answer(2))).toBe("New feedback since the ship's last round: 2 new items.");
  });

  it('names changes requested only when the PR\'s review is CHANGES_REQUESTED', () => {
    expect(feedbackBarText(answer(1, { review: 'CHANGES_REQUESTED' }))).toBe("New feedback since the ship's last round: changes requested, 1 new item.");
    expect(feedbackBarText(answer(3, { review: 'CHANGES_REQUESTED' }))).toBe("New feedback since the ship's last round: changes requested, 3 new items.");
    expect(feedbackBarText(answer(1, { review: 'APPROVED' }))).not.toContain('changes requested');
  });

  it('is empty, so no bar shows, without an answer or on a PR that has ended', () => {
    expect(feedbackBarText(pr(7))).toBe('');
    expect(feedbackBarText(pr(7, { feedback: 2 }))).toBe('');
    expect(feedbackBarText(answer(1, { state: 'MERGED' }))).toBe('');
    expect(feedbackBarText(answer(1, { state: 'CLOSED' }))).toBe('');
  });
});

describe('chooseStackDetail', () => {
  const stack = { number: 12, size: 2, base: 'main', url: 'https://github.com/acme/api/stack/12', ...entries(7, 8) };

  it("returns the URL, stack and command of the first carried PR whose detail has a stack", () => {
    const got = chooseStackDetail([pr(7), pr(8)], { [URL(7)]: { stack: null }, [URL(8)]: { stack, answer_all: '/commodore https://github.com/acme/api/stack/12' } });
    expect(got).toEqual({ url: URL(8), stack, answerAll: '/commodore https://github.com/acme/api/stack/12' });
  });

  it("takes the first stack in the ship's order, not the last", () => {
    const a = { ...entries(7, 8), number: 1 } as never;
    const b = { ...entries(8, 9), number: 2 } as never;
    expect(chooseStackDetail([pr(9), pr(8), pr(7)], { [URL(7)]: { stack: b }, [URL(8)]: { stack: a } })?.stack).toBe(a);
  });

  it('is null when no detail has a stack, or none has loaded', () => {
    expect(chooseStackDetail([pr(7), pr(8)], { [URL(7)]: { stack: null } })).toBeNull();
    expect(chooseStackDetail([pr(7)], {})).toBeNull();
  });
});

describe('showAnswerAll', () => {
  const stack = { number: 12, size: 2, base: 'main', url: 'https://github.com/acme/api/stack/12', ...entries(7, 8) };
  const cmd = '/commodore https://github.com/acme/api/stack/12';
  const fb = (n: number, over: Partial<MooredPR> = {}) => pr(n, { feedback: 1, answer: `a${n}`, ...over });

  it('shows for a two-PR stack where both PRs have feedback', () => {
    expect(showAnswerAll([fb(7), fb(8)], stack, cmd)).toBe(true);
  });

  it('hides without answer_all', () => {
    expect(showAnswerAll([fb(7), fb(8)], stack, undefined)).toBe(false);
    expect(showAnswerAll([fb(7), fb(8)], stack, '')).toBe(false);
  });

  it('hides when an open PR in the pushed list has no feedback, even if the detail still has answer_all', () => {
    expect(showAnswerAll([fb(7), pr(8)], stack, cmd)).toBe(false);
  });

  it('hides for unstacked PRs', () => {
    expect(showAnswerAll([fb(7), fb(8)], null, cmd)).toBe(false);
  });

  it('hides when only one PR is open, and a merged layer does not need feedback', () => {
    expect(showAnswerAll([fb(7, { state: 'MERGED' }), fb(8)], stack, cmd)).toBe(false);
    expect(showAnswerAll([pr(7, { state: 'MERGED' }), fb(8), fb(9)], { ...stack, ...entries(7, 8, 9) }, cmd)).toBe(true);
  });
});

describe('actingWhile', () => {
  it('keeps a confirm locked while the ship is moored', () => {
    expect(actingWhile(true, true)).toBe(true);
    expect(actingWhile(false, true)).toBe(false);
  });

  it('frees the actions once the ship has left its mooring, for the next time it moors', () => {
    expect(actingWhile(true, false)).toBe(false);
  });
});

describe('reviewMark', () => {
  it('names the author a reviewed PR was opened by', () => {
    expect(reviewMark(pr(7, { reviewing: true, author: 'dana' }))).toBe('Reviewing · by @dana');
  });

  it('says Reviewing alone before a poll has the author', () => {
    expect(reviewMark(pr(7, { reviewing: true }))).toBe('Reviewing');
  });

  it('is empty for a PR the fleet opened, whatever its author field says', () => {
    expect(reviewMark(pr(7))).toBe('');
    expect(reviewMark(pr(7, { author: 'fake-viewer' }))).toBe('');
  });
});

describe('reviewStackBar', () => {
  const stackUrl = 'https://github.com/acme/api/stack/9';
  const stackShip = (prs: MooredPR[], url: string | undefined = stackUrl) => ({ url, prs }) as Pick<Ship, 'url' | 'prs'>;
  const layers = (author: string | undefined) => [pr(6, { reviewing: true, author }), pr(7, { reviewing: true, author }), pr(8, { reviewing: true, author })];

  it('names the stack, its author and how many layers are reviewed, bottom to top', () => {
    expect(reviewStackBar(stackShip(layers('dana')))).toBe('Reviewing a GitHub stack by @dana · 3 PRs, bottom to top.');
  });

  it('leaves the author out before a poll has it', () => {
    expect(reviewStackBar(stackShip(layers(undefined)))).toBe('Reviewing a GitHub stack · 3 PRs, bottom to top.');
  });

  it('counts the reviewing layers and takes the author of the bottom one, not the fleet\'s own layers', () => {
    const mixed = [pr(5, { reviewing: true, author: 'dana' }), pr(6, { reviewing: true, author: 'dana' }), pr(7, { author: 'fake-viewer' })];
    expect(reviewStackBar(stackShip(mixed))).toBe('Reviewing a GitHub stack by @dana · 2 PRs, bottom to top.');
  });

  it('is empty for a ship reviewing one PR, and for a PR-URL ship that also carries its own PR', () => {
    expect(reviewStackBar(stackShip(layers('dana').slice(0, 1)))).toBe('');
    const prUrl = 'https://github.com/acme/api/pull/6';
    expect(reviewStackBar(stackShip([pr(6, { reviewing: true, author: 'dana' }), pr(9)], prUrl))).toBe('');
    expect(reviewStackBar(stackShip(layers('dana'), prUrl))).toBe('');
    expect(reviewStackBar(stackShip(layers('dana'), ''))).toBe('');
  });

  it('is empty for a stack the fleet opened', () => {
    expect(reviewStackBar(stackShip([pr(6), pr(7)]))).toBe('');
  });
});

describe('prSummary for a reviewing ship', () => {
  const rev = (n: number, over: Partial<MooredPR> = {}) => pr(n, { reviewing: true, author: 'dana', ...over });

  it('says the author responded', () => {
    expect(prSummary([rev(7, { responded: true })])).toBe('Reviewing 1 PR by @dana · author responded');
    expect(prSummary([rev(7), rev(8, { responded: true })])).toBe('Reviewing 2 PRs by @dana · author responded');
  });

  it('says it waits on the author, never Ready to merge, even when the server marks the PR ready', () => {
    expect(prSummary([rev(7)])).toBe('Reviewing 1 PR by @dana · waiting on the author');
    expect(prSummary([rev(7, { ready: true, review: 'APPROVED', checks: 'SUCCESS' }), rev(8, { ready: true })])).toBe('Reviewing 2 PRs by @dana · waiting on the author');
  });

  it('leaves the author out before a poll has it', () => {
    expect(prSummary([pr(7, { reviewing: true })])).toBe('Reviewing 1 PR · waiting on the author');
  });

  it('counts only open reviewed PRs', () => {
    expect(prSummary([rev(7, { state: 'MERGED' }), rev(8)])).toBe('Reviewing 1 PR by @dana · waiting on the author');
  });
});
