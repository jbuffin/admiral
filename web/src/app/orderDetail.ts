import type { Order, Source } from './api';
import type { Campaign } from './campaign';
import { forYou, identity, orderRef, viewRows, type OrderRow } from './ordersView';
import { shipAge } from './shipList';

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

function andList(items: string[]): string {
  if (items.length < 2) return items.join('');
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

/** The sentence under "Why it's here": the For you reason, then every view the order is in. Orders match across views by url, else source and id. */
export function whyItsHere(sources: Source[], source: string, order: Order, now: number): string {
  const id = identity(source, order);
  const g = forYou(sources, now);
  const raw = [...g.needsYou, ...g.waiting].find((r) => identity(r.source, r.order) === id)?.reason ?? '';
  const reason = capital(raw === 'assigned' ? 'assigned to you' : raw);
  const views = sources.flatMap((s) => s.views.filter((v) => v.orders.some((o) => identity(s.key, o) === id)).map((v) => `“${v.title}”`));
  const named = andList(raw ? ['“For you”', ...views] : views);
  if (!named) return reason;
  return raw ? `${reason} · in ${named}` : `In ${named}`;
}

/** "3d ago by @author"; empty when neither the time nor the author is known. */
export function openedLine(order: Order, now: number): string {
  const age = order.opened_at ? shipAge(order.opened_at, now) : '';
  const when = age === 'now' ? 'just now' : age && `${age} ago`;
  return [when, order.author && `by @${order.author}`].filter(Boolean).join(' ');
}

export type CampaignAction = { kind: 'follow'; url: string } | { kind: 'open'; id: string; fleet: string } | null;

export function campaignAction(order: Order, campaigns: Campaign[]): CampaignAction {
  if (!order.url || (order.sub_issues ?? 0) <= 0) return null;
  const hit = campaigns.find((c) => !c.ended && c.url === order.url);
  return hit ? { kind: 'open', id: hit.id, fleet: hit.fleet } : { kind: 'follow', url: order.url };
}

/** The pane's top line: the repo reference for an order with a url, else its id and the view it came from. */
export function topLine(sources: Source[], source: string, order: Order): string {
  if (order.url) return orderRef(order);
  const id = identity(source, order);
  const view = sources.flatMap((s) => s.views.filter((v) => v.orders.some((o) => identity(s.key, o) === id))).at(0);
  return [order.id, view?.title].filter(Boolean).join(' · ');
}

/** The Ships link's words for a Needs you count; empty when nothing needs you. */
export function forYouLinkText(needsYou: number): string {
  return needsYou > 0 ? `${needsYou} for you →` : '';
}

export const selectedOrderOf = (search: string): string | null => new URLSearchParams(search).get('order') || null;

export function withSelectedOrder(search: string, key: string | null): string {
  const q = new URLSearchParams(search);
  q.delete('order');
  if (key) q.set('order', key);
  return q.size ? `?${q}` : '';
}

/** The row the URL selects in the tab's view; undefined once a refresh removed it. */
export function selectedRow(sources: Source[], tabId: string, search: string, now: number): OrderRow | undefined {
  const key = selectedOrderOf(search);
  return key ? viewRows(sources, tabId, now).find((r) => r.key === key) : undefined;
}

/** True once sources have loaded and the URL's order is no longer in the tab; while they are still empty a reload keeps its selection. */
export function selectionGone(sources: Source[], tabId: string, search: string, now: number): boolean {
  return sources.length > 0 && selectedOrderOf(search) !== null && !selectedRow(sources, tabId, search, now);
}
