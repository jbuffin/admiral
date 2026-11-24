import type { Ship } from './api';

export interface AgentRow {
  key: string;
  kindLabel: string;
  name: string;
  type: string;
  start: string;
  end?: string;
  state: 'running' | 'finished';
}

const KIND_LABEL: Record<string, string> = {
  subagent: 'Subagent',
  teammate: 'Agent-team teammate',
  shell: 'Background shell',
  monitor: 'Background monitor',
};

// A finished boat has no end time, so its last activity (else its launch) stands in for it.
const finishedAt = (r: AgentRow & { at: string }): number => Date.parse(r.at) || 0;

/** The Agents tab's rows: crew boats then stored entries, running first, then finished newest first. */
export function agentRows(ship: Pick<Ship, 'crew'> & Partial<Pick<Ship, 'agents'>>): AgentRow[] {
  const rows = [
    ...(ship.crew ?? []).map((b) => {
      const running = b.status !== 'anchored';
      return { key: `boat:${b.name}`, kindLabel: 'Crew boat', name: b.name, type: '', start: b.launched ?? '', state: running ? ('running' as const) : ('finished' as const), at: b.last_activity ?? b.launched ?? '' };
    }),
    ...(ship.agents ?? []).map((a) => ({
      key: `agent:${a.id}`,
      kindLabel: KIND_LABEL[a.kind ?? 'subagent'] ?? KIND_LABEL.subagent,
      name: a.id,
      type: a.type,
      start: a.start,
      end: a.end,
      state: a.end ? ('finished' as const) : ('running' as const),
      at: a.end ?? a.start,
    })),
  ];
  const running = rows.filter((r) => r.state === 'running');
  const finished = rows.filter((r) => r.state === 'finished').sort((a, b) => finishedAt(b) - finishedAt(a));
  return [...running, ...finished].map(({ at: _at, ...r }) => r);
}

export const runningAgents = (ship: Pick<Ship, 'crew'> & Partial<Pick<Ship, 'agents'>>): number => agentRows(ship).filter((r) => r.state === 'running').length;
