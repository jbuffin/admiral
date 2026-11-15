import type { Order, OrderView, Source } from './api';
import { shipAge } from './shipList';

export const FOR_YOU = 'for-you';
const DEFAULT_VIEWS = ['review-requested', 'my-prs', 'assigned-to-me'];

export interface OrderRow {
  /** Stable across refreshes: the source and the order's id. */
  key: string;
  source: string;
  order: Order;
  reason: string;
  /** The state word; empty when none applies. */
  state: string;
  /** In For you's Needs you group; the state word is coloured only then. */
  needsYou: boolean;
}

export interface ForYou {
  needsYou: OrderRow[];
  waiting: OrderRow[];
  count: number;
}

export interface Tab {
  id: string;
  title: string;
  count: number;
}

const isGitHub = (s: Source) => s.plugin === 'github';
const identity = (source: string, o: Order) => o.url || `${source}/${o.id}`;
const open = (o: Order) => !!o.pr && (o.pr.state ?? 'OPEN') === 'OPEN';
const failing = (o: Order) => (o.pr?.failing_check_count ?? 0) > 0 || o.pr?.checks === 'FAILURE' || o.pr?.checks === 'ERROR';
const conflicts = (o: Order) => !!o.pr?.conflicts || o.pr?.mergeable === 'CONFLICTING';
const changes = (o: Order) => o.pr?.review === 'CHANGES_REQUESTED';
const unreviewed = (o: Order) => !!o.pr && o.reviewed_since_push !== true;

function needs(viewId: string, o: Order): boolean {
  if (viewId === 'review-requested') return unreviewed(o);
  if (viewId === 'my-prs') return open(o) && (changes(o) || failing(o) || conflicts(o));
  return viewId === 'assigned-to-me' && !o.pr && !!o.hot;
}

function stateWord(o: Order, inReview: boolean): string {
  if (open(o)) {
    if (o.pr!.ready) return 'Ready to merge';
    if (changes(o)) return 'Changes requested';
    if (failing(o)) return 'Checks failing';
    if (conflicts(o)) return 'Conflicts';
  }
  if (inReview && unreviewed(o)) return 'Review requested';
  return !o.pr && o.hot ? 'High' : '';
}

const join = (...parts: (string | undefined | false)[]) => parts.filter(Boolean).join(' · ');
const author = (o: Order) => (o.author ? `@${o.author}` : '');
const ageOf = (iso: string | undefined, now: number) => (iso ? shipAge(iso, now) : '');

function forYouReason(viewId: string, o: Order, now: number): string {
  if (viewId === 'review-requested') {
    const age = ageOf(o.review_requested_at, now);
    return join(age ? `review requested ${age} ago` : 'review requested', author(o));
  }
  if (viewId === 'my-prs') {
    const n = o.pr?.failing_check_count ?? 0;
    let why = '';
    if (open(o)) {
      if (changes(o)) why = 'changes requested';
      else if (failing(o)) why = n > 0 ? `${n} ${n === 1 ? 'check' : 'checks'} failing` : 'checks failing';
      else if (conflicts(o)) why = 'conflicts';
    }
    return join('your PR', why);
  }
  return 'assigned';
}

function viewReason(src: Source, v: OrderView, o: Order, now: number): string {
  if (isGitHub(src)) {
    const age = ageOf(o.opened_at, now);
    return join(age && `opened ${age} ago`, author(o));
  }
  return join(o.id, v.title);
}

interface Yours {
  needsYou: Map<string, true>;
  reviewing: Set<string>;
  rows: OrderRow[];
}

// One pass over the GitHub source's default views: each yours order once, with the reason from the view that puts it in Needs you.
function collect(sources: Source[], now: number): Yours {
  const gh = sources.find(isGitHub);
  const out: Yours = { needsYou: new Map(), reviewing: new Set(), rows: [] };
  if (!gh) return out;
  const byId = new Map<string, OrderRow & { reasonFromNeeds: boolean }>();
  for (const v of gh.views.filter((x) => DEFAULT_VIEWS.includes(x.id))) {
    for (const o of v.orders) {
      const id = identity(gh.key, o);
      if (v.id === 'review-requested') out.reviewing.add(id);
      const hit = needs(v.id, o);
      if (hit) out.needsYou.set(id, true);
      const seen = byId.get(id);
      if (seen && (seen.reasonFromNeeds || !hit)) continue;
      const row = { key: `${gh.key}/${o.id}`, source: gh.key, order: o, reason: forYouReason(v.id, o, now), state: '', needsYou: false, reasonFromNeeds: hit };
      if (seen) Object.assign(seen, row);
      else {
        byId.set(id, row);
        out.rows.push(row);
      }
    }
  }
  for (const row of out.rows) {
    const id = identity(row.source, row.order);
    row.needsYou = out.needsYou.has(id);
    row.state = stateWord(row.order, out.reviewing.has(id));
  }
  return out;
}

export function forYou(sources: Source[], now: number): ForYou {
  const rows = collect(sources, now).rows.map(({ reasonFromNeeds: _, ...r }: OrderRow & { reasonFromNeeds?: boolean }) => r);
  const needsYou = rows.filter((r) => r.needsYou);
  return { needsYou, waiting: rows.filter((r) => !r.needsYou), count: rows.length };
}

const tabId = (s: Source, v: OrderView) => `${s.key}/${v.id}`;

function distinct(source: string, orders: Order[]): Order[] {
  const seen = new Set<string>();
  return orders.filter((o) => {
    const id = identity(source, o);
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

export function tabs(sources: Source[]): Tab[] {
  const ordered = [...sources.filter(isGitHub), ...sources.filter((s) => !isGitHub(s))];
  const prefix = sources.length >= 2;
  const out: Tab[] = [{ id: FOR_YOU, title: 'For you', count: forYou(sources, 0).count }];
  for (const s of ordered) {
    for (const v of s.views) {
      out.push({ id: tabId(s, v), title: prefix && !isGitHub(s) ? `${s.name}: ${v.title}` : v.title, count: distinct(s.key, v.orders).length });
    }
  }
  return out;
}

function find(sources: Source[], id: string): { src: Source; view: OrderView } | undefined {
  for (const src of sources) {
    const view = src.views.find((v) => tabId(src, v) === id);
    if (view) return { src, view };
  }
  return undefined;
}

export function viewRows(sources: Source[], id: string, now: number): OrderRow[] {
  if (id === FOR_YOU) {
    const g = forYou(sources, now);
    return [...g.needsYou, ...g.waiting];
  }
  const hit = find(sources, id);
  if (!hit) return [];
  const { needsYou, reviewing } = collect(sources, now);
  const gh = isGitHub(hit.src);
  return distinct(hit.src.key, hit.view.orders).map((o) => {
    const ident = identity(hit.src.key, o);
    return {
      key: `${hit.src.key}/${o.id}`,
      source: hit.src.key,
      order: o,
      reason: viewReason(hit.src, hit.view, o, now),
      state: stateWord(o, gh && reviewing.has(ident)),
      needsYou: gh && needsYou.has(ident),
    };
  });
}

const oldest = (views: OrderView[]) =>
  views.reduce<string | undefined>((acc, v) => {
    if (!v.refreshed_at || Number.isNaN(Date.parse(v.refreshed_at))) return acc;
    return acc === undefined || Date.parse(v.refreshed_at) < Date.parse(acc) ? v.refreshed_at : acc;
  }, undefined);

/** For you reads the oldest answer among GitHub's default views, so a stale one isn't hidden; a view tab reads its own view. */
export function headerTime(sources: Source[], id: string): string | undefined {
  if (id === FOR_YOU) return oldest(sources.find(isGitHub)?.views.filter((v) => DEFAULT_VIEWS.includes(v.id)) ?? []);
  const hit = find(sources, id);
  return hit ? oldest([hit.view]) : undefined;
}

/** The header's time words; `shipAge` says "now" under a minute, which doesn't read with "ago". */
export function refreshedLine(iso: string, now: number): string {
  const age = shipAge(iso, now);
  return age === 'now' ? 'refreshed just now' : `refreshed ${age} ago`;
}

/** The source name the header shows and Refresh now asks, if the tab's source can refresh. */
export function refreshTarget(sources: Source[], id: string): string | undefined {
  const src = id === FOR_YOU ? sources.find(isGitHub) : find(sources, id)?.src;
  return src?.capabilities?.includes('refresh') ? src.key : undefined;
}

export function sourceName(sources: Source[], id: string): string {
  return (id === FOR_YOU ? sources.find(isGitHub) : find(sources, id)?.src)?.name ?? '';
}

// The GitHub source sends the repository's URL; plugins and tests send owner/repo. Only a GitHub repository, pull or issue URL reduces to a repo.
export function repoName(o: Order): string {
  const fromUrl = (u: string) => /^https:\/\/github\.com\/([^/]+\/[^/]+?)(?:\/(?:pull|issues)\/\d+)?\/?$/.exec(u)?.[1];
  if (o.repo) return /^https?:\/\//.test(o.repo) ? (fromUrl(o.repo) ?? '') : o.repo;
  return fromUrl(o.url ?? '') ?? '';
}

export function orderRef(o: Order): string {
  const num = /#(\d+)$/.exec(o.id)?.[1] ?? /\/(\d+)\/?$/.exec(o.url ?? '')?.[1];
  const repo = repoName(o);
  return repo && num ? `${repo} #${num}` : repo || o.id;
}

export interface Filter {
  query?: string;
  repo?: string;
}

export function filterRows(rows: OrderRow[], f: Filter): OrderRow[] {
  const q = (f.query ?? '').trim().toLowerCase();
  return rows.filter(({ order: o }) => {
    if (f.repo && repoName(o) !== f.repo) return false;
    if (!q) return true;
    return [o.title, repoName(o), ...(o.labels ?? [])].some((t) => t.toLowerCase().includes(q));
  });
}

export function repoOptions(rows: OrderRow[]): string[] {
  return [...new Set(rows.map((r) => repoName(r.order)).filter(Boolean))];
}

/** The Launch dialog's order for a row's arrow. */
export const launchRef = (r: OrderRow) => ({ source: r.source, id: r.order.id, title: r.order.title, text: r.order.text || r.order.title });

/** The error text of each view the tab lists, shown above its rows. */
export function viewErrors(sources: Source[], id: string): string[] {
  const views = id === FOR_YOU ? (sources.find(isGitHub)?.views.filter((v) => DEFAULT_VIEWS.includes(v.id)) ?? []) : [find(sources, id)?.view];
  return [...new Set(views.flatMap((v) => (v?.error ? [v.error] : [])))];
}
