import type { MooredPR, Ship } from './api';
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
export function prChips(pr: MooredPR): Chip[] {
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

export function staleNote(ship: Ship, now: number): string {
  if (!ship.stale) return '';
  const age = shipAge(ship.polled_at ?? '', now);
  if (!age) return 'Poll failed';
  return `Poll failed · last checked ${age === 'now' ? 'under a minute' : age} ago`;
}
