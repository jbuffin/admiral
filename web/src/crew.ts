import type { WireCrew } from './ships';

// Pure, so the tests need no browser.

export function bridgePath(session: string, crew?: string, base = '/ws'): string {
  const ship = `${base}/bridge/${encodeURIComponent(session)}`;
  return crew === undefined ? ship : `${ship}/crew/${encodeURIComponent(crew)}`;
}

export function findCrew(list: { session: string; crew: WireCrew[] }[], session: string, crew: string): WireCrew | undefined {
  return list.find((s) => s.session === session)?.crew?.find((c) => c.name === crew);
}

export const atSea = (s: { status: string }) => s.status === 'underway' || s.status === 'needs_you';

// A subagent's age: under a minute is "just started"; a start that does not parse shows none.
export function age(start: string, now: number): string {
  const t = Date.parse(start);
  if (Number.isNaN(t)) return '';
  const m = Math.max(0, Math.floor((now - t) / 60000));
  return m < 1 ? 'just started' : `${m}m`;
}
