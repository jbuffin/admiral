import type { MooredPR, Order, Ship } from './api';
import { feedbackBarText, prSummary } from './prTab';
import { GROUP_TITLES, shipAge } from './shipList';

export type ChipTone = 'ok' | 'err' | 'muted';
export interface Chip {
  text: string;
  tone: ChipTone;
}

const REVIEW: Record<string, Chip> = {
  APPROVED: { text: 'Approved', tone: 'ok' },
  CHANGES_REQUESTED: { text: 'Changes requested', tone: 'err' },
  REVIEW_REQUIRED: { text: 'Review required', tone: 'muted' },
};
const CHECKS: Record<string, Chip> = {
  SUCCESS: { text: 'Checks passing', tone: 'ok' },
  FAILURE: { text: 'Checks failing', tone: 'err' },
  ERROR: { text: 'Checks failing', tone: 'err' },
  PENDING: { text: 'Checks pending', tone: 'muted' },
  EXPECTED: { text: 'Checks pending', tone: 'muted' },
};
const MERGE: Record<string, Chip> = {
  MERGEABLE: { text: 'Mergeable', tone: 'ok' },
  CONFLICTING: { text: 'Conflicts', tone: 'err' },
};
const ENDED: Record<string, Chip> = {
  MERGED: { text: 'Merged', tone: 'muted' },
  CLOSED: { text: 'Closed', tone: 'muted' },
};

// A value the table doesn't know shows no chip rather than a guess.
export function prChips(pr: Pick<MooredPR, 'state' | 'review' | 'checks' | 'mergeable'>): Chip[] {
  const ended = ENDED[pr.state];
  if (ended) return [ended];
  return [REVIEW[pr.review], CHECKS[pr.checks], MERGE[pr.mergeable]].filter((c): c is Chip => c !== undefined);
}

/** `https://github.com/acme/api/pull/812` → `api#812`. */
export function prLabel(url: string): string {
  const m = /\/([^/]+)\/pull\/(\d+)/.exec(url);
  return m ? `${m[1]}#${m[2]}` : url;
}

/** Why a moored ship's wake starts fresh: its record has no valid resume text, so the typed message is the whole prompt. */
export const NO_RESUME_NOTE = 'No resume text: Wake starts it fresh, with your message as its prompt';

/** The moored row's buttons: all are off while a call is out. The note is why a due feedback round waits; Wake by hand stays on while one waits. A stale poll changes nothing, since waking needs no GitHub. */
export function rowButtons(ship: Pick<Ship, 'waiting'>, busy: boolean): { wake: boolean; others: boolean; note: string } {
  return { wake: busy, others: busy, note: ship.waiting ?? '' };
}

export function staleNote(ship: Ship, now: number): string {
  if (!ship.stale) return '';
  const age = shipAge(ship.polled_at ?? '', now);
  if (!age) return 'Poll failed';
  return `Poll failed · last checked ${age === 'now' ? 'under a minute' : age} ago`;
}

/** What Merge will do: the repository's method, and for a stack that the whole stack merges. */
export const mergeHint = (method?: string, stack = false): string => {
  const how = ({ squash: 'squash', rebase: 'rebase', merge: 'merge commit' })[method as 'merge'] ?? '';
  return stack ? [how, 'whole stack'].filter(Boolean).join(' · ') : how;
};

const prKey = (url: string) => url.replace(/\/+$/, '').toLowerCase();

/** What the first open PR that is not ready waits on: the first of conflicts, failing checks, requested changes, pending checks, a missing review. */
function prStep(prs: MooredPR[]): string {
  // A merged or closed layer waits on nothing, so it is skipped.
  const layer = prs.find((p) => p.state === 'OPEN' && !p.ready);
  if (!layer) return '';
  if (layer.conflicts || layer.mergeable === 'CONFLICTING') return 'Has conflicts';
  if (layer.checks === 'FAILURE' || layer.checks === 'ERROR') return 'Checks failing';
  if (layer.review === 'CHANGES_REQUESTED') return 'Changes requested';
  if (layer.checks === 'PENDING' || layer.checks === 'EXPECTED') return 'Checks pending';
  if (layer.review === 'REVIEW_REQUIRED' || !layer.review) return 'Waiting on review';
  return '';
}

/** A GitHub stack URL, `https://<host>/<owner>/<repo>/stack/<N>`. */
export const isStackUrl = (url: string | undefined): boolean => /\/stack\/\d+\/?$/.test(url ?? '');

/** Whether the ship carries a PR it reviews rather than one it opened. */
export const reviews = (ship: Pick<Ship, 'prs'>): boolean => (ship.prs ?? []).some((p) => p.reviewing);

/** Why Merge isn't here yet, shown beside Moor on a live ship's PR tab. */
export const MOOR_LINE = 'Admiral merges only from a moored ship. Moor ends the session and keeps its PRs; a feedback round or your next message wakes it in a new session.';

/** The open PRs a live ship opened itself; reviewed ones never merge from Admiral. */
const ownOpenPRs = (ship: Pick<Ship, 'prs'>): MooredPR[] => (ship.prs ?? []).filter((p) => p.state === 'OPEN' && !p.reviewing);

/** Whether a live ship's PR tab offers Moor: it carries an open PR it opened, and isn't moored or returning. */
export function offersMoor(ship: Pick<Ship, 'status' | 'prs' | 'returning'>): boolean {
  return ship.status !== 'moored' && !ship.returning && ownOpenPRs(ship).length > 0;
}

/** What the Moor sheet says: the open PRs it keeps, why Moor is the way to merge, and for an underway ship that its turn is cut off. */
export function moorSheetParts(ship: Pick<Ship, 'name' | 'status' | 'prs'>): { urls: string[]; explain: string } {
  const urls = ownOpenPRs(ship).map((p) => p.url);
  const one = urls.length === 1;
  const cut = ship.status === 'underway' ? ` ${ship.name} is mid-turn: the turn is cut off.` : '';
  return {
    urls,
    explain: `Moor ends the session and keeps ${one ? 'the PR' : 'the PRs'}, so you can merge ${one ? 'it' : 'them'} here. A feedback round or a typed message wakes the ship in a new session.${cut}`,
  };
}

/** The reviewed PRs whose author has responded since the fleet's review, bottom layer first. */
export const respondedPRs = (ship: Pick<Ship, 'prs'>): MooredPR[] => (ship.prs ?? []).filter((p) => p.reviewing && p.responded && p.state === 'OPEN');

/** Where Re-review is offered and with which URL, exactly as served: on the stack bar for a stack-URL ship with two or more PRs, else on the responded PR (with the stack's URL for a stack-URL ship); null unless the ship is moored with a responded reviewed PR. */
export function reReviewPlace(ship: Pick<Ship, 'status' | 'prs' | 'url'>): { place: 'stack' | 'pr'; url: string } | null {
  const responded = respondedPRs(ship);
  if (ship.status !== 'moored' || responded.length === 0) return null;
  if (!isStackUrl(ship.url)) return { place: 'pr', url: responded[0].url };
  return { place: (ship.prs ?? []).length >= 2 ? 'stack' : 'pr', url: ship.url! };
}

/** What a carrying ship does next: `ready` when its whole stack can merge (rendered by ReadyToMerge), else the ship's own note, else what its PR waits on. */
export function nextStep(ship: Ship): { ready: boolean; text: string } {
  // Admiral doesn't merge a reviewed PR, though the server still serves stack_ready for it.
  if (reviews(ship)) return { ready: false, text: prSummary(ship.prs) };
  if (ship.stack_ready) return { ready: true, text: '' };
  return { ready: false, text: rowButtons(ship, false).note || prStep(ship.prs ?? []) };
}

const ON_ORDER: Ship['status'][] = ['underway', 'needs_you', 'moored'];

const requestedAgain = (o: Order): boolean => !!o.review_requested_at && o.reviewed_since_push !== true;

/** The own open PR a moored ship wakes on: the order's own when it has feedback, else the first with feedback. */
function wakePR(ship: Ship, order: Order): MooredPR | undefined {
  const key = prKey(order.url ?? '');
  const open = ownOpenPRs(ship).filter((p) => feedbackBarText(p) !== '');
  return open.find((p) => prKey(p.url) === key) ?? open[0];
}

/** The URL Re-review sends, exactly as the server serves it; null when nothing asks for a re-review. */
function reReviewUrl(ship: Ship, order: Order): string | null {
  const place = reReviewPlace(ship);
  if (place) return place.url;
  if (ship.status !== 'moored' || !reviews(ship) || !requestedAgain(order)) return null;
  const key = prKey(order.url ?? '');
  const matched = (ship.prs ?? []).find((p) => p.reviewing && prKey(p.url) === key);
  if (matched) return matched.url;
  return isStackUrl(ship.url) ? ship.url! : null;
}

export type RowAgain = { kind: 'rereview'; url: string } | { kind: 'fix'; pr: string } | { kind: 'wake'; pr: string } | null;

function againOf(ship: Ship, order: Order): RowAgain {
  if (ship.status !== 'moored') return null;
  const url = reReviewUrl(ship, order);
  if (url) return { kind: 'rereview', url };
  if (ship.fix) return { kind: 'fix', pr: ship.fix.pr };
  const pr = wakePR(ship, order);
  return pr ? { kind: 'wake', pr: pr.url } : null;
}

/** The live or moored ships on an order, the one its row shows first: a moored ship whose PR needs you again, then needs_you, underway, moored; newest launch first within each. */
export function shipsOnOrder(ships: Ship[], order: Order): Ship[] {
  if (!order.url) return [];
  const key = prKey(order.url);
  const stackKey = order.stack ? prKey(order.stack.url) : '';
  const hits = ships.filter((s) => ON_ORDER.includes(s.status) && [s.url, s.link, ...(s.issues ?? []), ...(s.prs ?? []).map((p) => p.url)].some((u) => !!u && (prKey(u) === key || prKey(u) === stackKey)));
  const rank = (s: Ship) => (againOf(s, order) ? 0 : ({ needs_you: 1, underway: 2, moored: 3 })[s.status as 'moored']);
  const at = (s: Ship) => Date.parse(s.launched) || 0;
  return hits.sort((a, b) => rank(a) - rank(b) || at(b) - at(a));
}

/** The moored ship on the order, wherever it ranks, so Merge doesn't hide behind a live ship. */
export const mooredShipOn = (ships: Ship[], order: Order): Ship | undefined => shipsOnOrder(ships, order).find((s) => s.status === 'moored');

/** The Re-review sheet's line when the order asks for the review and no PR of the ship is responded; `url` is what Re-review sends. */
export function askedAgainLine(url: string): string {
  const n = /\/pull\/(\d+)\/?$/.exec(url)?.[1];
  return `You were asked to review ${isStackUrl(url) || !n ? 'the whole stack' : `#${n}`} again.`;
}

export type RowAction = { kind: 'launch' } | { kind: 'ship'; ship: Ship; word: string; needsYou: boolean; again: RowAgain };

/** What an order's row offers: the launch arrow, or its ship's pill with Re-review or Wake when a moored ship's PR needs you again. */
export function orderRowAction(ships: Ship[], order: Order): RowAction {
  const ship = shipsOnOrder(ships, order)[0];
  if (!ship) return { kind: 'launch' };
  return { kind: 'ship', ship, word: GROUP_TITLES[ship.status as keyof typeof GROUP_TITLES], needsYou: ship.status === 'needs_you', again: againOf(ship, order) };
}

/** Whether a moored ship on the order has its PR needing you again, which puts the order in Needs you. */
export const needsYouAgain = (ships: Ship[], order: Order): boolean => {
  const a = orderRowAction(ships, order);
  return a.kind === 'ship' && a.again !== null;
};
