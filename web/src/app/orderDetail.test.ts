import { describe, expect, it } from 'vitest';
import type { Order, OrderView, Source } from './api';
import type { Campaign } from './campaign';
import { campaignAction, forYouLinkText, openedLine, selectedOrderOf, selectedRow, whyItsHere, withSelectedOrder } from './orderDetail';

const NOW = Date.parse('2026-11-14T12:00:00Z');
const ago = (ms: number) => new Date(NOW - ms).toISOString();
const H = 3600_000;
const D = 24 * H;

const pr = (over: Partial<NonNullable<Order['pr']>> = {}): NonNullable<Order['pr']> => ({ state: 'OPEN', review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE', conflicts: false, ...over });
const order = (id: string, over: Partial<Order> = {}): Order => ({ id, title: `Title of ${id}`, url: `https://github.com/acme/api/issues/${id.replace(/\D/g, '')}`, repo: 'acme/api', ...over });
const view = (id: string, title: string, orders: Order[]): OrderView => ({ id, title, orders });
const source = (key: string, name: string, plugin: string, views: OrderView[]): Source => ({ key, name, plugin, fleet: 'zulu', icon_url: '', has_schema: false, state: 'running', error: null, warnings: [], views, capabilities: [] });
const gh = (views: OrderView[]) => source('gh', 'GitHub', 'github', views);
const plugin = (views: OrderView[]) => source('orders', 'Orders', 'test', views);

const campaign = (over: Partial<Campaign> = {}): Campaign => ({
  id: 'c1',
  url: 'https://github.com/acme/api/issues/7',
  fleet: 'zulu',
  work_repo: '',
  ended: false,
  last_read: '',
  read_error: '',
  progress: { closed: 0, total: 0 },
  counts: {},
  title: 'A campaign',
  number: 7,
  repo: 'acme/api',
  followed_at: '',
  ready_with_you: 0,
  ready_without_you: 0,
  reading: false,
  ...over,
});

describe('Why it\'s here', () => {
  const assigned = order('acme/api#31', { hot: true, author: 'bob' });
  const review = order('acme/api#11', { pr: pr(), author: 'alice', review_requested_at: ago(2 * H), reviewed_since_push: false });
  const mine = order('acme/api#21', { pr: pr({ review: 'CHANGES_REQUESTED' }), author: 'me', reviewed_since_push: true });

  it('says "Assigned to you" for an assigned order, then the views it is in with For you first', () => {
    const sources = [gh([view('assigned-to-me', 'Assigned to me', [assigned])])];
    expect(whyItsHere(sources, 'gh', assigned, NOW)).toBe('Assigned to you · in “For you” and “Assigned to me”');
  });

  it('capitalises the other For you reasons as the row words them', () => {
    const sources = [gh([view('review-requested', 'Review requested', [review]), view('my-prs', 'My PRs', [mine])])];
    expect(whyItsHere(sources, 'gh', review, NOW)).toBe('Review requested 2h ago · @alice · in “For you” and “Review requested”');
    expect(whyItsHere(sources, 'gh', mine, NOW)).toBe('Your PR · changes requested · in “For you” and “My PRs”');
  });

  it('lists three views with a comma and "and", matching the same order across views by url', () => {
    const inMine = order('acme/api#21', { pr: pr({ review: 'CHANGES_REQUESTED' }), author: 'me' });
    const inAssigned = order('acme/api#21', { pr: pr({ review: 'CHANGES_REQUESTED' }), author: 'me', id: 'acme/api#21-alias' });
    const sources = [gh([view('assigned-to-me', 'Assigned to me', [inAssigned]), view('my-prs', 'My PRs', [inMine])])];
    expect(whyItsHere(sources, 'gh', inMine, NOW)).toBe('Your PR · changes requested · in “For you”, “Assigned to me” and “My PRs”');
  });

  it('gives only the view when the order is not in For you', () => {
    const o = order('ord-1', { url: undefined });
    expect(whyItsHere([gh([]), plugin([view('open', 'Open work', [o])])], 'orders', o, NOW)).toBe('In “Open work”');
  });

  it('matches an order without a url by source and id, so another source\'s same id is not counted', () => {
    const a = order('ord-1', { url: undefined });
    const b = order('ord-1', { url: undefined });
    const sources = [gh([view('open', 'Open', [b])]), plugin([view('open', 'Mine', [a])])];
    expect(whyItsHere(sources, 'orders', a, NOW)).toBe('In “Mine”');
  });
});

describe('Opened', () => {
  it('reads age and author together', () => {
    expect(openedLine(order('a#1', { opened_at: ago(3 * D), author: 'bob' }), NOW)).toBe('3d ago by @bob');
  });
  it('says "by @author" alone without a time', () => {
    expect(openedLine(order('a#1', { author: 'bob' }), NOW)).toBe('by @bob');
  });
  it('says the age alone without an author', () => {
    expect(openedLine(order('a#1', { opened_at: ago(5 * H) }), NOW)).toBe('5h ago');
  });
  it('is empty when both are missing, so the row is dropped', () => {
    expect(openedLine(order('a#1'), NOW)).toBe('');
  });
  it('treats an unreadable time as missing', () => {
    expect(openedLine(order('a#1', { opened_at: 'soon', author: 'bob' }), NOW)).toBe('by @bob');
  });
});

describe('the campaign action', () => {
  const kids = order('a#7', { url: 'https://github.com/acme/api/issues/7', sub_issues: 4 });

  it('offers Follow as campaign for an order with sub-issues, a url and no campaign', () => {
    expect(campaignAction(kids, [])).toEqual({ kind: 'follow', url: 'https://github.com/acme/api/issues/7' });
  });
  it('offers Open campaign, in that campaign\'s fleet, when an un-ended campaign has the url', () => {
    expect(campaignAction(kids, [campaign({ id: 'c9', fleet: 'alpha' })])).toEqual({ kind: 'open', id: 'c9', fleet: 'alpha' });
  });
  it('still offers Follow when the only campaign with the url has ended', () => {
    expect(campaignAction(kids, [campaign({ ended: true })])).toEqual({ kind: 'follow', url: kids.url });
  });
  it('ignores campaigns on other urls', () => {
    expect(campaignAction(kids, [campaign({ url: 'https://github.com/acme/api/issues/8' })])?.kind).toBe('follow');
  });
  it('offers nothing without sub-issues', () => {
    expect(campaignAction(order('a#7', { sub_issues: 0 }), [])).toBeNull();
    expect(campaignAction(order('a#7'), [])).toBeNull();
  });
  it('offers nothing without a url', () => {
    expect(campaignAction(order('a#7', { url: undefined, sub_issues: 4 }), [])).toBeNull();
  });
});

describe('the "N for you →" link', () => {
  const needsYou = (n: number) => Array.from({ length: n }, (_, i) => order(`acme/api#${i + 1}`, { pr: pr(), reviewed_since_push: false, author: 'alice' }));

  it('counts For you\'s Needs you', () => {
    expect(forYouLinkText([gh([view('review-requested', 'Review requested', needsYou(3))])], NOW)).toBe('3 for you →');
  });
  it('does not count Waiting on others', () => {
    const waiting = order('acme/api#9', { pr: pr(), reviewed_since_push: true });
    expect(forYouLinkText([gh([view('review-requested', 'Review requested', [...needsYou(2), waiting])])], NOW)).toBe('2 for you →');
  });
  it('is absent at zero, with no GitHub source or nothing that needs you', () => {
    expect(forYouLinkText([], NOW)).toBe('');
    expect(forYouLinkText([plugin([view('open', 'Open', needsYou(2))])], NOW)).toBe('');
    expect(forYouLinkText([gh([view('review-requested', 'Review requested', [])])], NOW)).toBe('');
  });
});

describe('the selected order in the URL', () => {
  it('reads the order beside the tab', () => {
    expect(selectedOrderOf('?fleet=zulu&tab=orders%2Fopen&order=orders%2Ford-1')).toBe('orders/ord-1');
  });
  it('reads an id with a slash and a hash', () => {
    expect(selectedOrderOf('?order=gh%2Facme%2Fapi%232')).toBe('gh/acme/api#2');
  });
  it('is null with no order or a malformed one', () => {
    expect(selectedOrderOf('?tab=gh%2Fmy-prs')).toBeNull();
    expect(selectedOrderOf('')).toBeNull();
    expect(selectedOrderOf('?order=')).toBeNull();
  });
  it('writes the order encoded and keeps the other params', () => {
    expect(withSelectedOrder('?fleet=zulu&tab=gh%2Fmy-prs', 'gh/acme/api#2')).toBe('?fleet=zulu&tab=gh%2Fmy-prs&order=gh%2Facme%2Fapi%232');
  });
  it('replaces an earlier selection', () => {
    expect(withSelectedOrder('?order=gh%2Fa%231&fleet=zulu', 'gh/b#2')).toBe('?fleet=zulu&order=gh%2Fb%232');
  });
  it('clears the order, and the search when nothing else is left', () => {
    expect(withSelectedOrder('?fleet=zulu&order=gh%2Fa%231', null)).toBe('?fleet=zulu');
    expect(withSelectedOrder('?order=gh%2Fa%231', null)).toBe('');
  });
  it('round-trips a key', () => {
    expect(selectedOrderOf(withSelectedOrder('', 'orders/Fix #4 / now'))).toBe('orders/Fix #4 / now');
  });
});

describe('the selected row', () => {
  const o = order('acme/api#31', { hot: true });
  const sources = [gh([view('assigned-to-me', 'Assigned to me', [o])])];

  it('finds the row the URL names in the tab\'s view', () => {
    expect(selectedRow(sources, 'for-you', '?order=gh%2Facme%2Fapi%2331', NOW)?.order.id).toBe('acme/api#31');
    expect(selectedRow(sources, 'gh/assigned-to-me', '?tab=gh%2Fassigned-to-me&order=gh%2Facme%2Fapi%2331', NOW)?.order.id).toBe('acme/api#31');
  });
  it('is undefined once a refresh removed the order, so the pane closes', () => {
    expect(selectedRow([gh([view('assigned-to-me', 'Assigned to me', [])])], 'for-you', '?order=gh%2Facme%2Fapi%2331', NOW)).toBeUndefined();
  });
  it('is undefined with no order in the URL', () => {
    expect(selectedRow(sources, 'for-you', '', NOW)).toBeUndefined();
  });
});
