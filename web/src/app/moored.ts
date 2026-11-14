import type { MooredPR, Ship } from './api';
import { prSummary } from './prTab';
import { shipAge } from './shipList';

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

/** The moored ship whose PRs include the order's URL, comparing case-insensitively and ignoring a trailing slash. */
export function carryingShip(ships: Ship[], orderUrl: string): Ship | undefined {
  const key = prKey(orderUrl);
  return ships.find((s) => s.status === 'moored' && (s.prs ?? []).some((p) => prKey(p.url) === key));
}

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
