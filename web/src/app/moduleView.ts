import type { PageGroup } from './api';

export type ChipTone = 'good' | 'warn' | 'muted' | 'neutral';

const TONES: readonly string[] = ['good', 'warn', 'muted'];

/** A plugin's tone, or neutral for a missing or unknown one. */
export const chipTone = (tone?: string): ChipTone => (tone !== undefined && TONES.includes(tone) ? (tone as ChipTone) : 'neutral');

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
