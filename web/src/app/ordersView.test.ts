import { describe, expect, it } from 'vitest';
import type { Order, OrderView, Source } from './api';
import { filterRows, forYou, headerTime, orderRef, refreshedLine, refreshTarget, repoOptions, tabs, viewRows } from './ordersView';

const NOW = Date.parse('2026-11-14T12:00:00Z');
const ago = (ms: number) => new Date(NOW - ms).toISOString();
const H = 3600_000;
const D = 24 * H;

const pr = (over: Partial<NonNullable<Order['pr']>> = {}): NonNullable<Order['pr']> => ({
  state: 'OPEN',
  review: 'REVIEW_REQUIRED',
  checks: 'SUCCESS',
  mergeable: 'MERGEABLE',
  conflicts: false,
  ...over,
});
const order = (id: string, over: Partial<Order> = {}): Order => ({
  id,
  title: `Title of ${id}`,
  url: `https://github.com/acme/api/pull/${id.replace(/\D/g, '')}`,
  repo: 'acme/api',
  ...over,
});
const view = (id: string, title: string, orders: Order[], over: Partial<OrderView> = {}): OrderView => ({ id, title, orders, ...over });
const source = (key: string, name: string, plugin: string, views: OrderView[], over: Partial<Source>): Source => ({
  key,
  name,
  plugin,
  fleet: 'zulu',
  icon_url: '',
  has_schema: false,
  state: 'running',
  error: null,
  warnings: [],
  views,
  ...over,
});
const gh = (views: OrderView[], over: Partial<Source> = {}): Source => source('gh', 'GitHub', 'github', views, { capabilities: ['refresh'], ...over });
const plugin = (key: string, name: string, views: OrderView[], over: Partial<Source> = {}): Source => source(key, name, 'test', views, { capabilities: [], ...over });

const ids = (rows: { order: Order }[]) => rows.map((r) => r.order.id);

describe('For you groups', () => {
  const reviewPR = (id: string, over: Partial<Order> = {}) => order(id, { pr: pr(), author: 'alice', review_requested_at: ago(2 * H), reviewed_since_push: false, ...over });
  const asGroups = (views: OrderView[]) => forYou([gh(views)], NOW);

  it('puts a review-requested PR you have not reviewed since the last push in Needs you', () => {
    const g = asGroups([view('review-requested', 'Review requested', [reviewPR('acme/api#1')])]);
    expect(ids(g.needsYou)).toEqual(['acme/api#1']);
    expect(g.waiting).toEqual([]);
  });

  it('puts it in Waiting on others once you reviewed since the push', () => {
    const g = asGroups([view('review-requested', 'Review requested', [reviewPR('acme/api#1', { reviewed_since_push: true })])]);
    expect(g.needsYou).toEqual([]);
    expect(ids(g.waiting)).toEqual(['acme/api#1']);
  });

  it('treats a review-requested PR whose reviewed_since_push is missing as not reviewed', () => {
    const o = reviewPR('acme/api#1');
    delete o.reviewed_since_push;
    expect(ids(asGroups([view('review-requested', 'Review requested', [o])]).needsYou)).toEqual(['acme/api#1']);
  });

  it.each([
    ['changes requested', { review: 'CHANGES_REQUESTED' }],
    ['a failing check count', { failing_check_count: 1 }],
    ['checks FAILURE', { checks: 'FAILURE' }],
    ['checks ERROR', { checks: 'ERROR' }],
    ['conflicts', { conflicts: true }],
    ['mergeable CONFLICTING', { mergeable: 'CONFLICTING' }],
  ])('puts your open PR with %s in Needs you', (_name, over) => {
    const g = asGroups([view('my-prs', 'My PRs', [order('acme/api#2', { pr: pr(over) })])]);
    expect(ids(g.needsYou)).toEqual(['acme/api#2']);
    expect(g.waiting).toEqual([]);
  });

  it('puts your approved, green PR in Waiting on others', () => {
    const g = asGroups([view('my-prs', 'My PRs', [order('acme/api#2', { pr: pr({ review: 'APPROVED', ready: true }) })])]);
    expect(g.needsYou).toEqual([]);
    expect(ids(g.waiting)).toEqual(['acme/api#2']);
  });

  it('does not put your closed PR with a failing check in Needs you', () => {
    const g = asGroups([view('my-prs', 'My PRs', [order('acme/api#2', { pr: pr({ state: 'MERGED', checks: 'FAILURE' }) })])]);
    expect(g.needsYou).toEqual([]);
  });

  it('puts a hot assigned issue in Needs you and a cold one in Waiting on others', () => {
    const g = asGroups([
      view('assigned-to-me', 'Assigned to me', [
        order('acme/api#3', { url: 'https://github.com/acme/api/issues/3', hot: true }),
        order('acme/api#4', { url: 'https://github.com/acme/api/issues/4', hot: false }),
      ]),
    ]);
    expect(ids(g.needsYou)).toEqual(['acme/api#3']);
    expect(ids(g.waiting)).toEqual(['acme/api#4']);
  });

  it('does not count an assigned PR, hot or not, as a hot issue', () => {
    const g = asGroups([view('assigned-to-me', 'Assigned to me', [order('acme/api#5', { pr: pr(), hot: true })])]);
    expect(g.needsYou).toEqual([]);
    expect(ids(g.waiting)).toEqual(['acme/api#5']);
  });

  it('shows an order in both my-prs and review-requested once, in Needs you when either rule holds', () => {
    const mine = order('acme/api#6', { pr: pr({ review: 'APPROVED' }) });
    const asked = reviewPR('acme/api#6', { url: mine.url });
    const g = asGroups([view('review-requested', 'Review requested', [asked]), view('my-prs', 'My PRs', [mine])]);
    expect(ids(g.needsYou)).toEqual(['acme/api#6']);
    expect(g.waiting).toEqual([]);
    expect(g.count).toBe(1);
  });

  it('matches orders without a url by source and id', () => {
    const a = order('acme/api#7', { url: undefined, pr: pr() });
    const g = asGroups([view('my-prs', 'My PRs', [a]), view('assigned-to-me', 'Assigned to me', [{ ...a }])]);
    expect(g.count).toBe(1);
  });

  it('counts only the three default views of the GitHub source', () => {
    const g = forYou(
      [
        gh([view('my-prs', 'My PRs', [order('acme/api#8', { pr: pr() })]), view('stale', 'Stale', [order('acme/api#9', { pr: pr() })])]),
        plugin('lin', 'Linear', [view('my-prs', 'My PRs', [order('LIN-1', { hot: true })]), view('assigned-to-me', 'Assigned', [order('LIN-2', { hot: true })])]),
      ],
      NOW,
    );
    expect(ids(g.waiting)).toEqual(['acme/api#8']);
    expect(g.needsYou).toEqual([]);
    expect(g.count).toBe(1);
  });

  it('counts the Needs you group and every yours order', () => {
    const g = asGroups([
      view('review-requested', 'Review requested', [reviewPR('acme/api#1'), reviewPR('acme/api#10', { reviewed_since_push: true })]),
      view('my-prs', 'My PRs', [order('acme/api#2', { pr: pr({ review: 'CHANGES_REQUESTED' }) })]),
    ]);
    expect(g.needsYou).toHaveLength(2);
    expect(g.waiting).toHaveLength(1);
    expect(g.count).toBe(3);
  });

  it('is empty with no GitHub source', () => {
    expect(forYou([plugin('lin', 'Linear', [view('v', 'V', [order('LIN-1')])])], NOW)).toEqual({ needsYou: [], waiting: [], count: 0 });
    expect(forYou([], NOW).count).toBe(0);
  });
});

describe('the reason line', () => {
  const only = (viewId: string, o: Order) => {
    const g = forYou([gh([view(viewId, viewId, [o])])], NOW);
    return [...g.needsYou, ...g.waiting][0];
  };

  it('says when a review was requested and by whom opened', () => {
    const row = only('review-requested', order('acme/api#1', { pr: pr(), author: 'alice', review_requested_at: ago(2 * H) }));
    expect(row.reason).toBe('review requested 2h ago · @alice');
  });

  it('drops the part whose field is missing', () => {
    expect(only('review-requested', order('acme/api#1', { pr: pr(), review_requested_at: ago(2 * H) })).reason).toBe('review requested 2h ago');
    expect(only('review-requested', order('acme/api#1', { pr: pr(), author: 'alice' })).reason).toBe('review requested · @alice');
  });

  it.each([
    ['one failing check', { failing_check_count: 1 }, 'your PR · 1 check failing'],
    ['two failing checks', { failing_check_count: 2 }, 'your PR · 2 checks failing'],
    ['a failing rollup with no count', { checks: 'FAILURE' }, 'your PR · checks failing'],
    ['changes requested', { review: 'CHANGES_REQUESTED' }, 'your PR · changes requested'],
    ['conflicts', { conflicts: true }, 'your PR · conflicts'],
  ])('says why your PR needs you: %s', (_n, over, line) => {
    expect(only('my-prs', order('acme/api#2', { pr: pr(over) })).reason).toBe(line);
  });

  it('starts a waiting PR of yours with "your PR"', () => {
    expect(only('my-prs', order('acme/api#2', { pr: pr({ review: 'APPROVED', ready: true }) })).reason).toMatch(/^your PR/);
  });

  it('says "assigned" for an assigned issue, hot or not', () => {
    expect(only('assigned-to-me', order('acme/api#3', { hot: true })).reason).toBe('assigned');
    expect(only('assigned-to-me', order('acme/api#3')).reason).toBe('assigned');
  });

  it('reads a GitHub row in a view tab as opened time and author', () => {
    const src = gh([view('v1', 'Everything', [order('acme/api#1', { opened_at: ago(3 * D), author: 'carol' }), order('acme/api#2', { opened_at: ago(3 * D) }), order('acme/api#3', { author: 'carol' })])]);
    const rows = viewRows([src], 'gh/v1', NOW);
    expect(rows.map((r) => r.reason)).toEqual(['opened 3d ago · @carol', 'opened 3d ago', '@carol']);
  });

  it('reads a plugin row as its reference and view name', () => {
    const src = plugin('lin', 'Linear', [view('triage', 'Triage', [order('LIN-12', { url: undefined, repo: undefined })])]);
    expect(viewRows([src], 'lin/triage', NOW)[0].reason).toBe('LIN-12 · Triage');
  });
});

describe('the state word', () => {
  const forYouRow = (viewId: string, o: Order) => {
    const g = forYou([gh([view(viewId, viewId, [o])])], NOW);
    return [...g.needsYou, ...g.waiting][0];
  };

  it.each([
    ['Ready to merge', 'my-prs', { pr: pr({ ready: true, review: 'APPROVED' }) }],
    ['Changes requested', 'my-prs', { pr: pr({ review: 'CHANGES_REQUESTED' }) }],
    ['Checks failing', 'my-prs', { pr: pr({ failing_check_count: 1 }) }],
    ['Conflicts', 'my-prs', { pr: pr({ conflicts: true }) }],
    ['Review requested', 'review-requested', { pr: pr(), reviewed_since_push: false }],
    ['High', 'assigned-to-me', { hot: true }],
    ['', 'assigned-to-me', {}],
    ['', 'my-prs', { pr: pr({ review: 'APPROVED' }) }],
    ['', 'review-requested', { pr: pr(), reviewed_since_push: true }],
  ] as [string, string, Partial<Order>][])('reads %j for the order in %s', (word, viewId, over) => {
    expect(forYouRow(viewId, order('acme/api#1', over)).state).toBe(word);
  });

  it('takes the first that applies: ready, changes, checks, conflicts, review requested, high', () => {
    const state = (over: Partial<NonNullable<Order['pr']>>, extra: Partial<Order> = {}) => forYouRow('review-requested', order('acme/api#1', { pr: pr(over), reviewed_since_push: false, ...extra })).state;
    expect(state({ ready: true, review: 'CHANGES_REQUESTED', checks: 'FAILURE', conflicts: true })).toBe('Ready to merge');
    expect(state({ review: 'CHANGES_REQUESTED', checks: 'FAILURE', conflicts: true })).toBe('Changes requested');
    expect(state({ checks: 'FAILURE', conflicts: true })).toBe('Checks failing');
    expect(state({ conflicts: true })).toBe('Conflicts');
    expect(state({}, { hot: true })).toBe('Review requested');
  });

  it('colours the word only for a row in Needs you', () => {
    const g = forYou(
      [gh([view('my-prs', 'My PRs', [order('acme/api#1', { pr: pr({ review: 'CHANGES_REQUESTED' }) }), order('acme/api#2', { pr: pr({ ready: true, review: 'APPROVED' }) })])])],
      NOW,
    );
    expect(g.needsYou.map((r) => [r.state, r.needsYou])).toEqual([['Changes requested', true]]);
    expect(g.waiting.map((r) => [r.state, r.needsYou])).toEqual([['Ready to merge', false]]);
  });

  it('gives a view-tab row the same word, coloured when the order is in Needs you', () => {
    const src = gh([view('review-requested', 'Review requested', [order('acme/api#1', { pr: pr(), reviewed_since_push: false }), order('acme/api#2', { pr: pr(), reviewed_since_push: true })])]);
    expect(viewRows([src], 'gh/review-requested', NOW).map((r) => [r.state, r.needsYou])).toEqual([
      ['Review requested', true],
      ['', false],
    ]);
  });
});

describe('the reference', () => {
  it('reads owner/repo #number from the repo and the id', () => {
    expect(orderRef(order('acme/api#7'))).toBe('acme/api #7');
  });

  it('falls back to the url when the order has no repo', () => {
    const url = 'https://github.com/acme/api/pull/7';
    expect(orderRef(order('acme/api#7', { repo: undefined, url }))).toBe('acme/api #7');
  });
});

describe('tabs', () => {
  const lin = plugin('lin', 'Linear', [view('triage', 'Triage', [order('LIN-1')]), view('mine', 'Mine', [order('LIN-2'), order('LIN-3')])]);
  const ghSrc = gh([view('assigned-to-me', 'Assigned to me', [order('acme/api#1')]), view('my-prs', 'My PRs', [order('acme/api#2', { pr: pr() })]), view('custom', 'Custom', [])]);

  it('puts For you first with its count of distinct yours orders', () => {
    const t = tabs([ghSrc]);
    expect(t[0].title).toBe('For you');
    expect(t[0].count).toBe(2);
  });

  it('lists GitHub views, then plugin views, each source in its own view order, whatever the order of sources', () => {
    expect(tabs([lin, ghSrc]).map((t) => t.title)).toEqual(['For you', 'Assigned to me', 'My PRs', 'Custom', 'Linear: Triage', 'Linear: Mine']);
  });

  it('keeps plugin sources in the order given', () => {
    const jira = plugin('jira', 'Jira', [view('open', 'Open', [])]);
    expect(tabs([ghSrc, jira, lin]).map((t) => t.title).slice(4)).toEqual(['Jira: Open', 'Linear: Triage', 'Linear: Mine']);
  });

  it('prefixes a plugin view with its source name only once the fleet has two sources', () => {
    expect(tabs([lin]).map((t) => t.title)).toEqual(['For you', 'Triage', 'Mine']);
    expect(tabs([ghSrc, lin]).map((t) => t.title)).toContain('Linear: Triage');
    expect(tabs([ghSrc, lin]).map((t) => t.title)).toContain('Assigned to me');
  });

  it('counts each view\'s orders', () => {
    const t = tabs([ghSrc, lin]);
    const count = (title: string) => t.find((x) => x.title === title)?.count;
    expect(count('Assigned to me')).toBe(1);
    expect(count('Custom')).toBe(0);
    expect(count('Linear: Mine')).toBe(2);
  });

  it('counts an order that repeats a url once', () => {
    const dup = gh([view('v', 'V', [order('acme/api#1'), order('acme/api#1-again', { url: order('acme/api#1').url }), order('acme/api#2')])]);
    expect(tabs([dup]).find((t) => t.title === 'V')?.count).toBe(2);
    expect(viewRows([dup], 'gh/v', NOW)).toHaveLength(2);
  });

  it('counts orders without a url once per source and id', () => {
    const dup = plugin('lin', 'Linear', [view('v', 'V', [order('LIN-1', { url: undefined }), order('LIN-1', { url: undefined }), order('LIN-2', { url: undefined })])]);
    expect(tabs([dup]).find((t) => t.title === 'V')?.count).toBe(2);
  });

  it('gives each tab an id that viewRows reads back', () => {
    const t = tabs([ghSrc, lin]);
    expect(new Set(t.map((x) => x.id)).size).toBe(t.length);
    const mine = t.find((x) => x.title === 'Linear: Mine')!;
    expect(ids(viewRows([ghSrc, lin], mine.id, NOW))).toEqual(['LIN-2', 'LIN-3']);
  });
});

describe('the header time', () => {
  const t = (s: number) => new Date(NOW - s * 60_000).toISOString();

  it('is the tab\'s own view time for a plugin view tab', () => {
    const src = plugin('lin', 'Linear', [view('a', 'A', [], { refreshed_at: t(2) }), view('b', 'B', [], { refreshed_at: t(30) }), view('c', 'C', [])]);
    const [, a, b, c] = tabs([src]);
    expect(headerTime([src], a.id)).toBe(t(2));
    expect(headerTime([src], b.id)).toBe(t(30));
    expect(headerTime([src], c.id)).toBeUndefined();
  });

  it('reads "just now" under a minute and an age with ago after', () => {
    expect(refreshedLine(new Date(NOW - 20_000).toISOString(), NOW)).toBe('refreshed just now');
    expect(refreshedLine(t(2), NOW)).toBe('refreshed 2m ago');
  });

  it('is a single view\'s time for a view tab', () => {
    const src = gh([view('my-prs', 'My PRs', [], { refreshed_at: t(2) }), view('assigned-to-me', 'Assigned to me', [], { refreshed_at: t(30) })]);
    expect(headerTime([src], 'gh/my-prs')).toBe(t(2));
  });

  it('for For you reads only the GitHub default views', () => {
    const src = gh([
      view('review-requested', 'Review requested', [], { refreshed_at: t(5) }),
      view('my-prs', 'My PRs', [], { refreshed_at: t(7) }),
      view('assigned-to-me', 'Assigned to me', [], { refreshed_at: t(3) }),
      view('custom', 'Custom', [], { refreshed_at: t(90) }),
    ]);
    const lin = plugin('lin', 'Linear', [view('my-prs', 'My PRs', [], { refreshed_at: t(500) })]);
    expect(headerTime([src, lin], 'for-you')).toBe(t(7));
  });

  it('is absent when no view of the tab has answered', () => {
    const src = gh([view('my-prs', 'My PRs', []), view('review-requested', 'Review requested', []), view('assigned-to-me', 'Assigned to me', [])]);
    expect(headerTime([src], 'for-you')).toBeUndefined();
    expect(headerTime([src], 'gh/my-prs')).toBeUndefined();
  });
});

describe('Refresh now', () => {
  it('targets the GitHub source on For you and a source with the refresh capability on its own tabs', () => {
    const lin = plugin('lin', 'Linear', [view('triage', 'Triage', [])], { capabilities: ['refresh'] });
    const jira = plugin('jira', 'Jira', [view('open', 'Open', [])]);
    const g = gh([view('my-prs', 'My PRs', [])]);
    expect(refreshTarget([g, lin, jira], 'for-you')).toBe('gh');
    expect(refreshTarget([g, lin, jira], 'lin/triage')).toBe('lin');
    expect(refreshTarget([g, lin, jira], 'jira/open')).toBeUndefined();
  });

  it('is not offered on For you when the GitHub source cannot refresh', () => {
    expect(refreshTarget([gh([], { capabilities: [] })], 'for-you')).toBeUndefined();
    expect(refreshTarget([gh([], { capabilities: undefined })], 'for-you')).toBeUndefined();
  });
});

describe('the filters', () => {
  const rows = viewRows(
    [
      gh([
        view('v', 'V', [
          order('acme/api#1', { title: 'Fix the Login redirect', labels: ['bug'] }),
          order('acme/web#2', { title: 'Port the table', repo: 'acme/web', labels: ['Design', 'ux'] }),
          order('acme/web#3', { title: 'Cache lookups', repo: 'acme/web' }),
        ]),
      ]),
    ],
    'gh/v',
    NOW,
  );

  it('narrows by title, case-insensitively', () => {
    expect(ids(filterRows(rows, { query: 'login' }))).toEqual(['acme/api#1']);
    expect(ids(filterRows(rows, { query: 'TABLE' }))).toEqual(['acme/web#2']);
  });

  it('narrows by repo, case-insensitively', () => {
    expect(ids(filterRows(rows, { query: 'ACME/WEB' }))).toEqual(['acme/web#2', 'acme/web#3']);
  });

  it('narrows by label, case-insensitively', () => {
    expect(ids(filterRows(rows, { query: 'design' }))).toEqual(['acme/web#2']);
    expect(ids(filterRows(rows, { query: 'BUG' }))).toEqual(['acme/api#1']);
  });

  it('ignores surrounding space and keeps every row for an empty query', () => {
    expect(ids(filterRows(rows, { query: '  login ' }))).toEqual(['acme/api#1']);
    expect(filterRows(rows, { query: '' })).toHaveLength(3);
    expect(filterRows(rows, {})).toHaveLength(3);
  });

  it('narrows by repo with the repo filter, and the two filters combine', () => {
    expect(ids(filterRows(rows, { repo: 'acme/web' }))).toEqual(['acme/web#2', 'acme/web#3']);
    expect(ids(filterRows(rows, { repo: 'acme/web', query: 'cache' }))).toEqual(['acme/web#3']);
    expect(filterRows(rows, { repo: 'acme/api', query: 'table' })).toEqual([]);
  });

  it('lists each repo present once', () => {
    expect([...repoOptions(rows)].sort()).toEqual(['acme/api', 'acme/web']);
  });
});

describe('the repo of an order', () => {
  it('reduces a GitHub repository URL to owner/repo', () => {
    expect(orderRef(order('acme/api#3', { repo: 'https://github.com/acme/api' }))).toBe('acme/api #3');
    const byUrl = order('x#4', { repo: undefined, url: 'https://github.com/acme/api/pull/4' });
    expect(orderRef(byUrl)).toBe('acme/api #4');
  });

  it('gives no repo for a Linear URL: the ref is the id and the repo filter lists nothing', () => {
    const lin = order('ENG-12', { repo: undefined, url: 'https://linear.app/acme/issue/ENG-12/slug' });
    expect(orderRef(lin)).toBe('ENG-12');
    expect(repoOptions(viewRows([plugin('lin', 'Linear', [view('a', 'A', [lin])])], tabs([plugin('lin', 'Linear', [view('a', 'A', [lin])])])[1].id, NOW))).toEqual([]);
  });
});
