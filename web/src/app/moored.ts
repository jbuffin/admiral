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
