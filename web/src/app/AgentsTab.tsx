import type { Ship } from './api';
import { agentRows } from './agents';
import { clockTime } from './chat';

const Stamp = ({ at }: { at: string }) => (
  <time dateTime={at}>{clockTime(at) || at}</time>
);

/** The ship's crew boats, subagents, teammates and background tasks as plain rows. */
export function AgentsTab({ ship }: { ship: Ship }) {
  const rows = agentRows(ship);
  if (rows.length === 0) return <p class="agents-empty">No crew, subagents or background tasks yet</p>;
  return (
    <ul class="agents-list">
      {rows.map((r) => (
        <li key={r.key} class={`agent-row ${r.state}`}>
          <span class="agent-kind">{r.kindLabel}</span>
          <span class="agent-name">{r.name}</span>
          {r.type ? <span class="agent-type">{r.type}</span> : null}
          <span class="agent-state">{r.state === 'running' ? 'running' : 'finished'}</span>
          {r.start ? (
            <span class="agent-times">
              <Stamp at={r.start} />
              {r.end ? (
                <>
                  {' to '}
                  <Stamp at={r.end} />
                </>
              ) : null}
            </span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}
