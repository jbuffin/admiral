import type { Order, OrderView, Ship, Source } from './api';
import type { OrderRef } from './LaunchDialog';
import { isStackUrl, needsYouAgain, orderRowAction, shipsOnOrder, stackRowActions, type RowAction } from './moored';
import { stackBarLine } from './prTab';
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
  /** False for a parent row built from a child's `parent`: no view of the source lists it. */
  inView: boolean;
  /** Whether a launch can find the order: some view of the source holds it. */
  launchable: boolean;
  /** A stack row built from its layers, never "not in this view". */
  stackRow?: boolean;
  /** A stack row's launch lands on this layer's order: the bottom open shown layer. */
  stackLayer?: string;
  /** A chain group's header row: not a GitHub stack, never launched or counted as one. */
  chainRow?: boolean;
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
export const identity = (source: string, o: Order) => o.url || `${source}/${o.id}`;
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

/** `in #n` for an order with a parent, `in stack #n` for a PR in a GitHub stack, never naming the parent's repo. */
export function parentNote(o: Order): string {
  if (o.pr && o.stack) return `in stack #${o.stack.number}`;
  const p = o.parent;
  const num = p && (/#(\d+)$/.exec(p.id)?.[1] ?? /\/(\d+)\/?$/.exec(p.url)?.[1]);
  return num ? `in #${num}` : '';
}

/** A For you reason without its `in #n` or `in stack #n` lead. */
export const withoutParentNote = (reason: string) => reason.replace(/^in (?:stack )?#\d+(?: · |$)/, '');
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
  // With no repo the row's ref already shows the id.
  return join(repoName(o) && o.id, v.title);
}

interface Yours {
  needsYou: Map<string, true>;
  reviewing: Set<string>;
  rows: OrderRow[];
}

// One pass over the GitHub source's default views: each yours order once, with the reason from the view that puts it in Needs you.
function collect(sources: Source[], now: number, ships: Ship[]): Yours {
  const gh = sources.find(isGitHub);
  const out: Yours = { needsYou: new Map(), reviewing: new Set(), rows: [] };
  if (!gh) return out;
  const byId = new Map<string, OrderRow & { reasonFromNeeds: boolean }>();
  for (const v of gh.views.filter((x) => DEFAULT_VIEWS.includes(x.id))) {
    for (const o of v.orders) {
      const id = identity(gh.key, o);
      if (v.id === 'review-requested') out.reviewing.add(id);
      const hit = needs(v.id, o) || needsYouAgain(ships, o);
      if (hit) out.needsYou.set(id, true);
      const seen = byId.get(id);
      if (seen && (seen.reasonFromNeeds || !hit)) continue;
      const row = { key: `${gh.key}/${o.id}`, source: gh.key, order: o, reason: join(parentNote(o), forYouReason(v.id, o, now)), state: '', needsYou: false, inView: true, launchable: true, reasonFromNeeds: hit };
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

export function forYou(sources: Source[], now: number, ships: Ship[] = []): ForYou {
  const rows = collect(sources, now, ships).rows.map(({ reasonFromNeeds: _, ...r }: OrderRow & { reasonFromNeeds?: boolean }) => r);
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
  const gh = sources.find(isGitHub);
  const forYouTab = (title: string): Tab => ({ id: FOR_YOU, title, count: forYou(sources, 0).count });
  const out: Tab[] = gh?.tabs ? [] : [forYouTab('For you')];
  for (const s of ordered) {
    const arranged = s === gh ? s.tabs : undefined;
    if (!arranged) {
      for (const v of s.views) {
        out.push({ id: tabId(s, v), title: prefix && !isGitHub(s) ? `${s.name}: ${v.title}` : v.title, count: distinct(s.key, v.orders).length });
      }
      continue;
    }
    for (const t of arranged) {
      if (t.id === FOR_YOU) out.push(forYouTab(t.title));
      else {
        const v = s.views.find((x) => x.id === t.id);
        if (v) out.push({ id: tabId(s, v), title: t.title, count: distinct(s.key, v.orders).length });
      }
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

export function viewRows(sources: Source[], id: string, now: number, ships: Ship[] = []): OrderRow[] {
  if (id === FOR_YOU) {
    const g = forYou(sources, now, ships);
    return [...g.needsYou, ...g.waiting];
  }
  const hit = find(sources, id);
  if (!hit) return [];
  const { needsYou, reviewing } = collect(sources, now, ships);
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
      inView: true,
      launchable: true,
    };
  });
}

export interface ViewGroup {
  row: OrderRow;
  /** The children shown under the row; empty for a loose row and in For you. */
  children: OrderRow[];
  /** Any shown child is High. */
  high: boolean;
  /** Live or moored ships carrying a shown child, each once. */
  ships: number;
}

const urlKey = (u: string) => u.replace(/\/+$/, '').toLowerCase();
const CARRYING = ['underway', 'needs_you', 'moored'];

function shipCount(ships: Ship[], children: OrderRow[], groupUrl?: string): number {
  const urls = new Set([...children.map((c) => c.order.url), groupUrl].filter((u): u is string => !!u).map(urlKey));
  if (urls.size === 0) return 0;
  return ships.filter((s) => CARRYING.includes(s.status) && [s.url, s.link, ...(s.issues ?? []), ...(s.prs ?? []).map((p) => p.url)].some((u) => !!u && urls.has(urlKey(u)))).length;
}

function parentRow(src: Source, p: NonNullable<Order['parent']>): OrderRow {
  const held = src.views.flatMap((v) => v.orders).find((o) => o.id === p.id);
  // The held order's text is what a launch from this row sends; its labels stay off a row not in this view.
  const order: Order = { id: p.id, title: p.title, url: p.url, ...(p.repo ? { repo: p.repo } : {}), ...(held?.text ? { text: held.text } : {}) };
  return { key: `${src.key}/${p.id}`, source: src.key, order, reason: 'not in this view', state: '', needsYou: false, inView: false, launchable: !!held };
}

const stackOf = (r: OrderRow) => (r.order.pr ? r.order.stack : undefined);

function stackRow(src: Source, layer: OrderRow, s: NonNullable<Order['stack']>): OrderRow {
  const order: Order = { id: s.url, title: `Stack #${s.number}`, url: s.url, repo: repoName(layer.order) };
  return { key: `${src.key}/${s.url}`, source: src.key, order, reason: '', state: '', needsYou: false, inView: false, launchable: false, stackRow: true };
}

function chainRow(src: Source, chain: NonNullable<Order['chain']>, shown: number, layer: OrderRow): OrderRow {
  const order: Order = { id: chain.id, title: `${shown} chained PRs`, url: '', repo: repoName(layer.order) };
  return { key: `${src.key}/chain:${chain.id}`, source: src.key, order, reason: '', state: '', needsYou: false, inView: false, launchable: false, chainRow: true };
}

const withNote = (r: OrderRow): OrderRow => (parentNote(r.order) ? { ...r, reason: join(parentNote(r.order), r.reason) } : r);

/** A view tab's rows with children placed under their parent's row and PRs under their GitHub stack's row; For you stays flat. */
export function viewGroups(sources: Source[], id: string, now: number, ships: Ship[], filter: Filter = {}): ViewGroup[] {
  const all = viewRows(sources, id, now, ships);
  const shown = filterRows(all, filter);
  const flat = (r: OrderRow): ViewGroup => ({ row: r, children: [], high: false, ships: 0 });
  const hit = find(sources, id);
  if (id === FOR_YOU || !hit) return shown.map(flat);
  const candidates = all.filter((r) => !r.order.pr && r.order.parent?.id && r.order.parent.id !== r.order.id);
  const parentIds = new Set(candidates.map((r) => r.order.parent!.id));
  const isChild = (r: OrderRow) => candidates.includes(r) && !parentIds.has(r.order.id);
  const inView = new Map(all.map((r) => [r.order.id, r]));
  const shownParents = new Set(shown.filter(isChild).map((r) => r.order.parent!.id));
  const groups = new Map<string, ViewGroup>();
  const out: ViewGroup[] = [];
  // The route links only a run of layers from the bottom up, so a filter that hides a middle layer leaves the layers above it as ordinary PRs.
  const chainPositions = new Map<string, Set<number>>();
  for (const r of shown) if (r.order.pr && !r.order.stack && r.order.chain) chainPositions.set(r.order.chain.id, (chainPositions.get(r.order.chain.id) ?? new Set()).add(r.order.chain.position));
  const chainRun = new Map<string, number>();
  for (const [cid, positions] of chainPositions) {
    let k = 0;
    while (positions.has(k + 1)) k++;
    if (k >= 2) chainRun.set(cid, k);
  }
  for (const r of shown) {
    const stack = stackOf(r);
    const chain = r.order.pr && !stack && r.order.chain && r.order.chain.position <= (chainRun.get(r.order.chain.id) ?? 0) ? r.order.chain : undefined;
    if (chain) {
      let g = groups.get(`chain:${chain.id}`);
      if (!g) {
        g = { row: chainRow(hit.src, chain, chainRun.get(chain.id)!, r), children: [], high: false, ships: 0 };
        groups.set(`chain:${chain.id}`, g);
        out.push(g);
      }
      g.children.push({ ...r, reason: join(parentNote(r.order), r.reason) });
    } else if (stack) {
      if (r.order.pr!.state === 'MERGED') continue;
      let g = groups.get(stack.url);
      if (!g) {
        g = { row: stackRow(hit.src, r, stack), children: [], high: false, ships: 0 };
        groups.set(stack.url, g);
        out.push(g);
      }
      g.children.push({ ...r, reason: join(parentNote(r.order), r.reason) });
    } else if (isChild(r)) {
      const p = r.order.parent!;
      let g = groups.get(p.id);
      if (!g) {
        const head = inView.get(p.id);
        g = { row: head ? withNote(head) : parentRow(hit.src, p), children: [], high: false, ships: 0 };
        groups.set(p.id, g);
        out.push(g);
      }
      g.children.push({ ...r, reason: join(parentNote(r.order), r.reason) });
    } else if (!shownParents.has(r.order.id)) out.push(flat(withNote(r)));
  }
  for (const g of groups.values()) {
    if (g.row.stackRow) g.children.sort((a, b) => stackOf(a)!.position - stackOf(b)!.position);
    if (g.row.chainRow) g.children.sort((a, b) => a.order.chain!.position - b.order.chain!.position);
    g.high = g.children.some((c) => !!c.order.hot);
    if (g.row.stackRow) {
      const bottom = g.children.find((c) => open(c.order));
      g.row = { ...g.row, launchable: !!bottom, stackLayer: bottom?.order.id };
    }
    g.ships = shipCount(ships, g.children, g.row.stackRow ? g.row.order.url : undefined);
  }
  return out;
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

/** The source behind a tab: For you reads the GitHub source. */
function tabSource(sources: Source[], id: string): Source | undefined {
  return id === FOR_YOU ? sources.find(isGitHub) : find(sources, id)?.src;
}

/** The source name the header shows and Refresh now asks, if the tab's source can refresh. */
export function refreshTarget(sources: Source[], id: string): string | undefined {
  const src = tabSource(sources, id);
  return src?.capabilities?.includes('refresh') ? src.key : undefined;
}

/** The Refresh control is a spinner while the tab's source refreshes, whoever asked, or while this page's own request is in flight. */
export function refreshBusy(sources: Source[], tabId: string, posting: boolean): boolean {
  return posting || !!tabSource(sources, tabId)?.refreshing;
}

export function sourceName(sources: Source[], id: string): string {
  return tabSource(sources, id)?.name ?? '';
}

// The GitHub source sends the repository's URL; plugins and tests send owner/repo. Only a GitHub repository, pull or issue URL reduces to a repo.
export function repoName(o: Order): string {
  const fromUrl = (u: string) => /^https:\/\/github\.com\/([^/]+\/[^/]+?)(?:\/(?:pull|issues)\/\d+)?\/?$/.exec(u)?.[1];
  if (o.repo) return /^https?:\/\//.test(o.repo) ? (fromUrl(o.repo) ?? '') : o.repo;
  return fromUrl(o.url ?? '') ?? '';
}

export function orderRef(o: Order): string {
  // A GitHub stack's number is its title; the reference names only the repo.
  if (isStackUrl(o.url)) return repoName(o);
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
export const launchRef = (r: OrderRow) =>
  r.stackRow
    ? { source: r.source, id: r.stackLayer ?? '', title: r.order.title, text: r.order.url ?? '', stack: true }
    : { source: r.source, id: r.order.id, title: r.order.title, text: r.order.text || r.order.title };

/** The error text of each view the tab lists, shown above its rows. */
export function viewErrors(sources: Source[], id: string): string[] {
  const views = id === FOR_YOU ? (sources.find(isGitHub)?.views.filter((v) => DEFAULT_VIEWS.includes(v.id)) ?? []) : [find(sources, id)?.view];
  return [...new Set(views.flatMap((v) => (v?.error ? [v.error] : [])))];
}

/** The lines the Launch dialog shows under a GitHub stack's title: who is being reviewed, then each layer that already has a ship. */
export function stackLaunchLines(g: ViewGroup, ships: Ship[], viewer: string | undefined): string[] {
  const layers = g.children.filter((c) => open(c.order));
  const theirs = layers.filter((c) => !viewer || c.order.author !== viewer).length;
  const lines = theirs > 0 ? [`Reviewing stack #${/\/stack\/(\d+)/.exec(g.row.order.url ?? '')?.[1] ?? ''} · ${theirs} ${theirs === 1 ? 'PR' : 'PRs'}`] : [];
  for (const { c, held } of heldLayers(g, ships)) lines.push(`PR #${/#(\d+)$/.exec(c.order.id)?.[1] ?? /\/(\d+)\/?$/.exec(c.order.url ?? '')?.[1] ?? ''} already has ship ${held.name}`);
  return lines;
}

const heldLayers = (g: ViewGroup, ships: Ship[]): { c: OrderRow; held: Ship }[] =>
  g.children.filter((c) => open(c.order)).flatMap((c) => {
    const held = shipsOnOrder(ships, c.order)[0];
    return held ? [{ c, held }] : [];
  });

/** The action of a group's row and of each child, in order; one ship holding a GitHub stack's every open layer takes the stack row. */
export function groupRowActions(ships: Ship[], g: ViewGroup, viewer: string | undefined): { row: RowAction; children: RowAction[] } {
  if (g.row.stackRow) return stackRowActions(ships, g.children.map((c) => c.order), viewer);
  return { row: orderRowAction(ships, g.row.order), children: g.children.map((c) => orderRowAction(ships, c.order)) };
}

/** The Launch dialog's order for a GitHub stack: its lines, and `anyway` when a layer already has a ship so the launch goes ahead on confirm. */
export function stackLaunchRef(g: ViewGroup, ships: Ship[], viewer: string | undefined): OrderRef {
  const notes = stackLaunchLines(g, ships, viewer);
  return { ...launchRef(g.row), ...(notes.length > 0 && { notes }), ...(heldLayers(g, ships).length > 0 && { anyway: true }) };
}

export const MAX_CHECKED = 50;

/** A GitHub view tab whose orders are all issues, at least one. */
export function isIssueTab(sources: Source[], tabId: string): boolean {
  const hit = tabId === FOR_YOU ? undefined : find(sources, tabId);
  if (!hit || !isGitHub(hit.src)) return false;
  const orders = hit.view.orders;
  return orders.length > 0 && orders.every((o) => !o.pr);
}

export interface BoxState {
  disabled: boolean;
  label: string;
}

// A synthesized parent row never shows its own parent, so it stays checkable; the create route refuses one that has a parent.
const checkable = (r: OrderRow) => !!r.order.url && !r.order.parent;

export function boxState(row: OrderRow, checked: ReadonlySet<string>): BoxState {
  const ref = orderRef(row.order);
  if (row.order.parent) return { disabled: true, label: `${ref} already has a parent` };
  const full = checked.size >= MAX_CHECKED && !checked.has(row.key);
  return { disabled: !row.order.url || full, label: `Select ${ref}` };
}

/** The tab's rows in order, with a parent no view lists placed just above its first child. */
export function selectableRows(sources: Source[], tabId: string, now: number, ships: Ship[]): OrderRow[] {
  const rows = viewRows(sources, tabId, now, ships);
  const hit = find(sources, tabId);
  if (!hit) return rows;
  const candidates = rows.filter((r) => !r.order.pr && r.order.parent?.id && r.order.parent.id !== r.order.id);
  const parentIds = new Set(candidates.map((r) => r.order.parent!.id));
  const held = new Set(rows.map((r) => r.order.id));
  const out: OrderRow[] = [];
  for (const r of rows) {
    const p = r.order.parent;
    if (p && candidates.includes(r) && !parentIds.has(r.order.id) && !held.has(p.id)) {
      held.add(p.id);
      out.push(parentRow(hit.src, p));
    }
    out.push(r);
  }
  return out;
}

/** The checked keys that a refreshed list still holds and can still check. */
export function keepChecked(checked: ReadonlySet<string>, rows: OrderRow[]): Set<string> {
  const ok = new Set(rows.filter(checkable).map((r) => r.key));
  return new Set([...checked].filter((k) => ok.has(k)));
}

export interface SelectState {
  fleet: string;
  tab: string;
  on: boolean;
  checked: ReadonlySet<string>;
}

export function selectAfterChoose(state: SelectState, tab: string, fleet: string): SelectState {
  if (state.tab === tab && state.fleet === fleet) return state;
  return { fleet, tab, on: false, checked: new Set() };
}

/** The state a render stores: it ends with a tab or fleet change or a tab that isn't an issue tab, and drops checks a refresh removed. */
export function selection(state: SelectState, ctx: { tab: string; fleet: string; rows: OrderRow[]; issueTab: boolean }): SelectState {
  const next = selectAfterChoose(state, ctx.tab, ctx.fleet);
  if (next !== state) return next;
  if (!ctx.issueTab) return state.on || state.checked.size > 0 ? { ...state, on: false, checked: new Set() } : state;
  const kept = keepChecked(state.checked, ctx.rows);
  return kept.size === state.checked.size ? state : { ...state, checked: kept };
}

export function checkedUrls(rows: OrderRow[], checked: ReadonlySet<string>): string[] {
  return rows.filter((r) => checked.has(r.key) && r.order.url).map((r) => r.order.url!);
}

export const issuesLabel = (n: number) => `${n} ${n === 1 ? 'issue' : 'issues'}`;

export function newCampaignSearch(search: string, urls: string[]): string {
  const q = new URLSearchParams(search);
  q.delete('newCampaign');
  if (urls.length > 0) q.set('newCampaign', urls.join(','));
  return q.size ? `?${q}` : '';
}

/** The issue urls the parameter names, dropping anything else; `owner/repo#n` reads as a github.com issue. */
export function newCampaignRefs(search: string): string[] {
  const raw = new URLSearchParams(search).get('newCampaign') ?? '';
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .flatMap((s) => {
      const short = /^([^/\s#]+\/[^/\s#]+)#(\d+)$/.exec(s);
      if (short) return [`https://github.com/${short[1]}/issues/${short[2]}`];
      return /^https:\/\/[^/\s?#]+\/[^/\s?#]+\/[^/\s?#]+\/issues\/\d+$/.test(s) ? [s] : [];
    });
}

/** The not-stacked line a chain group's header shows, in the PR tab's stack bar words, or null for any other row. */
export function chainGroupLine(g: ViewGroup): { lead: string; rest: string } | null {
  return g.row.chainRow ? stackBarLine({ shape: 'chained', prs: g.children.map((c) => c.order.url ?? '') }) : null;
}

/** Whether a chain group's header offers Stack on GitHub: only a chain every shown PR of which is the viewer's. */
export function chainGroupCanStack(g: ViewGroup): boolean {
  return !!g.row.chainRow && g.children.length >= 2 && g.children.every((c) => c.order.chain?.mine === true);
}
