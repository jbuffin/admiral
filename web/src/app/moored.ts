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

/** Why a moored ship cannot be woken: its record has no valid resume text. */
export const NO_RESUME_NOTE = 'Cannot wake: no valid resume text';

/** The moored row's buttons: all are off while a call is out, and Wake also when the ship has no valid resume text. The note is that reason, or else why a due feedback round waits; Wake by hand stays on while one waits. A stale poll changes nothing, since waking needs no GitHub. */
export function rowButtons(ship: Pick<Ship, 'no_resume' | 'waiting'>, busy: boolean): { wake: boolean; others: boolean; note: string } {
  return { wake: busy || !!ship.no_resume, others: busy, note: ship.no_resume ? NO_RESUME_NOTE : (ship.waiting ?? '') };
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

/** The reviewed PRs whose author has responded since the fleet's review, bottom layer first. */
export const respondedPRs = (ship: Pick<Ship, 'prs'>): MooredPR[] => (ship.prs ?? []).filter((p) => p.reviewing && p.responded && p.state === 'OPEN');

/** The URL Re-review wakes the ship with: the stack's when the ship reviews one, else the lowest responded PR's. Exactly as served. */
export const reReviewUrl = (ship: Pick<Ship, 'prs' | 'url'>): string => (isStackUrl(ship.url) ? ship.url! : (respondedPRs(ship)[0]?.url ?? ''));

/** What a carrying ship does next: `ready` when its whole stack can merge (rendered by ReadyToMerge), else the ship's own note, else what its PR waits on. */
export function nextStep(ship: Ship): { ready: boolean; text: string } {
  // Admiral doesn't merge a reviewed PR, though the server still serves stack_ready for it.
  if (reviews(ship)) return { ready: false, text: prSummary(ship.prs) };
  if (ship.stack_ready) return { ready: true, text: '' };
  return { ready: false, text: rowButtons(ship, false).note || prStep(ship.prs ?? []) };
}
