import type { WireCrew, WireShip } from './ships';

// Crew boats on the bridge (#154). Pure, so the tests need no browser.

// The flotilla row's second line for a crew boat. The wire has no role (#157), so every boat is a Teammate.
export function crewRowText(c: WireCrew): string {
  if (c.status === 'needs_you') return 'Teammate · needs you';
  if (c.status === 'anchored') return `Teammate · anchored, ${c.exit_signal ? `killed (${c.exit_signal})` : `exit ${c.exit_code ?? '?'}`}`;
  return 'Teammate · working';
}

// The bridge socket of a ship, or of one of its crew boats (docs/bridge-protocol.md).
export function bridgePath(session: string, crew?: string): string {
  const ship = `/ws/bridge/${encodeURIComponent(session)}`;
  return crew === undefined ? ship : `${ship}/crew/${encodeURIComponent(crew)}`;
}

export function findCrew(list: { session: string; crew: WireCrew[] }[], session: string, crew: string): WireCrew | undefined {
  return list.find((s) => s.session === session)?.crew.find((c) => c.name === crew);
}

// Whether the boarded terminal takes keys. A crew boat has its own status: a live one of an anchored ship still does.
export function takesInput(ship: Pick<WireShip, 'status'>, crew?: Pick<WireCrew, 'status'>): boolean {
  const status = (crew ?? ship).status;
  return status === 'underway' || status === 'needs_you';
}
