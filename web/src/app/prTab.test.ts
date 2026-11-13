import { describe, expect, it } from 'vitest';
import type { MooredPR } from './api';
import { chooseStack, defaultPR, headerChips, openCount, orderPRs, prDot, prSummary, stackBarText, stackedPRs } from './prTab';

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

describe('chooseStack', () => {
  it('takes the first stack a carried PR\'s detail has, in the ship\'s order', () => {
    const a = { ...entries(7, 8), number: 1 } as never;
    const b = { ...entries(8, 9), number: 2 } as never;
    expect(chooseStack([pr(9), pr(8), pr(7)], { [URL(7)]: { stack: b }, [URL(8)]: { stack: a } })).toBe(a);
  });

  it('is null when no detail has a stack, or none has loaded', () => {
    expect(chooseStack([pr(7), pr(8)], { [URL(7)]: { stack: null } })).toBeNull();
    expect(chooseStack([pr(7)], {})).toBeNull();
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
