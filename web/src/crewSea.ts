import type { WireCrew, WireSubagent } from './ships';

// Pure, so the tests need no browser.

export const RAFT_MAX = 3; // tokens.json size.crewRaft.max

export function raft(crew: WireCrew[]): { shown: WireCrew[]; hidden: WireCrew[]; pill: { text: string; name: string } | null } {
  const shown = crew.slice(0, RAFT_MAX);
  const hidden = crew.slice(RAFT_MAX);
  const pill = hidden.length ? { text: `+${hidden.length} crew`, name: `${hidden.length} more crew boats` } : null;
  return { shown, hidden, pill };
}

const kind = (s: WireSubagent) => s.type || 'subagent';
const count = (n: number) => `${n} subagent${n === 1 ? '' : 's'}`;

export const deckLabel = (shipName: string, subs: WireSubagent[]) =>
  `${count(subs.length)} on ${shipName}: ${subs.map(kind).join(', ')}. They have no terminal`;
