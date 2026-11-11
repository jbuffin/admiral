import type { WireCrew, WireShip } from './ships';

// Pure, so the tests need no browser.

// The wire has no role, so every boat is a Teammate.
export function crewRowText(c: WireCrew): string {
  if (c.status === 'needs_you') return 'Teammate · needs you';
  if (c.status === 'idle') return 'Teammate · idle';
  if (c.status === 'anchored') return `Teammate · anchored, ${c.exit_signal ? `killed (${c.exit_signal})` : `exit ${c.exit_code ?? '?'}`}`;
  return 'Teammate · working';
}

export function bridgePath(session: string, crew?: string, base = '/ws'): string {
  const ship = `${base}/bridge/${encodeURIComponent(session)}`;
  return crew === undefined ? ship : `${ship}/crew/${encodeURIComponent(crew)}`;
}

export function findCrew(list: { session: string; crew: WireCrew[] }[], session: string, crew: string): WireCrew | undefined {
  return list.find((s) => s.session === session)?.crew?.find((c) => c.name === crew);
}

export const atSea = (s: { status: string }) => s.status === 'underway' || s.status === 'needs_you';

// Whether the boarded terminal takes keys. A crew boat has its own status: a live one, idle too, of an anchored ship still does.
export function takesInput(ship: Pick<WireShip, 'status'>, crew?: Pick<WireCrew, 'status'>): boolean {
  return crew ? crew.status !== 'anchored' : atSea(ship);
}

// An argument of `admiral attach`, quoted for a shell when its name needs it: single quotes, and not a leading `-`.
// `admiral attach Saltpaw II` takes a ship's words unquoted, so each word is quoted on its own.
const shellQuote = (a: string) => (/^[A-Za-z0-9._/][A-Za-z0-9._/-]*$/.test(a) ? a : `'${a.replace(/'/g, `'\\''`)}'`);

export function attachCommand(ship: string, crew?: string): string {
  return ['admiral attach', ...ship.split(' ').map(shellQuote), ...(crew === undefined ? [] : [shellQuote(crew)])].join(' ');
}

// A subagent's age: under a minute is "just started"; a start that does not parse shows none.
export function age(start: string, now: number): string {
  const t = Date.parse(start);
  if (Number.isNaN(t)) return '';
  const m = Math.max(0, Math.floor((now - t) / 60000));
  return m < 1 ? 'just started' : `${m}m`;
}
