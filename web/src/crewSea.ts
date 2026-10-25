import { age } from './crew';
import type { WireCrew, WireShip, WireSubagent } from './ships';

// Crew boats and subagents on the sea (#154). Pure, so the tests need no browser.

export const crewAsks = (c: Pick<WireCrew, 'status'>) => c.status === 'needs_you';

// The plaque's line while crew boats need you. It is keyed on the boats, not the ship's `reason`, which the
// server keeps as the ship's own while the ship needs you itself.
export function plaqueNeeds(crew: WireCrew[]): string | null {
  const asking = crew.filter(crewAsks);
  return asking.length ? `${asking.map((c) => c.name).join(', ')} needs you` : null;
}

export const RAFT_MAX = 3; // tokens.json size.crewRaft.max

// The first three boats raft beside the ship; the rest are counted in a pill.
export function raft(crew: WireCrew[]): { shown: WireCrew[]; hidden: WireCrew[]; pill: { text: string; name: string } | null } {
  const shown = crew.slice(0, RAFT_MAX);
  const hidden = crew.slice(RAFT_MAX);
  const pill = hidden.length
    ? { text: `+${hidden.length} crew${hidden.some(crewAsks) ? ' !' : ''}`, name: `${hidden.length} more crew boats; board the ship to see them` }
    : null;
  return { shown, hidden, pill };
}

// Whether this `ships` message plays the chime: a ship turns needs_you, or a crew boat is needs_you that was not.
// One answer per message, however many turned. A ship that is new in `now` never chimes, nor do its boats.
export function shouldChime(before: WireShip[], now: WireShip[], first: boolean): boolean {
  if (first) return false;
  const was = new Map(before.map((s) => [s.session, s]));
  return now.some((s) => {
    const prev = was.get(s.session);
    if (!prev) return false;
    if (prev.status !== 'needs_you' && s.status === 'needs_you') return true;
    const asked = new Set((prev.crew ?? []).filter(crewAsks).map((c) => c.name));
    return (s.crew ?? []).some((c) => crewAsks(c) && !asked.has(c.name));
  });
}

const kind = (s: WireSubagent) => s.type || 'subagent';
const count = (n: number) => `${n} subagent${n === 1 ? '' : 's'}`;

export const subagentTip = (subs: WireSubagent[], now: number) =>
  `${count(subs.length)} · ${subs.map((s) => `${kind(s)} ${age(s.start, now) || 'just started'}`).join(' · ')} · no terminal`;

export const deckLabel = (shipName: string, subs: WireSubagent[]) =>
  `${count(subs.length)} on ${shipName}: ${subs.map(kind).join(', ')}. They have no terminal`;
