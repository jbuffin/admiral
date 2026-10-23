// The decisions on a findings item (#116): what the ship wrote, read defensively, and what the engineer has
// decided on each finding card. Pure; inboxState.ts keeps one of these per item.

export type Verdict = 'agree' | 'disagree' | 'unclear';
export type Decision = 'approved' | 'edited' | 'rejected';
export interface Proposal {
  kind: string;
  content: string;
}
export interface FindingCard {
  id: string;
  source: string;
  quote: string;
  verdict: Verdict;
  reason: string;
  proposal: Proposal | null;
  evidence: string[];
}

const text = (v: unknown) => (typeof v === 'string' ? v : '');

// The cards of an item file, which a ship wrote: a malformed entry is dropped or read as empty, never thrown on.
export function readCards(raw: unknown): FindingCard[] {
  if (!Array.isArray(raw)) return [];
  const seen = new Set<string>();
  const out: FindingCard[] = [];
  for (const c of raw) {
    if (!c || typeof c !== 'object') continue;
    const r = c as Record<string, unknown>;
    if (typeof r.id !== 'string' || seen.has(r.id)) continue;
    seen.add(r.id);
    const p = r.proposal as Record<string, unknown> | null;
    out.push({
      id: r.id,
      source: text(r.source),
      quote: text(r.quote),
      verdict: r.verdict === 'agree' || r.verdict === 'disagree' ? r.verdict : 'unclear',
      reason: text(r.reason),
      proposal: p && typeof p === 'object' && typeof p.kind === 'string' && typeof p.content === 'string' ? { kind: p.kind, content: p.content } : null,
      evidence: Array.isArray(r.evidence) ? r.evidence.filter((e): e is string => typeof e === 'string') : [],
    });
  }
  return out;
}

export function createDecisions(cards: FindingCard[]) {
  const byId = new Map(cards.map((c) => [c.id, c]));
  const chosen = new Map<string, Decision>();
  const edits = new Map<string, string>();

  const proposal = (id: string): Proposal | null => {
    const p = byId.get(id)?.proposal ?? null;
    const edit = edits.get(id);
    return p && chosen.get(id) === 'edited' && edit !== undefined ? { kind: p.kind, content: edit } : p;
  };

  return {
    approve(id: string) {
      if (byId.has(id) && chosen.get(id) !== 'edited') chosen.set(id, 'approved');
    },
    reject(id: string) {
      if (!byId.has(id)) return;
      chosen.set(id, 'rejected');
      edits.delete(id);
    },
    save(id: string, content: string): boolean {
      if (!byId.has(id) || content.trim() === '') return false;
      chosen.set(id, 'edited');
      edits.set(id, content);
      return true;
    },
    decision: (id: string) => chosen.get(id),
    proposal,
    progress: () => ({ decided: chosen.size, total: cards.length }),
    canSend: () => chosen.size === cards.length,
    payload() {
      return {
        cards: cards.map((c) => {
          const decision = chosen.get(c.id)!;
          return decision === 'edited' ? { id: c.id, decision, proposal: proposal(c.id)! } : { id: c.id, decision };
        }),
      };
    },
    summary() {
      const n = (d: Decision) => [...chosen.values()].filter((x) => x === d).length;
      const parts = (['approved', 'edited', 'rejected'] as const).filter(n).map((d) => `${n(d)} ${d}`);
      return parts.length === 0 ? 'Sent, no cards' : `Sent ${parts.join(', ')}`;
    },
  };
}

export type Decisions = ReturnType<typeof createDecisions>;
