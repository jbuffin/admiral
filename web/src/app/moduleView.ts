import type { PageGroup } from './api';

export type ChipTone = 'good' | 'warn' | 'muted' | 'neutral';

const TONES: readonly string[] = ['good', 'warn', 'muted'];

/** A plugin's tone, or neutral for a missing or unknown one. */
export const chipTone = (tone?: string): ChipTone => (tone !== undefined && TONES.includes(tone) ? (tone as ChipTone) : 'neutral');

export type NoticeTone = 'info' | 'warn' | 'error';

const NOTICE_TONES: readonly string[] = ['warn', 'error'];

/** A notice's tone, or info for a missing or unknown one. */
export const noticeTone = (tone?: string): NoticeTone => (tone !== undefined && NOTICE_TONES.includes(tone) ? (tone as NoticeTone) : 'info');

/** The groups narrowed to rows whose title, subtitle or a chip label holds the query; a group with none left drops. */
export function filterGroups(groups: PageGroup[], query: string): PageGroup[] {
  const q = query.trim().toLowerCase();
  if (!q) return groups;
  const has = (s?: string) => !!s && s.toLowerCase().includes(q);
  return groups
    .map((g) => ({ ...g, rows: g.rows.filter((r) => has(r.title) || has(r.subtitle) || (r.chips ?? []).some((c) => has(c.label))) }))
    .filter((g) => g.rows.length > 0);
}

/** Whether a page change makes the open row's detail stale: it names the row, names none at all, or skipped a version. */
export function shouldRefetchRow(open: string, prev: number, next: { version: number; rows?: string[] }): boolean {
  if (next.version === prev) return false;
  return next.rows === undefined || next.rows.includes(open) || next.version - prev > 1;
}

/** The longest duration a form takes: the protocol's hh:mm has two-digit hours. */
export const MAX_DURATION = 99 * 60 + 59;

/**
 * Minutes from what the engineer typed, or null when it can't be read. A plain whole number is minutes and a decimal is
 * hours; h:m takes one or two digits a side, and units read as 1h30, 45m, 1.5h or 1 hour 30 minutes.
 */
export function parseDuration(raw: string): number | null {
  const s = raw.trim().toLowerCase().replace(/\s+/g, ' ');
  if (!s) return null;
  let m = /^(\d{1,2}):(\d{1,2})$/.exec(s);
  if (m) return Number(m[1]) * 60 + Number(m[2]);
  if (/^\d+$/.test(s)) return Number(s);
  if (/^\d*\.\d+$/.test(s)) return Math.round(parseFloat(s) * 60);
  m = /^(?:(\d*\.?\d+) ?h(?:r|rs|our|ours)?)? ?(?:(\d+) ?(m|min|mins|minute|minutes)?)?$/.exec(s);
  // A bare trailing number with no hours before it is the plain-number case above, so it needs a unit here.
  if (!m || (!m[1] && !m[2]) || (!m[1] && !m[3])) return null;
  return Math.round((m[1] ? parseFloat(m[1]) * 60 : 0) + (m[2] ? Number(m[2]) : 0));
}

/** Minutes as the engineer reads them: 45 min, 2 h, 1 h 30 min. */
export const durationLabel = (n: number) => (n >= 60 ? `${Math.floor(n / 60)} h${n % 60 ? ` ${n % 60} min` : ''}` : `${n} min`);

/** Minutes as the hh:mm a plugin's form takes. */
export const toHHMM = (n: number) => `${String(Math.floor(n / 60)).padStart(2, '0')}:${String(n % 60).padStart(2, '0')}`;
